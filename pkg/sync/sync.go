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
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/mattn/go-isatty"
)

// The max number of key per listing request
const (
	maxResults      = 1000
	defaultPartSize = 5 << 20
	maxBlock        = defaultPartSize * 2
	markDelete      = -1
	markCopyPerms   = -2
)

var (
	found       int64
	todo        int64
	copied      int64
	copiedBytes int64
	failed      int64
	deleted     int64
	concurrent  chan int
	limiter     *ratelimit.Bucket
)

var logger = utils.GetLogger("juicefs")

// human readable bytes size
func formatSize(bytes uint64) string {
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
	if strings.HasPrefix(src.String(), "file://") || strings.HasPrefix(dst.String(), "file://") {
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
	firstBlock := -1
	if obj.Size() > maxBlock {
		firstBlock = maxBlock
	}
	in, e := src.Get(key, 0, int64(firstBlock))
	if e != nil {
		if _, err := src.Head(key); err != nil {
			return nil
		}
		return e
	}
	data, err := ioutil.ReadAll(in)
	in.Close()
	if err != nil {
		return err
	}
	if firstBlock == -1 {
		return dst.Put(key, bytes.NewReader(data))
	}

	// download the object into disk first
	f, err := ioutil.TempFile("", "rep")
	if err != nil {
		return err
	}
	os.Remove(f.Name()) // will be deleted after Close()
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if in, e = src.Get(key, int64(len(data)), -1); e != nil {
		return e
	}
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, e = io.CopyBuffer(f, in, *buf)
	in.Close()
	if e != nil {
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

func worker(todo chan object.Object, src, dst object.ObjectStorage, config *Config) {
	for {
		obj, ok := <-todo
		if !ok {
			break
		}
		start := time.Now()
		var err error
		if obj.Size() == markDelete {
			if config.DeleteSrc {
				if config.Dry {
					logger.Debugf("Will delete %s from %s", obj.Key(), src)
					continue
				}
				if err = try(3, func() error {
					return src.Delete(obj.Key())
				}); err == nil {
					logger.Debugf("Deleted %s from %s", obj.Key(), src)
					atomic.AddInt64(&deleted, 1)
				} else {
					logger.Errorf("Failed to delete %s from %s: %s", obj.Key(), src, err.Error())
					atomic.AddInt64(&failed, 1)
				}
			}
			if config.DeleteDst {
				if config.Dry {
					logger.Debugf("Will delete %s from %s", obj.Key(), dst)
					continue
				}
				if err = try(3, func() error {
					return dst.Delete(obj.Key())
				}); err == nil {
					logger.Debugf("Deleted %s from %s", obj.Key(), dst)
					atomic.AddInt64(&deleted, 1)
				} else {
					logger.Errorf("Failed to delete %s from %s: %s", obj.Key(), dst, err.Error())
					atomic.AddInt64(&failed, 1)
				}
			}
			continue
		}

		if config.Dry {
			logger.Debugf("Will copy %s (%d bytes)", obj.Key(), obj.Size())
			continue
		}
		if config.Perms && obj.Size() == markCopyPerms {
			fi := obj.(object.File)
			if err := dst.(object.FileSystem).Chmod(obj.Key(), fi.Mode()); err != nil {
				logger.Warnf("Chmod %s to %d: %s", obj.Key(), fi.Mode(), err)
			}
			if err := dst.(object.FileSystem).Chown(obj.Key(), fi.Owner(), fi.Group()); err != nil {
				logger.Warnf("Chown %s to (%s,%s): %s", obj.Key(), fi.Owner(), fi.Group(), err)
			}
			atomic.AddInt64(&copied, 1)
			logger.Debugf("Copied permissions (%s:%s:%s) for %s in %s", fi.Owner(), fi.Group(), fi.Mode(), obj.Key(), time.Since(start))
			continue
		}
		err = copyInParallel(src, dst, obj)
		if err != nil {
			atomic.AddInt64(&failed, 1)
			logger.Errorf("Failed to copy %s: %s", obj.Key(), err.Error())
		} else {
			if mc, ok := dst.(object.MtimeChanger); ok {
				err := mc.Chtimes(obj.Key(), obj.Mtime())
				if err != nil {
					logger.Warnf("Update mtime of %s: %s", obj.Key(), err)
				}
			}
			if config.Perms {
				fi := obj.(object.File)
				if err := dst.(object.FileSystem).Chmod(obj.Key(), fi.Mode()); err != nil {
					logger.Warnf("Chmod %s to %o: %s", obj.Key(), fi.Mode(), err)
				}
				if err := dst.(object.FileSystem).Chown(obj.Key(), fi.Owner(), fi.Group()); err != nil {
					logger.Warnf("Chown %s to (%s,%s): %s", obj.Key(), fi.Owner(), fi.Group(), err)
				}
			}
			atomic.AddInt64(&copied, 1)
			atomic.AddInt64(&copiedBytes, int64(obj.Size()))
			logger.Debugf("Copied %s (%d bytes) in %s", obj.Key(), obj.Size(), time.Since(start))
		}
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

func deleteFromDst(tasks chan object.Object, dstobj object.Object) {
	tasks <- &withSize{dstobj, markDelete}
	atomic.AddInt64(&found, 1)
	atomic.AddInt64(&todo, 1)
}

func producer(tasks chan object.Object, src, dst object.ObjectStorage, config *Config) {
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

	var dstobj object.Object
	hasMore := true
OUT:
	for obj := range srckeys {
		if obj == nil {
			logger.Errorf("Listing failed, stop syncing, waiting for pending ones")
			hasMore = false
			break
		}
		if !config.Dirs && obj.IsDir() {
			// ignore directories
			logger.Debug("Ignore directory ", obj.Key())
			continue
		}
		atomic.AddInt64(&found, 1)
		for hasMore && (dstobj == nil || obj.Key() > dstobj.Key()) {
			var ok bool
			if config.DeleteDst && dstobj != nil && dstobj.Key() < obj.Key() {
				if !config.Dirs && dstobj.IsDir() {
					// ignore
					logger.Debug("Ignore deleting dst directory ", dstobj.Key())
				} else {
					deleteFromDst(tasks, dstobj)
				}
			}
			dstobj, ok = <-dstkeys
			if !ok {
				hasMore = false
			} else if dstobj == nil {
				// Listing failed, stop
				logger.Errorf("Listing failed, stop syncing, waiting for pending ones")
				hasMore = false
				break OUT
			}
		}
		// FIXME: there is a race when source is modified during coping
		if !hasMore || obj.Key() < dstobj.Key() ||
			obj.Key() == dstobj.Key() && (config.ForceUpdate || obj.Size() != dstobj.Size() ||
				config.Update && obj.Mtime().After(dstobj.Mtime())) {
			tasks <- obj
			atomic.AddInt64(&todo, 1)
		} else if config.DeleteSrc && dstobj != nil && obj.Key() == dstobj.Key() && obj.Size() == dstobj.Size() {
			tasks <- &withSize{obj, markDelete}
			atomic.AddInt64(&todo, 1)
		} else if config.Perms {
			f1 := obj.(object.File)
			f2 := dstobj.(object.File)
			if f2.Mode() != f1.Mode() || f2.Owner() != f1.Owner() || f2.Group() != f1.Group() {
				tasks <- &withFSize{f1, markCopyPerms}
				atomic.AddInt64(&todo, 1)
			}
		}
		if dstobj != nil && dstobj.Key() == obj.Key() {
			dstobj = nil
		}
	}
	if config.DeleteDst && hasMore {
		if dstobj != nil {
			deleteFromDst(tasks, dstobj)
		}
		for obj := range dstkeys {
			if obj != nil {
				deleteFromDst(tasks, obj)
			}
		}
	}
	close(tasks)
}

func showProgress() {
	var lastDone, lastBytes []int64
	var lastTime []time.Time
	for {
		if found == 0 {
			time.Sleep(time.Millisecond * 10)
			continue
		}
		same := atomic.LoadInt64(&found) - atomic.LoadInt64(&todo)
		var width int64 = 45
		a := width * same / found
		b := width * (copied + deleted + failed) / found
		var bar [80]byte
		var i int64
		for i = 0; i < width; i++ {
			if i < a {
				bar[i] = '='
			} else if i < a+b {
				bar[i] = '+'
			} else {
				bar[i] = ' '
			}
		}
		now := time.Now()
		lastDone = append(lastDone, copied+deleted+failed)
		lastBytes = append(lastBytes, copiedBytes)
		lastTime = append(lastTime, now)
		for len(lastTime) > 18 { // 5 seconds
			lastDone = lastDone[1:]
			lastBytes = lastBytes[1:]
			lastTime = lastTime[1:]
		}
		if len(lastTime) > 1 {
			n := len(lastTime) - 1
			d := lastTime[n].Sub(lastTime[0]).Seconds()
			fps := float64(lastDone[n]-lastDone[0]) / d
			bw := float64(lastBytes[n]-lastBytes[0]) / d / 1024 / 1024
			fmt.Printf("[%s] % 8d % 2d%% % 4.1f/s % 4.1f MB/s \r", string(bar[:]), found, (found-todo+copied+deleted+failed)*100/found, fps, bw)
		}
		time.Sleep(time.Millisecond * 300)
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
	todo := make(chan object.Object, bufferSize)
	wg := sync.WaitGroup{}
	concurrent = make(chan int, config.Threads)
	if config.BWLimit > 0 {
		bps := float64(config.BWLimit*(1<<20)/8) * 0.85 // 15% overhead
		limiter = ratelimit.NewBucketWithRate(bps, int64(bps)*3)
	}
	for i := 0; i < config.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(todo, src, dst, config)
		}()
	}

	if config.Manager == "" {
		go producer(todo, src, dst, config)
		tty := isatty.IsTerminal(os.Stdout.Fd())
		if tty && !config.Verbose && !config.Quiet {
			go showProgress()
		}
		if config.Workers != nil {
			addr, err := startManager(todo)
			if err != nil {
				return err
			}
			launchWorker(addr, config, &wg)
		}
	} else {
		// start fetcher
		go fetchJobs(todo, config)
		go func() {
			for {
				sendStats(config.Manager)
				time.Sleep(time.Second)
			}
		}()
	}

	wg.Wait()

	if failed > 0 {
		return fmt.Errorf("Failed to copy %d objects", failed)
	}
	if config.Manager == "" {
		logger.Infof("Found: %d, copied: %d, deleted: %d, failed: %d, transferred: %s", found, copied, deleted, failed, formatSize(uint64(copiedBytes)))
	} else {
		sendStats(config.Manager)
	}
	return nil
}
