/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package sync

import (
	"bytes"
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
	bufferSize      = 32 << 10
	maxBlock        = defaultPartSize * 2
	markDeleteSrc   = -1
	markDeleteDst   = -2
	markCopyPerms   = -3
	markChecksum    = -4
)

var (
	total                    int64
	handled                  *mpb.Bar
	copied, copiedBytes      *mpb.Bar
	checkedBytes             *mpb.Bar
	deleted, skipped, failed *mpb.Bar
	concurrent               chan int
	limiter                  *ratelimit.Bucket
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
	return fmt.Sprintf("%.2f %siB", v, units[z])
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
		buf := make([]byte, bufferSize)
		return &buf
	},
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

func deleteObj(storage object.ObjectStorage, key string, dry bool) {
	if dry {
		logger.Infof("Will delete %s from %s", key, storage)
		return
	}
	start := time.Now()
	if err := try(3, func() error { return storage.Delete(key) }); err == nil {
		deleted.Increment()
		logger.Debugf("Deleted %s from %s in %s", key, storage, time.Since(start))
	} else {
		failed.Increment()
		logger.Errorf("Failed to delete %s from %s in %s: %s", key, storage, time.Since(start), err)
	}
}

func needCopyPerms(o1, o2 object.Object) bool {
	f1 := o1.(object.File)
	f2 := o2.(object.File)
	return f2.Mode() != f1.Mode() || f2.Owner() != f1.Owner() || f2.Group() != f1.Group()
}

func copyPerms(dst object.ObjectStorage, obj object.Object) {
	start := time.Now()
	key := obj.Key()
	fi := obj.(object.File)
	if err := dst.(object.FileSystem).Chmod(key, fi.Mode()); err != nil {
		logger.Warnf("Chmod %s to %d: %s", key, fi.Mode(), err)
	}
	if err := dst.(object.FileSystem).Chown(key, fi.Owner(), fi.Group()); err != nil {
		logger.Warnf("Chown %s to (%s,%s): %s", key, fi.Owner(), fi.Group(), err)
	}
	logger.Debugf("Copied permissions (%s:%s:%s) for %s in %s", fi.Owner(), fi.Group(), fi.Mode(), key, time.Since(start))
}

func doCheckSum(src, dst object.ObjectStorage, key string, size int64, equal *bool) error {
	abort := make(chan struct{})
	checkPart := func(offset, length int64) error {
		if limiter != nil {
			limiter.Wait(length)
		}
		select {
		case <-abort:
			return fmt.Errorf("aborted")
		case concurrent <- 1:
			defer func() {
				<-concurrent
			}()
		}
		in, err := src.Get(key, offset, length)
		if err != nil {
			return fmt.Errorf("src get: %s", err)
		}
		defer in.Close()
		in2, err := dst.Get(key, offset, length)
		if err != nil {
			return fmt.Errorf("dest get: %s", err)
		}
		defer in2.Close()

		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)
		buf2 := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf2)
		for left := int(length); left > 0; left -= bufferSize {
			bs := bufferSize
			if left < bufferSize {
				bs = left
			}
			*buf = (*buf)[:bs]
			*buf2 = (*buf2)[:bs]
			if _, err = io.ReadFull(in, *buf); err != nil {
				return fmt.Errorf("src read: %s", err)
			}
			if _, err = io.ReadFull(in2, *buf2); err != nil {
				return fmt.Errorf("dest read: %s", err)
			}
			if !bytes.Equal(*buf, *buf2) {
				return fmt.Errorf("bytes not equal")
			}
		}
		return nil
	}

	var err error
	if size < maxBlock {
		err = checkPart(0, size)
	} else {
		n := int((size-1)/defaultPartSize) + 1
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			go func(num int) {
				sz := int64(defaultPartSize)
				if num == n-1 {
					sz = size - int64(num)*defaultPartSize
				}
				errs <- checkPart(int64(num)*defaultPartSize, sz)
			}(i)
		}
		for i := 0; i < n; i++ {
			if err = <-errs; err != nil {
				close(abort)
				break
			}
		}
	}

	if err != nil && err.Error() == "bytes not equal" {
		*equal = false
		err = nil
	} else {
		*equal = err == nil
	}
	return err
}

func checkSum(src, dst object.ObjectStorage, key string, size int64) (bool, error) {
	start := time.Now()
	var equal bool
	err := try(3, func() error { return doCheckSum(src, dst, key, size, &equal) })
	if err == nil {
		checkedBytes.IncrInt64(size)
		if equal {
			logger.Debugf("Checked %s OK (and equal) in %s,", key, time.Since(start))
		} else {
			logger.Warnf("Checked %s OK (but NOT equal) in %s,", key, time.Since(start))
		}
	} else {
		logger.Errorf("Failed to check %s in %s: %s", key, time.Since(start), err)
	}
	return equal, err
}

func doCopySingle(src, dst object.ObjectStorage, key string, size int64) error {
	if limiter != nil {
		limiter.Wait(size)
	}
	concurrent <- 1
	defer func() {
		<-concurrent
	}()
	in, err := src.Get(key, 0, -1)
	if err != nil {
		if _, e := src.Head(key); e != nil {
			logger.Debugf("Head src %s: %s", key, err)
			return nil
		}
		return err
	}
	defer in.Close()

	if size <= maxBlock ||
		strings.HasPrefix(src.String(), "file://") ||
		strings.HasPrefix(dst.String(), "file://") {
		return dst.Put(key, in)
	} else { // obj.Size > maxBlock, download the object into disk first
		f, err := ioutil.TempFile("", "rep")
		if err != nil {
			return err
		}
		_ = os.Remove(f.Name()) // will be deleted after Close()
		defer f.Close()

		if _, err = io.Copy(f, in); err != nil {
			return err
		}
		// upload
		if _, err = f.Seek(0, 0); err != nil {
			return err
		}
		return dst.Put(key, f)
	}
}

func doCopyMultiple(src, dst object.ObjectStorage, key string, size int64, upload *object.MultipartUpload) error {
	partSize := int64(upload.MinPartSize)
	if partSize == 0 {
		partSize = defaultPartSize
	}
	if size > partSize*int64(upload.MaxCount) {
		partSize = size / int64(upload.MaxCount)
		partSize = ((partSize-1)>>20 + 1) << 20 // align to MB
	}
	n := int((size-1)/partSize) + 1
	logger.Debugf("Copying data of %s as %d parts (size: %d): %s", key, n, partSize, upload.UploadID)
	abort := make(chan struct{})
	parts := make([]*object.Part, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(num int) {
			sz := partSize
			if num == n-1 {
				sz = size - int64(num)*partSize
			}
			if limiter != nil {
				limiter.Wait(sz)
			}
			select {
			case <-abort:
				errs <- fmt.Errorf("aborted")
				return
			case concurrent <- 1:
				defer func() {
					<-concurrent
				}()
			}

			data := make([]byte, sz)
			if err := try(3, func() error {
				in, err := src.Get(key, int64(num)*partSize, sz)
				if err != nil {
					return err
				}
				defer in.Close()
				if _, err = io.ReadFull(in, data); err != nil {
					return err
				}
				// PartNumber starts from 1
				parts[num], err = dst.UploadPart(key, upload.UploadID, num+1, data)
				return err
			}); err == nil {
				errs <- nil
				copiedBytes.IncrInt64(sz)
				logger.Debugf("Copied data of %s part %d", key, num)
			} else {
				errs <- fmt.Errorf("part %d: %s", num, err)
				logger.Warnf("Failed to copy data of %s part %d: %s", key, num, err)
			}
		}(i)
	}

	var err error
	for i := 0; i < n; i++ {
		if err = <-errs; err != nil {
			close(abort)
			break
		}
	}
	if err == nil {
		err = try(3, func() error { return dst.CompleteUpload(key, upload.UploadID, parts) })
	}
	if err != nil {
		dst.AbortUpload(key, upload.UploadID)
		return fmt.Errorf("multipart: %s", err)
	}
	return nil
}

func copyData(src, dst object.ObjectStorage, key string, size int64) error {
	start := time.Now()
	var multiple bool
	var err error
	if size < maxBlock {
		err = try(3, func() error { return doCopySingle(src, dst, key, size) })
	} else {
		var upload *object.MultipartUpload
		if upload, err = dst.CreateMultipartUpload(key); err == nil {
			multiple = true
			err = doCopyMultiple(src, dst, key, size, upload)
		} else { // fallback
			err = try(3, func() error { return doCopySingle(src, dst, key, size) })
		}
	}
	if err == nil {
		if !multiple {
			copiedBytes.IncrInt64(size)
		}
		logger.Debugf("Copied data of %s (%d bytes) in %s", key, size, time.Since(start))
	} else {
		logger.Errorf("Failed to copy data of %s in %s: %s", key, time.Since(start), err)
	}
	return err
}

func worker(tasks <-chan object.Object, src, dst object.ObjectStorage, config *Config) {
	for obj := range tasks {
		key := obj.Key()
		switch obj.Size() {
		case markDeleteSrc:
			deleteObj(src, key, config.Dry)
		case markDeleteDst:
			deleteObj(dst, key, config.Dry)
		case markCopyPerms:
			if config.Dry {
				logger.Infof("Will copy permissions for %s", key)
				break
			}
			copyPerms(dst, obj)
			copied.Increment()
		case markChecksum:
			if config.Dry {
				logger.Infof("Will compare checksum for %s", key)
				break
			}
			obj = obj.(*withSize).Object
			if equal, err := checkSum(src, dst, key, obj.Size()); err != nil {
				failed.Increment()
				break
			} else if equal {
				if config.DeleteSrc {
					deleteObj(src, key, false)
				} else if config.Perms {
					if o, e := dst.Head(key); e == nil {
						if needCopyPerms(obj, o) {
							copyPerms(dst, obj)
							copied.Increment()
						} else {
							skipped.Increment()
						}
					} else {
						logger.Warnf("Failed to head object %s: %s", key, e)
						failed.Increment()
					}
				} else {
					skipped.Increment()
				}
				break
			}
			// checkSum not equal, copy the object
			fallthrough
		default:
			if config.Dry {
				logger.Infof("Will copy %s (%d bytes)", obj.Key(), obj.Size())
				break
			}
			err := copyData(src, dst, key, obj.Size())
			if err == nil && (config.CheckAll || config.CheckNew) {
				var equal bool
				if equal, err = checkSum(src, dst, key, obj.Size()); err == nil && !equal {
					err = fmt.Errorf("checksums of copied object %s don't match", key)
				}
			}
			if err == nil {
				if mc, ok := dst.(object.MtimeChanger); ok {
					if err = mc.Chtimes(obj.Key(), obj.Mtime()); err != nil {
						logger.Warnf("Update mtime of %s: %s", key, err)
					}
				}
				if config.Perms {
					copyPerms(dst, obj)
				}
				copied.Increment()
			} else {
				failed.Increment()
				logger.Errorf("Failed to copy object %s: %s", key, err)
			}
		}
		handled.Increment()
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
	handled.SetTotal(total, false)
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
		handled.SetTotal(total, false)

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

		// FIXME: there is a race when source is modified during coping
		if dstobj == nil || obj.Key() < dstobj.Key() {
			tasks <- obj
		} else { // obj.key == dstobj.key
			if config.ForceUpdate ||
				(config.Update && obj.Mtime().Unix() > dstobj.Mtime().Unix()) ||
				(!config.Update && obj.Size() != dstobj.Size()) {
				tasks <- obj
			} else if config.Update && obj.Mtime().Unix() < dstobj.Mtime().Unix() {
				skipped.Increment()
				handled.Increment()
			} else if config.CheckAll { // two objects are likely the same
				tasks <- &withSize{obj, markChecksum}
			} else if config.DeleteSrc {
				tasks <- &withSize{obj, markDeleteSrc}
			} else if config.Perms && needCopyPerms(obj, dstobj) {
				tasks <- &withFSize{obj.(object.File), markCopyPerms}
			} else {
				skipped.Increment()
				handled.Increment()
			}
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
	progress, handled = utils.NewDynProgressBar("scanning objects: ", config.Verbose || config.Quiet || config.Manager != "")
	addBytes := func(name string) *mpb.Bar {
		return progress.Add(0,
			utils.NewSpinner(),
			mpb.PrependDecorators(
				decor.Name(name+" bytes: ", decor.WCSyncWidth),
				decor.CurrentKibiByte("% .2f", decor.WCSyncWidthR),
				// FIXME: maybe use EWMA speed
				decor.AverageSpeed(decor.UnitKiB, "   % .2f", decor.WCSyncWidthR),
			),
			mpb.BarFillerClearOnComplete(),
		)
	}
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
	copiedBytes, checkedBytes = addBytes("copied"), addBytes("checked")
	deleted, skipped, failed = addSpinner("deleted"), addSpinner("skipped"), addSpinner("failed")

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
	for _, b := range []*mpb.Bar{handled, copied, copiedBytes, checkedBytes, deleted, skipped, failed} {
		b.SetTotal(0, true)
	}
	progress.Wait()

	if config.Manager == "" {
		logger.Infof("Found: %d, copied: %d (%s), checked: %s, deleted: %d, skipped: %d, failed: %d",
			total, copied.Current(), formatSize(copiedBytes.Current()), formatSize(checkedBytes.Current()),
			deleted.Current(), skipped.Current(), failed.Current())
	} else {
		sendStats(config.Manager)
	}
	if n := failed.Current(); n > 0 {
		return fmt.Errorf("Failed to handle %d objects", n)
	}
	if handled.Current() != total {
		return fmt.Errorf("Number of handled objects %d != expected %d", handled.Current(), total)
	}
	return nil
}
