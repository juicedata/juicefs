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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juju/ratelimit"
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
	handled                  *utils.Bar
	copied, copiedBytes      *utils.Bar
	checkedBytes             *utils.Bar
	deleted, skipped, failed *utils.Bar
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
	} else if !errors.Is(err, utils.ENOTSUP) {
		return nil, err
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
					logger.Errorf("The keys are out of order: marker %q, last %q current %q", marker, lastkey, key)
					out <- nil
					return
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
	if size > maxBlock && !strings.HasPrefix(src.String(), "file://") && !strings.HasPrefix(src.String(), "hdfs://") {
		var err error
		var in io.Reader
		downer := newParallelDownloader(src, key, size, 10<<20, concurrent)
		defer downer.Close()
		if strings.HasPrefix(dst.String(), "file://") || strings.HasPrefix(dst.String(), "hdfs://") {
			in = downer
		} else {
			var f *os.File
			// download the object into disk
			if f, err = ioutil.TempFile("", "rep"); err != nil {
				logger.Warnf("create temp file: %s", err)
				goto SINGLE
			}
			_ = os.Remove(f.Name()) // will be deleted after Close()
			defer f.Close()
			buf := bufPool.Get().(*[]byte)
			defer bufPool.Put(buf)
			if _, err = io.CopyBuffer(struct{ io.Writer }{f}, downer, *buf); err == nil {
				_, err = f.Seek(0, 0)
				in = f
			}
		}
		if err == nil {
			err = dst.Put(key, in)
		}
		if err != nil {
			if _, e := src.Head(key); os.IsNotExist(e) {
				logger.Debugf("Head src %s: %s", key, err)
				copied.IncrInt64(-1)
				err = nil
			}
		}
		return err
	}
SINGLE:
	if limiter != nil {
		limiter.Wait(size)
	}
	concurrent <- 1
	defer func() {
		<-concurrent
	}()
	var in io.ReadCloser
	var err error
	if size == 0 {
		in = io.NopCloser(bytes.NewReader(nil))
	} else {
		in, err = src.Get(key, 0, -1)
		if err != nil {
			if _, e := src.Head(key); os.IsNotExist(e) {
				logger.Debugf("Head src %s: %s", key, err)
				copied.IncrInt64(-1)
				err = nil
			}
			return err
		}
	}
	defer in.Close()

	if err = dst.Put(key, in); err == nil {
		copiedBytes.IncrInt64(size)
	}
	return err
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
	var err error
	if size < maxBlock {
		err = try(3, func() error { return doCopySingle(src, dst, key, size) })
	} else {
		var upload *object.MultipartUpload
		if upload, err = dst.CreateMultipartUpload(key); err == nil {
			err = doCopyMultiple(src, dst, key, size, upload)
		} else { // fallback
			err = try(3, func() error { return doCopySingle(src, dst, key, size) })
		}
	}
	if err == nil {
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
			var err error
			if config.Links && obj.IsSymlink() {
				if err = copyLink(src, dst, key); err == nil {
					copied.Increment()
					handled.Increment()
					break
				}
				logger.Errorf("copy link failed: %s", err)
			} else {
				err = copyData(src, dst, key, obj.Size())
			}

			if err == nil && (config.CheckAll || config.CheckNew) {
				var equal bool
				if equal, err = checkSum(src, dst, key, obj.Size()); err == nil && !equal {
					err = fmt.Errorf("checksums of copied object %s don't match", key)
				}
			}
			if err == nil {
				if mc, ok := dst.(object.MtimeChanger); ok {
					if err = mc.Chtimes(obj.Key(), obj.Mtime()); err != nil && !errors.Is(err, utils.ENOTSUP) {
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

func copyLink(src object.ObjectStorage, dst object.ObjectStorage, key string) error {
	if p, err := src.(object.SupportSymlink).Readlink(key); err != nil {
		return err
	} else {
		if err := dst.Delete(key); err != nil {
			logger.Debugf("Deleted %s from %s ", key, dst)
			return err
		}
		// TODO: use relative path based on option
		return dst.(object.SupportSymlink).Symlink(p, key)
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

func deleteFromDst(tasks chan<- object.Object, dstobj object.Object, config *Config) bool {
	if !config.Dirs && dstobj.IsDir() {
		logger.Debug("Ignore deleting dst directory ", dstobj.Key())
		return false
	}
	if config.Limit >= 0 {
		if config.Limit == 0 {
			return true
		}
		config.Limit--
	}
	tasks <- &withSize{dstobj, markDeleteDst}
	handled.IncrTotal(1)
	return false
}

func startProducer(tasks chan<- object.Object, src, dst object.ObjectStorage, config *Config) error {
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
		return fmt.Errorf("list %s: %s", src, err)
	}

	dstkeys, err := ListAll(dst, start, end)
	if err != nil {
		return fmt.Errorf("list %s: %s", dst, err)
	}
	if len(config.Exclude) > 0 {
		rules := parseIncludeRules(os.Args)
		if runtime.GOOS == "windows" && (strings.HasPrefix(src.String(), "file:") || strings.HasPrefix(dst.String(), "file:")) {
			for _, r := range rules {
				r.pattern = strings.Replace(r.pattern, "\\", "/", -1)
			}
		}
		if len(rules) > 0 {
			srckeys = filter(srckeys, rules)
			dstkeys = filter(dstkeys, rules)
		}
	}
	go produce(tasks, src, dst, srckeys, dstkeys, config)
	return nil
}

func produce(tasks chan<- object.Object, src, dst object.ObjectStorage, srckeys, dstkeys <-chan object.Object, config *Config) {
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
		if config.Limit >= 0 {
			if config.Limit == 0 {
				return
			}
			config.Limit--
		}
		handled.IncrTotal(1)

		if dstobj != nil && obj.Key() > dstobj.Key() {
			if config.DeleteDst {
				if deleteFromDst(tasks, dstobj, config) {
					return
				}
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
					if deleteFromDst(tasks, dstobj, config) {
						return
					}
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
			if deleteFromDst(tasks, dstobj, config) {
				return
			}
		}
		for dstobj = range dstkeys {
			if dstobj != nil {
				if deleteFromDst(tasks, dstobj, config) {
					return
				}
			}
		}
	}
}

type rule struct {
	pattern string
	include bool
}

func parseIncludeRules(args []string) (rules []rule) {
	l := len(args)
	for i, a := range args {
		if strings.HasPrefix(a, "--") {
			a = a[1:]
		}
		if l-1 > i && (a == "-include" || a == "-exclude") {
			if _, err := path.Match(args[i+1], "xxxx"); err != nil {
				logger.Warnf("ignore invalid pattern: %s %s", a, args[i+1])
				continue
			}
			rules = append(rules, rule{pattern: args[i+1], include: a == "-include"})
		} else if strings.HasPrefix(a, "-include=") || strings.HasPrefix(a, "-exclude=") {
			if s := strings.Split(a, "="); len(s) == 2 && s[1] != "" {
				if _, err := path.Match(s[1], "xxxx"); err != nil {
					logger.Warnf("ignore invalid pattern: %s", a)
					continue
				}
				rules = append(rules, rule{pattern: s[1], include: strings.HasPrefix(a, "-include=")})
			}
		}
	}
	return
}

func filter(keys <-chan object.Object, rules []rule) <-chan object.Object {
	r := make(chan object.Object)
	go func() {
		for o := range keys {
			if o == nil {
				break
			}
			if matchKey(rules, o.Key()) {
				r <- o
			} else {
				logger.Debugf("exclude %s", o.Key())
			}
		}
		close(r)
	}()
	return r
}

func suffixForPattern(path, pattern string) string {
	if strings.HasPrefix(pattern, "/") ||
		strings.HasSuffix(pattern, "/") && !strings.HasSuffix(path, "/") {
		return path
	}
	n := strings.Count(strings.Trim(pattern, "/"), "/")
	m := strings.Count(strings.Trim(path, "/"), "/")
	if n >= m {
		return path
	}
	parts := strings.Split(path, "/")
	n = len(strings.Split(pattern, "/"))
	return strings.Join(parts[len(parts)-n:], "/")
}

// Consistent with rsync behavior, the matching order is adjusted according to the order of the "include" and "exclude" options
func matchKey(rules []rule, key string) bool {
	parts := strings.Split(key, "/")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		prefix := strings.Join(parts[:i+1], "/")
		for _, rule := range rules {
			var s string
			if i < len(parts)-1 && strings.HasSuffix(rule.pattern, "/") {
				s = "/"
			}
			suffix := suffixForPattern(prefix+s, rule.pattern)
			ok, err := path.Match(rule.pattern, suffix)
			if err != nil {
				logger.Fatalf("match %s with %s: %v", rule.pattern, suffix, err)
			}
			if ok {
				if rule.include {
					break // try next level
				} else {
					return false
				}
			}
		}
	}
	return true
}

// Sync syncs all the keys between to object storage
func Sync(src, dst object.ObjectStorage, config *Config) error {
	if strings.HasPrefix(src.String(), "file://") && strings.HasPrefix(dst.String(), "file://") {
		major, minor := utils.GetKernelVersion()
		// copy_file_range() system call first appeared in Linux 4.5, and reworked in 5.3
		// Go requires kernel >= 5.3 to use copy_file_range(), see:
		// https://github.com/golang/go/blob/go1.17.11/src/internal/poll/copy_file_range_linux.go#L58-L66
		if major > 5 || (major == 5 && minor >= 3) {
			d1 := utils.GetDev(src.String()[7:]) // remove prefix "file://"
			d2 := utils.GetDev(dst.String()[7:])
			if d1 != -1 && d1 == d2 {
				object.TryCFR = true
			}
		}
	}

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

	progress := utils.NewProgress(config.Verbose || config.Quiet || config.Manager != "", true)
	handled = progress.AddCountBar("Scanned objects", 0)
	copied = progress.AddCountSpinner("Copied objects")
	copiedBytes = progress.AddByteSpinner("Copied objects")
	checkedBytes = progress.AddByteSpinner("Checked objects")
	deleted = progress.AddCountSpinner("Deleted objects")
	skipped = progress.AddCountSpinner("Skipped objects")
	failed = progress.AddCountSpinner("Failed objects")
	for i := 0; i < config.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(tasks, src, dst, config)
		}()
	}

	if config.Manager == "" {
		err := startProducer(tasks, src, dst, config)
		if err != nil {
			return err
		}
		if len(config.Workers) > 0 {
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
	progress.Done()

	if config.Manager == "" {
		logger.Infof("Found: %d, copied: %d (%s), checked: %s, deleted: %d, skipped: %d, failed: %d",
			handled.Current(), copied.Current(), formatSize(copiedBytes.Current()), formatSize(checkedBytes.Current()),
			deleted.Current(), skipped.Current(), failed.Current())
	} else {
		sendStats(config.Manager)
	}
	if n := failed.Current(); n > 0 {
		return fmt.Errorf("Failed to handle %d objects", n)
	}
	return nil
}
