/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package sync

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

// The max number of key per listing request
const (
	maxResults      = 1000
	defaultPartSize = 5 << 20
	maxBlock        = defaultPartSize * 2
	markDeleteSrc   = -1
	markDeleteDst   = -2
	markCopyPerms   = -3
)

var (
	total               int64
	bar                 *mpb.Bar
	copied, copiedBytes *mpb.Bar
	failed, deleted     *mpb.Bar
	concurrent          chan int
	limiter             *ratelimit.Bucket
)

var logger = utils.GetLogger("juicefs")

// human readable bytes size
func formatSize(bytes int64) string {
	units := [7]string{" ", "K", "M", "G", "T", "P", "E"}
	if bytes < 1024 {
		return fmt.Sprintf("%v B", bytes)
	}
	z := 0
	v := float64(bytes)
	for v > 1024.0 {
		z++
		v /= 1024.0
	}
	return fmt.Sprintf("%.3f %siB", v, units[z])
}

// ListAll on all the keys that starts at marker from object storage.
func ListAll(store object.ObjectStorage, start, end string) (<-chan object.Object, error) {
	startTime := time.Now()
	logger.Debugf("Iterating objects from %s start %q", store, start)

	out := make(chan object.Object, maxResults*10)

	// As the result of object storage's List method doesn't include the marker key,
	// we try List the marker key separately.
	if start != "" {
		if obj, err := store.Head(start); err == nil {
			logger.Debugf("Found start key: %s from %s in %s", start, store, time.Since(startTime))
			out <- obj
		}
	}

	if ch, err := store.ListAll("", start); err == nil {
		if end == "" {
			go func() {
				for obj := range ch {
					out <- obj
				}
				close(out)
			}()
			return out, nil
		}

		go func() {
			for obj := range ch {
				if obj != nil && obj.Key() > end {
					break
				}
				out <- obj
			}
			close(out)
		}()
		return out, nil
	}

	marker := start
	logger.Debugf("Listing objects from %s marker %q", store, marker)
	objs, err := store.List("", marker, maxResults)
	if err != nil {
		logger.Errorf("Can't list %s: %s", store, err.Error())
		return nil, err
	}
	logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
	go func() {
		lastkey := ""
		first := true
	END:
		for len(objs) > 0 {
			for _, obj := range objs {
				key := obj.Key()
				if !first && key <= lastkey {
					logger.Fatalf("The keys are out of order: marker %q, last %q current %q", marker, lastkey, key)
				}
				if end != "" && key > end {
					break END
				}
				lastkey = key
				// logger.Debugf("key: %s", key)
				out <- obj
				first = false
			}
			// Corner case: the func parameter `marker` is an empty string("") and exactly
			// one object which key is an empty string("") returned by the List() method.
			if lastkey == "" {
				break END
			}

			marker = lastkey
			startTime = time.Now()
			logger.Debugf("Continue listing objects from %s marker %q", store, marker)
			objs, err = store.List("", marker, maxResults)
			count := 0
			for err != nil && count < 3 {
				logger.Warnf("Fail to list: %s, retry again", err.Error())
				// slow down
				time.Sleep(time.Millisecond * 100)
				objs, err = store.List("", marker, maxResults)
				count++
			}
			logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
			if err != nil {
				// Telling that the listing has failed
				out <- nil
				logger.Errorf("Fail to list after %s: %s", marker, err.Error())
				break
			}
			if len(objs) > 0 && objs[0].Key() == marker {
				// workaround from a object store that is not compatible to S3.
				objs = objs[1:]
			}
		}
		close(out)
	}()
	return out, nil
}

var bufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 32<<10)
		return &buf
	},
}

func copyObject(src, dst object.ObjectStorage, obj object.Object) error {
	if limiter != nil {
		limiter.Wait(obj.Size())
	}
	concurrent <- 1
	defer func() {
		<-concurrent
	}()
	key := obj.Key()

	if obj.Size() <= maxBlock ||
		strings.HasPrefix(src.String(), "file://") ||
		strings.HasPrefix(dst.String(), "file://") {
		in, e := src.Get(key, 0, -1)
		if e != nil {
			if _, err := src.Head(key); err != nil {
				return nil
			}
			return e
		}
		defer in.Close()
		return dst.Put(key, in)
	}

	// obj.Size > maxBlock, download the object into disk first
	in, e := src.Get(key, 0, int64(maxBlock))
	if e != nil {
		if _, err := src.Head(key); err != nil {
			return nil
		}
		return e
	}
	defer in.Close()

	f, e := ioutil.TempFile("", "rep")
	if e != nil {
		return e
	}
	os.Remove(f.Name()) // will be deleted after Close()
	defer f.Close()

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	n, e := io.CopyBuffer(f, in, *buf)
	if e != nil {
		return e
	}
	if n != maxBlock {
		return fmt.Errorf("Copy returns %d != expect %d", n, maxBlock)
	}
	left, e := src.Get(key, int64(maxBlock), -1)
	if e != nil {
		return e
	}
	defer left.Close()
	if _, e = io.CopyBuffer(f, left, *buf); e != nil {
		return e
	}
	// upload
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	return dst.Put(key, f)
}

func try(n int, f func() error) (err error) {
	for i := 0; i < n; i++ {
		err = f()
		if err == nil {
			return
		}
		time.Sleep(time.Second * time.Duration(i*i))
	}
	return
}

func copyInParallel(src, dst object.ObjectStorage, obj object.Object) error {
	if obj.Size() < maxBlock {
		return try(3, func() error {
			return copyObject(src, dst, obj)
		})
	}
	upload, err := dst.CreateMultipartUpload(obj.Key())
	if err != nil {
		return try(3, func() error {
			return copyObject(src, dst, obj)
		})
	}
	partSize := int64(upload.MinPartSize)
	if partSize == 0 {
		partSize = defaultPartSize
	}
	if obj.Size() > partSize*int64(upload.MaxCount) {
		partSize = obj.Size() / int64(upload.MaxCount)
		partSize = ((partSize-1)>>20 + 1) << 20 // align to MB
	}
	n := int((obj.Size()-1)/partSize) + 1
	key := obj.Key()
	logger.Debugf("Copying object %s as %d parts (size: %d): %s", key, n, partSize, upload.UploadID)
	parts := make([]*object.Part, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(num int) {
			sz := partSize
			if num == n-1 {
				sz = obj.Size() - int64(num)*partSize
			}
			var err error
			if limiter != nil {
				limiter.Wait(sz)
			}
			concurrent <- 1
			defer func() {
				<-concurrent
			}()
			data := make([]byte, sz)
			err = try(3, func() error {
				r, err := src.Get(key, int64(num)*partSize, int64(sz))
				if err != nil {
					return err
				}
				_, err = io.ReadFull(r, data)
				return err
			})
			if err == nil {
				err = try(3, func() error {
					// PartNumber starts from 1
					parts[num], err = dst.UploadPart(key, upload.UploadID, num+1, data)
					return err
				})
			}
			if err != nil {
				errs <- fmt.Errorf("part %d: %s", num, err.Error())
				logger.Warningf("Failed to copy %s part %d: %s", key, num, err.Error())
			} else {
				errs <- nil
				logger.Debugf("Copied %s part %d", key, num)
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		if err = <-errs; err != nil {
			break
		}
	}
	if err == nil {
		err = try(3, func() error {
			return dst.CompleteUpload(key, upload.UploadID, parts)
		})
	}
	if err != nil {
		dst.AbortUpload(key, upload.UploadID)
		return fmt.Errorf("multipart: %s", err.Error())
	}
	return nil
}

func worker(tasks <-chan object.Object, src, dst object.ObjectStorage, config *Config) {
	deleteObj := func(storage object.ObjectStorage, obj object.Object, start time.Time, dry bool) {
		if dry {
			logger.Infof("Will delete %s from %s", obj.Key(), storage)
			return
		}
		if err := try(3, func() error {
			return storage.Delete(obj.Key())
		}); err == nil {
			deleted.Increment()
			logger.Debugf("Deleted %s from %s in %s", obj.Key(), storage, time.Since(start))
		} else {
			failed.Increment()
			logger.Errorf("Failed to delete %s from %s: %s", obj.Key(), storage, err)
		}
	}
	copyPerms := func(obj object.Object, start time.Time, log, dry bool) {
		fi := obj.(object.File)
		if err := dst.(object.FileSystem).Chmod(obj.Key(), fi.Mode()); err != nil {
			logger.Warnf("Chmod %s to %d: %s", obj.Key(), fi.Mode(), err)
		}
		if err := dst.(object.FileSystem).Chown(obj.Key(), fi.Owner(), fi.Group()); err != nil {
			logger.Warnf("Chown %s to (%s,%s): %s", obj.Key(), fi.Owner(), fi.Group(), err)
		}
		if log {
			logger.Debugf("Copied permissions (%s:%s:%s) for %s in %s", fi.Owner(), fi.Group(), fi.Mode(), obj.Key(), time.Since(start))
		}
	}

	for obj := range tasks {
		start := time.Now()
		switch obj.Size() {
		case markDeleteSrc:
			deleteObj(src, obj, start, config.Dry)
		case markDeleteDst:
			deleteObj(dst, obj, start, config.Dry)
		case markCopyPerms:
			if config.Dry {
				logger.Infof("Will copy permissions for %s", obj.Key())
				break
			}
			copyPerms(obj, start, true, config.Dry)
			copied.Increment()
		default:
			if config.Dry {
				logger.Infof("Will copy %s (%d bytes)", obj.Key(), obj.Size())
				break
			}
			if err := copyInParallel(src, dst, obj); err == nil {
				if mc, ok := dst.(object.MtimeChanger); ok {
					if err = mc.Chtimes(obj.Key(), obj.Mtime()); err != nil {
						logger.Warnf("Update mtime of %s: %s", obj.Key(), err)
					}
				}
				if config.Perms {
					copyPerms(obj, start, false, config.Dry)
				}
				copied.Increment()
				copiedBytes.IncrInt64(obj.Size())
				logger.Debugf("Copied %s (%d bytes) in %s", obj.Key(), obj.Size(), time.Since(start))
			} else {
				failed.Increment()
				logger.Errorf("Failed to copy %s: %s", obj.Key(), err)
			}
		}
		bar.Increment()
	}
}

type withSize struct {
	object.Object
	nsize int64
}

func (o *withSize) Size() int64 {
	return o.nsize
}

type withFSize struct {
	object.File
	nsize int64
}

func (o *withFSize) Size() int64 {
	return o.nsize
}

func deleteFromDst(tasks chan<- object.Object, dstobj object.Object, dirs bool) {
	if !dirs && dstobj.IsDir() {
		logger.Debug("Ignore deleting dst directory ", dstobj.Key())
		return
	}
	tasks <- &withSize{dstobj, markDeleteDst}
	total++
	bar.SetTotal(total, false)
}

func producer(tasks chan<- object.Object, src, dst object.ObjectStorage, config *Config) {
	start, end := config.Start, config.End
	logger.Infof("Syncing from %s to %s", src, dst)
	if start != "" {
		logger.Infof("first key: %q", start)
	}
	if end != "" {
		logger.Infof("last key: %q", end)
	}
	logger.Debugf("maxResults: %d, defaultPartSize: %d, maxBlock: %d", maxResults, defaultPartSize, maxBlock)

	srckeys, err := ListAll(src, start, end)
	if err != nil {
		logger.Fatal(err)
	}

	dstkeys, err := ListAll(dst, start, end)
	if err != nil {
		logger.Fatal(err)
	}
	if config.Exclude != nil {
		srckeys = filter(srckeys, config.Include, config.Exclude)
		dstkeys = filter(dstkeys, config.Include, config.Exclude)
	}

	defer close(tasks)
	var dstobj object.Object
	for obj := range srckeys {
		if obj == nil {
			logger.Errorf("Listing failed, stop syncing, waiting for pending ones")
			return
		}
		if !config.Dirs && obj.IsDir() {
			logger.Debug("Ignore directory ", obj.Key())
			continue
		}
		total++
		bar.SetTotal(total, false)

		if dstobj != nil && obj.Key() > dstobj.Key() {
			if config.DeleteDst {
				deleteFromDst(tasks, dstobj, config.Dirs)
			}
			dstobj = nil
		}
		if dstobj == nil {
			for dstobj = range dstkeys {
				if dstobj == nil {
					logger.Errorf("Listing failed, stop syncing, waiting for pending ones")
					return
				}
				if obj.Key() <= dstobj.Key() {
					break
				}
				if config.DeleteDst {
					deleteFromDst(tasks, dstobj, config.Dirs)
				}
				dstobj = nil
			}
		}
		// assert(dstobj == nil || obj.Key() <= dstobj.Key())

		// FIXME: there is a race when source is modified during coping
		if dstobj == nil || obj.Key() < dstobj.Key() || config.ForceUpdate ||
			obj.Size() != dstobj.Size() || config.Update && obj.Mtime().After(dstobj.Mtime()) {
			tasks <- obj
		} else if config.DeleteSrc && obj.Size() == dstobj.Size() { // obj.Key() == dstobj.Key()
			tasks <- &withSize{obj, markDeleteSrc}
		} else if config.Perms {
			f1 := obj.(object.File)
			f2 := dstobj.(object.File)
			if f2.Mode() != f1.Mode() || f2.Owner() != f1.Owner() || f2.Group() != f1.Group() {
				tasks <- &withFSize{f1, markCopyPerms}
			}
		}
		if dstobj != nil && dstobj.Key() == obj.Key() {
			dstobj = nil
		}
	}
	if config.DeleteDst {
		if dstobj != nil {
			deleteFromDst(tasks, dstobj, config.Dirs)
		}
		for dstobj = range dstkeys {
			if dstobj != nil {
				deleteFromDst(tasks, dstobj, config.Dirs)
			}
		}
	}
}

func compileExp(patterns []string) []*regexp.Regexp {
	var rs []*regexp.Regexp
	for _, p := range patterns {
		r, err := regexp.CompilePOSIX(p)
		if err != nil {
			logger.Fatalf("invalid regular expression `%s`: %s", p, err)
		}
		rs = append(rs, r)
	}
	return rs
}

func findAny(s string, ps []*regexp.Regexp) bool {
	for _, p := range ps {
		if p.FindString(s) != "" {
			return true
		}
	}
	return false
}

func filter(keys <-chan object.Object, include, exclude []string) <-chan object.Object {
	inc := compileExp(include)
	exc := compileExp(exclude)
	r := make(chan object.Object)
	go func() {
		for o := range keys {
			if o == nil {
				break
			}
			if findAny(o.Key(), exc) {
				logger.Debugf("exclude %s", o.Key())
				continue
			}
			if len(inc) > 0 && !findAny(o.Key(), inc) {
				logger.Debugf("%s is not included", o.Key())
				continue
			}
			r <- o
		}
		close(r)
	}()
	return r
}

// Sync syncs all the keys between to object storage
func Sync(src, dst object.ObjectStorage, config *Config) error {
	var bufferSize = 10240
	if config.Manager != "" {
		bufferSize = 100
	}
	tasks := make(chan object.Object, bufferSize)
	wg := sync.WaitGroup{}
	concurrent = make(chan int, config.Threads)
	if config.BWLimit > 0 {
		bps := float64(config.BWLimit*(1<<20)/8) * 0.85 // 15% overhead
		limiter = ratelimit.NewBucketWithRate(bps, int64(bps)*3)
	}

	var progress *mpb.Progress
	progress, bar = utils.NewDynProgressBar("scanning objects: ", config.Verbose || config.Quiet || config.Manager != "")
	addSpinner := func(name string) *mpb.Bar {
		return progress.Add(0,
			utils.NewSpinner(),
			mpb.PrependDecorators(
				decor.Name(name+" count: ", decor.WCSyncWidth),
				decor.CurrentNoUnit("%d", decor.WCSyncWidthR),
			),
			mpb.BarFillerClearOnComplete(),
		)
	}
	copied = addSpinner("copied")
	copiedBytes = progress.Add(0,
		utils.NewSpinner(),
		mpb.PrependDecorators(
			decor.Name("copied bytes: ", decor.WCSyncWidth),
			decor.CurrentKibiByte("% d", decor.WCSyncWidthR),
		),
		mpb.BarFillerClearOnComplete(),
	)
	failed, deleted = addSpinner("failed"), addSpinner("deleted")

	for i := 0; i < config.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(tasks, src, dst, config)
		}()
	}

	if config.Manager == "" {
		go producer(tasks, src, dst, config)
		if config.Workers != nil {
			addr, err := startManager(tasks)
			if err != nil {
				return err
			}
			launchWorker(addr, config, &wg)
		}
	} else {
		go fetchJobs(tasks, config)
		go func() {
			for {
				sendStats(config.Manager)
				time.Sleep(time.Second)
			}
		}()
	}

	wg.Wait()
	bar.SetTotal(0, true)
	copied.SetTotal(0, true)
	copiedBytes.SetTotal(0, true)
	failed.SetTotal(0, true)
	deleted.SetTotal(0, true)
	progress.Wait()

	if n := failed.Current(); n > 0 {
		return fmt.Errorf("Failed to copy %d objects", n)
	}
	if config.Manager == "" {
		logger.Infof("Found: %d, copied: %d, deleted: %d, failed: %d, transferred: %s",
			bar.Current(), copied.Current(), deleted.Current(), failed.Current(), formatSize(copiedBytes.Current()))
	} else {
		sendStats(config.Manager)
	}
	return nil
}
