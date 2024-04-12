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
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
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
	handled               *utils.Bar
	pending               *utils.Bar
	copied, copiedBytes   *utils.Bar
	checked, checkedBytes *utils.Bar
	skipped, skippedBytes *utils.Bar
	deleted, failed       *utils.Bar
	concurrent            chan int
	limiter               *ratelimit.Bucket
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
func ListAll(store object.ObjectStorage, prefix, start, end string, followLink bool) (<-chan object.Object, error) {
	startTime := time.Now()
	logger.Debugf("Iterating objects from %s with prefix %s start %q", store, prefix, start)

	out := make(chan object.Object, maxResults*10)

	// As the result of object storage's List method doesn't include the marker key,
	// we try List the marker key separately.
	if start != "" && strings.HasPrefix(start, prefix) {
		if obj, err := store.Head(start); err == nil {
			logger.Debugf("Found start key: %s from %s in %s", start, store, time.Since(startTime))
			out <- obj
		}
	}

	if ch, err := store.ListAll(prefix, start, followLink); err == nil {
		go func() {
			for obj := range ch {
				if obj != nil && end != "" && obj.Key() > end {
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
	objs, err := store.List(prefix, marker, "", maxResults, followLink)
	if err == utils.ENOTSUP {
		return object.ListAllWithDelimiter(store, prefix, start, end, followLink)
	}
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
					break END
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
			objs, err = store.List(prefix, marker, "", maxResults, followLink)
			count := 0
			for err != nil && count < 3 {
				logger.Warnf("Fail to list: %s, retry again", err.Error())
				// slow down
				time.Sleep(time.Millisecond * 100)
				objs, err = store.List(prefix, marker, "", maxResults, followLink)
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
		logger.Debugf("Try %d failed: %s", i+1, err)
		time.Sleep(time.Second * time.Duration(i*i))
	}
	return
}

func deleteObj(storage object.ObjectStorage, key string, dry bool) {
	if dry {
		logger.Debugf("Will delete %s from %s", key, storage)
		deleted.Increment()
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

func copyPerms(dst object.ObjectStorage, obj object.Object, config *Config) {
	start := time.Now()
	key := obj.Key()
	fi := obj.(object.File)
	// chmod needs to be executed after chown, because chown will change setuid setgid to be invalid.
	if err := dst.(object.FileSystem).Chown(key, fi.Owner(), fi.Group()); err != nil {
		logger.Warnf("Chown %s to (%s,%s): %s", key, fi.Owner(), fi.Group(), err)
	}
	if !fi.IsSymlink() || !config.Links {
		if err := dst.(object.FileSystem).Chmod(key, fi.Mode()); err != nil {
			logger.Warnf("Chmod %s to %o: %s", key, fi.Mode(), err)
		}
	}
	logger.Debugf("Copied permissions (%s:%s:%s) for %s in %s", fi.Owner(), fi.Group(), fi.Mode(), key, time.Since(start))
}

func doCheckSum(src, dst object.ObjectStorage, key string, obj object.Object, config *Config, equal *bool) error {
	if obj.IsSymlink() && config.Links && (config.CheckAll || config.CheckNew) {
		var srcLink, dstLink string
		var err error
		if s, ok := src.(object.SupportSymlink); ok {
			if srcLink, err = s.Readlink(key); err != nil {
				return err
			}
		}
		if s, ok := dst.(object.SupportSymlink); ok {
			if dstLink, err = s.Readlink(key); err != nil {
				return err
			}
		}
		*equal = srcLink == dstLink && srcLink != "" && dstLink != ""
		return nil
	}
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
	if obj.Size() < maxBlock {
		err = checkPart(0, obj.Size())
	} else {
		n := int((obj.Size()-1)/defaultPartSize) + 1
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			go func(num int) {
				sz := int64(defaultPartSize)
				if num == n-1 {
					sz = obj.Size() - int64(num)*defaultPartSize
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

func checkSum(src, dst object.ObjectStorage, key string, obj object.Object, config *Config) (bool, error) {
	start := time.Now()
	var equal bool
	err := try(3, func() error { return doCheckSum(src, dst, key, obj, config, &equal) })
	if err == nil {
		checked.Increment()
		checkedBytes.IncrInt64(obj.Size())
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

var fastStreamRead = map[string]struct{}{"file": {}, "hdfs": {}, "jfs": {}, "gluster": {}}
var streamWrite = map[string]struct{}{"file": {}, "hdfs": {}, "sftp": {}, "gs": {}, "wasb": {}, "ceph": {}, "swift": {}, "webdav": {}, "upyun": {}, "jfs": {}, "gluster": {}}
var readInMem = map[string]struct{}{"mem": {}, "etcd": {}, "redis": {}, "tikv": {}, "mysql": {}, "postgres": {}, "sqlite3": {}}

func inMap(obj object.ObjectStorage, m map[string]struct{}) bool {
	_, ok := m[strings.Split(obj.String(), "://")[0]]
	return ok
}

func doCopySingle(src, dst object.ObjectStorage, key string, size int64) error {
	if size > maxBlock && !inMap(dst, readInMem) && !inMap(src, fastStreamRead) {
		var err error
		var in io.Reader
		downer := newParallelDownloader(src, key, size, 10<<20, concurrent)
		defer downer.Close()
		if inMap(dst, streamWrite) {
			in = downer
		} else {
			var f *os.File
			// download the object into disk
			if f, err = os.CreateTemp("", "rep"); err != nil {
				logger.Warnf("create temp file: %s", err)
				return doCopySingle0(src, dst, key, size)
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
	return doCopySingle0(src, dst, key, size)
}

func doCopySingle0(src, dst object.ObjectStorage, key string, size int64) error {
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
		if object.IsFileSystem(src) {
			// for check permissions
			r, err := src.Get(key, 0, -1)
			if err != nil {
				return err
			}
			_ = r.Close()
		}
		in = io.NopCloser(bytes.NewReader(nil))
	} else {
		in, err = src.Get(key, 0, size)
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
	return dst.Put(key, &withProgress{in})
}

type withProgress struct {
	r io.Reader
}

func (w *withProgress) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	copiedBytes.IncrInt64(int64(n))
	return n, err
}

func doUploadPart(src, dst object.ObjectStorage, srckey string, off, size int64, key, uploadID string, num int) (*object.Part, error) {
	if limiter != nil {
		limiter.Wait(size)
	}
	start := time.Now()
	sz := size
	p := chunk.NewOffPage(int(size))
	defer p.Release()
	data := p.Data
	var part *object.Part
	err := try(3, func() error {
		in, err := src.Get(srckey, off, sz)
		if err != nil {
			return err
		}
		defer in.Close()
		if _, err = io.ReadFull(in, data); err != nil {
			return err
		}
		// PartNumber starts from 1
		part, err = dst.UploadPart(key, uploadID, num+1, data)
		return err
	})
	if err != nil {
		logger.Warnf("Failed to copy data of %s part %d: %s", key, num, err)
		return nil, fmt.Errorf("part %d: %s", num, err)
	}
	logger.Debugf("Copied data of %s part %d in %s", key, num, time.Since(start))
	copiedBytes.IncrInt64(sz)
	return part, nil
}

func choosePartSize(upload *object.MultipartUpload, size int64) int64 {
	partSize := int64(upload.MinPartSize)
	if partSize == 0 {
		partSize = defaultPartSize
	}
	if size > partSize*int64(upload.MaxCount) {
		partSize = size / int64(upload.MaxCount)
		partSize = ((partSize-1)>>20 + 1) << 20 // align to MB
	}
	return partSize
}

func doCopyRange(src, dst object.ObjectStorage, key string, off, size int64, upload *object.MultipartUpload, num int, abort chan struct{}) (*object.Part, error) {
	select {
	case <-abort:
		return nil, fmt.Errorf("aborted")
	case concurrent <- 1:
		defer func() {
			<-concurrent
		}()
	}

	limits := dst.Limits()
	if size <= 32<<20 || !limits.IsSupportUploadPartCopy {
		return doUploadPart(src, dst, key, off, size, key, upload.UploadID, num)
	}

	tmpkey := fmt.Sprintf("%s.part%d", key, num)
	var up *object.MultipartUpload
	var err error
	err = try(3, func() error {
		up, err = dst.CreateMultipartUpload(tmpkey)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("range(%d,%d): %s", off, size, err)
	}

	partSize := choosePartSize(up, size)
	n := int((size-1)/partSize) + 1
	logger.Debugf("Copying data of %s (range: %d,%d) as %d parts (size: %d): %s", key, off, size, n, partSize, up.UploadID)
	parts := make([]*object.Part, n)

	for i := 0; i < n; i++ {
		sz := partSize
		if i == n-1 {
			sz = size - int64(i)*partSize
		}
		select {
		case <-abort:
			dst.AbortUpload(tmpkey, up.UploadID)
			return nil, fmt.Errorf("aborted")
		default:
		}
		parts[i], err = doUploadPart(src, dst, key, off+int64(i)*partSize, sz, tmpkey, up.UploadID, i)
		if err != nil {
			dst.AbortUpload(tmpkey, up.UploadID)
			return nil, fmt.Errorf("range(%d,%d): %s", off, size, err)
		}
	}

	err = try(3, func() error { return dst.CompleteUpload(tmpkey, up.UploadID, parts) })
	if err != nil {
		dst.AbortUpload(tmpkey, up.UploadID)
		return nil, fmt.Errorf("multipart: %s", err)
	}
	var part *object.Part
	err = try(3, func() error {
		part, err = dst.UploadPartCopy(key, upload.UploadID, num+1, tmpkey, 0, size)
		return err
	})
	_ = dst.Delete(tmpkey)
	return part, err
}

func doCopyMultiple(src, dst object.ObjectStorage, key string, size int64, upload *object.MultipartUpload) error {
	limits := dst.Limits()
	if size > limits.MaxPartSize*int64(upload.MaxCount) {
		return fmt.Errorf("object size %d is too large to copy", size)
	}

	partSize := choosePartSize(upload, size)
	n := int((size-1)/partSize) + 1
	logger.Debugf("Copying data of %s as %d parts (size: %d): %s", key, n, partSize, upload.UploadID)
	abort := make(chan struct{})
	parts := make([]*object.Part, n)
	errs := make(chan error, n)
	var err error

	for i := 0; i < n; i++ {
		go func(num int) {
			sz := partSize
			if num == n-1 {
				sz = size - int64(num)*partSize
			}
			parts[num], err = doCopyRange(src, dst, key, int64(num)*partSize, sz, upload, num, abort)
			errs <- err
		}(i)
	}

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
		} else if err == utils.ENOTSUP {
			err = try(3, func() error { return doCopySingle(src, dst, key, size) })
		} else { // other error retry
			if err = try(2, func() error {
				upload, err = dst.CreateMultipartUpload(key)
				return err
			}); err == nil {
				err = doCopyMultiple(src, dst, key, size, upload)
			}
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
				logger.Debugf("Will copy permissions for %s", key)
			} else {
				copyPerms(dst, obj, config)
			}
			copied.Increment()
		case markChecksum:
			if config.Dry {
				logger.Debugf("Will compare checksum for %s", key)
				checked.Increment()
				break
			}
			obj = obj.(*withSize).Object
			if equal, err := checkSum(src, dst, key, obj, config); err != nil {
				failed.Increment()
				break
			} else if equal {
				if config.DeleteSrc {
					deleteObj(src, key, false)
				} else if config.Perms {
					if o, e := dst.Head(key); e == nil {
						if needCopyPerms(obj, o) {
							copyPerms(dst, obj, config)
							copied.Increment()
						} else {
							skipped.Increment()
							skippedBytes.IncrInt64(obj.Size())
						}
					} else {
						logger.Warnf("Failed to head object %s: %s", key, e)
						failed.Increment()
					}
				} else {
					skipped.Increment()
					skippedBytes.IncrInt64(obj.Size())
				}
				break
			}
			// checkSum not equal, copy the object
			fallthrough
		default:
			if config.Dry {
				logger.Debugf("Will copy %s (%d bytes)", obj.Key(), obj.Size())
				copied.Increment()
				copiedBytes.IncrInt64(obj.Size())
				break
			}
			var err error
			if config.Links && obj.IsSymlink() {
				if err = copyLink(src, dst, key); err != nil {
					logger.Errorf("copy link failed: %s", err)
				}
			} else {
				err = copyData(src, dst, key, obj.Size())
			}

			if err == nil && (config.CheckAll || config.CheckNew) {
				var equal bool
				if equal, err = checkSum(src, dst, key, obj, config); err == nil && !equal {
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
					copyPerms(dst, obj, config)
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

func startSingleProducer(tasks chan<- object.Object, src, dst object.ObjectStorage, prefix string, config *Config) error {
	start, end := config.Start, config.End
	logger.Debugf("maxResults: %d, defaultPartSize: %d, maxBlock: %d", maxResults, defaultPartSize, maxBlock)

	srckeys, err := ListAll(src, prefix, start, end, !config.Links)
	if err != nil {
		return fmt.Errorf("list %s: %s", src, err)
	}

	var dstkeys <-chan object.Object
	if config.ForceUpdate {
		t := make(chan object.Object)
		close(t)
		dstkeys = t
	} else {
		dstkeys, err = ListAll(dst, prefix, start, end, !config.Links)
		if err != nil {
			return fmt.Errorf("list %s: %s", dst, err)
		}
	}
	produce(tasks, srckeys, dstkeys, config)
	return nil
}

func produce(tasks chan<- object.Object, srckeys, dstkeys <-chan object.Object, config *Config) {
	if len(config.rules) > 0 {
		srckeys = filter(srckeys, config.rules, config)
		dstkeys = filter(dstkeys, config.rules, config)
	}
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
			if config.Existing {
				skipped.Increment()
				skippedBytes.IncrInt64(obj.Size())
				handled.Increment()
				continue
			}
			tasks <- obj
		} else { // obj.key == dstobj.key
			if config.IgnoreExisting {
				skipped.Increment()
				skippedBytes.IncrInt64(obj.Size())
				handled.Increment()
				dstobj = nil
				continue
			}
			if config.ForceUpdate ||
				(config.Update && obj.Mtime().Unix() > dstobj.Mtime().Unix()) ||
				(!config.Update && obj.Size() != dstobj.Size()) {
				tasks <- obj
			} else if config.Update && obj.Mtime().Unix() < dstobj.Mtime().Unix() {
				skipped.Increment()
				skippedBytes.IncrInt64(obj.Size())
				handled.Increment()
			} else if config.CheckAll { // two objects are likely the same
				tasks <- &withSize{obj, markChecksum}
			} else if config.DeleteSrc {
				tasks <- &withSize{obj, markDeleteSrc}
			} else if config.Perms && needCopyPerms(obj, dstobj) {
				tasks <- &withFSize{obj.(object.File), markCopyPerms}
			} else {
				skipped.Increment()
				skippedBytes.IncrInt64(obj.Size())
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

func parseRule(name, p string) rule {
	if runtime.GOOS == "windows" {
		p = strings.Replace(p, "\\", "/", -1)
	}
	return rule{pattern: p, include: name == "-include"}
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
			rules = append(rules, parseRule(a, args[i+1]))
		} else if strings.HasPrefix(a, "-include=") || strings.HasPrefix(a, "-exclude=") {
			if s := strings.Split(a, "="); len(s) == 2 && s[1] != "" {
				if _, err := path.Match(s[1], "xxxx"); err != nil {
					logger.Warnf("ignore invalid pattern: %s", a)
					continue
				}
				rules = append(rules, parseRule(s[0], s[1]))
			}
		}
	}
	return
}

func filter(keys <-chan object.Object, rules []rule, config *Config) <-chan object.Object {
	r := make(chan object.Object)
	go func() {
		for o := range keys {
			if o == nil {
				break
			}
			var ok bool
			if config.MatchFullPath {
				ok = matchFullPath(rules, o.Key())
			} else {
				ok = matchLeveledPath(rules, o.Key())
			}
			if ok {
				r <- o
			} else {
				logger.Debugf("exclude %s", o.Key())
			}
		}
		close(r)
	}()
	return r
}

func matchTwoStar(p string, s []string) bool {
	if len(s) == 0 {
		return p == "*"
	}
	idx := strings.Index(p, "**")
	if idx == -1 {
		ok, _ := path.Match(p, strings.Join(s, "/"))
		return ok
	}
	ok, _ := path.Match(p[:idx+1], s[0])
	if !ok {
		return false
	}
	for i := 0; i <= len(s); i++ {
		tp := p[idx+1:]
		if i == 0 {
			tp = p[:idx] + p[idx+1:]
		}
		if matchTwoStar(tp, s[i:]) {
			return true
		}
	}
	return false
}

func matchPrefix(p, s []string) bool {
	if len(p) == 0 || len(s) == 0 {
		return len(p) == len(s)
	}
	first := p[0]
	n := len(s)
	switch {
	case first == "***":
		return true
	case strings.Contains(first, "**"):
		for i := 1; i <= n; i++ {
			if matchTwoStar(first, s[:i]) && matchPrefix(p[1:], s[i:]) {
				return true
			}
		}
		return false
	default:
		ok, _ := path.Match(first, s[0])
		return ok && matchPrefix(p[1:], s[1:])
	}
}

func matchSuffix(p, s []string) bool {
	if len(p) == 0 {
		return true
	}
	last := p[len(p)-1]
	if len(s) == 0 {
		return len(p) == 1 && (last == "***" || last == "**")
	}
	prefix := p[:len(p)-1]
	n := len(s)
	switch {
	case last == "***":
		for i := 0; i < n; i++ {
			if matchSuffix(prefix, s[:i]) {
				return true
			}
		}
		return false
	case strings.Contains(last, "**"):
		for i := 0; i < n; i++ {
			if matchTwoStar(last, s[i:]) && matchSuffix(prefix, s[:i]) {
				return true
			}
		}
		return false
	default:
		ok, _ := path.Match(last, s[n-1])
		return ok && matchSuffix(prefix, s[:n-1])
	}
}

func matchFullPath(rules []rule, key string) bool {
	ps := strings.Split(key, "/")
	for _, rule := range rules {
		p := strings.Split(rule.pattern, "/")
		var ok bool
		if p[0] == "" {
			if ps[0] != "" {
				p = p[1:]
			}
			ok = matchPrefix(p, ps)
		} else {
			ok = matchSuffix(p, ps)
		}
		if ok {
			if rule.include {
				break // try next level
			} else {
				return false
			}
		}
	}
	return true
}

// Consistent with rsync behavior, the matching order is adjusted according to the order of the "include" and "exclude" options
func matchLeveledPath(rules []rule, key string) bool {
	parts := strings.Split(key, "/")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		for _, rule := range rules {
			ps := parts[:i+1]
			p := strings.Split(rule.pattern, "/")
			if i < len(parts)-1 && (p[len(p)-1] == "" || p[len(p)-1] == "***") {
				ps = append(append([]string{}, ps...), "") // don't overwrite parts
			}
			var ok bool
			if p[0] == "" {
				if ps[0] != "" {
					p = p[1:]
				}
				ok = matchPrefix(p, ps)
			} else {
				ok = matchSuffix(p, ps)
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

func listCommonPrefix(store object.ObjectStorage, prefix string, cp chan object.Object, followLink bool) (chan object.Object, error) {
	var total []object.Object
	var marker string
	for {
		objs, err := store.List(prefix, marker, "/", maxResults, followLink)
		if err != nil {
			return nil, err
		}
		if len(objs) == 0 {
			break
		}
		total = append(total, objs...)
		marker = objs[len(objs)-1].Key()
		if marker == "" {
			break
		}
	}
	srckeys := make(chan object.Object, 1000)
	go func() {
		defer close(srckeys)
		for _, o := range total {
			if o.IsDir() && o.Key() > prefix {
				if cp != nil {
					cp <- o
				}
			} else {
				srckeys <- o
			}
		}
	}()
	return srckeys, nil
}

func startProducer(tasks chan<- object.Object, src, dst object.ObjectStorage, prefix string, config *Config) error {
	if prefix == "" && config.Limit == 1 && len(config.rules) == 0 {
		// fast path for single key
		obj, err := src.Head(config.Start)
		if err == nil && (!obj.IsDir() || config.Dirs) {
			var srckeys = make(chan object.Object, 1)
			srckeys <- obj
			close(srckeys)
			var dstkeys = make(chan object.Object, 1)
			if dobj, err := dst.Head(config.Start); err == nil || os.IsNotExist(err) {
				if dobj != nil {
					dstkeys <- dobj
				}
				close(dstkeys)
				logger.Debugf("produce single key %s", config.Start)
				produce(tasks, srckeys, dstkeys, config)
				return nil
			} else {
				logger.Warnf("head %s from %s: %s", config.Start, dst, err)
			}
		} else if err != nil && !os.IsNotExist(err) {
			logger.Warnf("head %s from %s: %s", config.Start, src, err)
		}
	}
	if config.ListThreads <= 1 || strings.Count(prefix, "/") >= config.ListDepth {
		return startSingleProducer(tasks, src, dst, prefix, config)
	}

	commonPrefix := make(chan object.Object, 1000)
	done := make(chan bool)
	go func() {
		defer close(done)
		var mu sync.Mutex
		processing := make(map[string]bool)
		var wg sync.WaitGroup
		defer wg.Wait()
		for c := range commonPrefix {
			mu.Lock()
			if processing[c.Key()] {
				mu.Unlock()
				continue
			}
			processing[c.Key()] = true
			mu.Unlock()

			if len(config.rules) > 0 && !matchLeveledPath(config.rules, c.Key()) {
				logger.Infof("exclude prefix %s", c.Key())
				continue
			}
			if c.Key() < config.Start {
				logger.Infof("ignore prefix %s", c.Key())
				continue
			}
			if config.End != "" && c.Key() > config.End {
				logger.Infof("ignore prefix %s", c.Key())
				continue
			}
			select {
			case config.concurrentList <- 1:
				wg.Add(1)
				go func(prefix string) {
					defer wg.Done()
					err := startProducer(tasks, src, dst, prefix, config)
					if err != nil {
						logger.Fatalf("list prefix %s: %s", prefix, err)
					}
					<-config.concurrentList
				}(c.Key())
			default:
				err := startProducer(tasks, src, dst, c.Key(), config)
				if err != nil {
					logger.Fatalf("list prefix %s: %s", c.Key(), err)
				}
			}

		}
	}()

	srckeys, err := listCommonPrefix(src, prefix, commonPrefix, !config.Links)
	if err == utils.ENOTSUP {
		return startSingleProducer(tasks, src, dst, prefix, config)
	} else if err != nil {
		return fmt.Errorf("list %s with delimiter: %s", src, err)
	}
	var dcp chan object.Object
	if config.DeleteDst {
		dcp = commonPrefix // search common prefix in dst
	}
	var dstkeys <-chan object.Object
	if config.ForceUpdate {
		t := make(chan object.Object)
		close(t)
		dstkeys = t
	} else {
		dstkeys, err = listCommonPrefix(dst, prefix, dcp, !config.Links)
		if err == utils.ENOTSUP {
			return startSingleProducer(tasks, src, dst, prefix, config)
		} else if err != nil {
			return fmt.Errorf("list %s with delimiter: %s", dst, err)
		}
	}
	// sync returned objects
	produce(tasks, srckeys, dstkeys, config)
	// consume all the keys from dst
	for range dstkeys {
	}
	close(commonPrefix)

	<-done
	return nil
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

	if config.Inplace {
		object.PutInplace = true
	}

	var bufferSize = 10240
	if config.Manager != "" {
		bufferSize = 100
	}
	tasks := make(chan object.Object, bufferSize)
	wg := sync.WaitGroup{}
	concurrent = make(chan int, config.Threads)
	if config.BWLimit > 0 {
		bps := float64(config.BWLimit*1e6/8) * 0.85 // 15% overhead
		limiter = ratelimit.NewBucketWithRate(bps, int64(bps)*3)
	}

	progress := utils.NewProgress(config.Verbose || config.Quiet || config.Manager != "")
	handled = progress.AddCountBar("Scanned objects", 0)
	skipped = progress.AddCountSpinner("Skipped objects")
	skippedBytes = progress.AddByteSpinner("Skipped bytes")
	pending = progress.AddCountSpinner("Pending objects")
	copied = progress.AddCountSpinner("Copied objects")
	copiedBytes = progress.AddByteSpinner("Copied bytes")
	if config.CheckAll || config.CheckNew {
		checked = progress.AddCountSpinner("Checked objects")
		checkedBytes = progress.AddByteSpinner("Checked bytes")
	}
	if config.DeleteSrc || config.DeleteDst {
		deleted = progress.AddCountSpinner("Deleted objects")
	}
	if !config.Dry {
		failed = progress.AddCountSpinner("Failed objects")
	}
	go func() {
		for {
			pending.SetCurrent(int64(len(tasks)))
			time.Sleep(time.Millisecond * 100)
		}
	}()

	initSyncMetrics(config)
	for i := 0; i < config.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(tasks, src, dst, config)
		}()
	}

	if len(config.Exclude) > 0 {
		config.rules = parseIncludeRules(os.Args)
	}

	if config.Manager == "" {
		if len(config.Workers) > 0 {
			addr, err := startManager(config, tasks)
			if err != nil {
				return err
			}
			launchWorker(addr, config, &wg)
		}
		logger.Infof("Syncing from %s to %s", src, dst)
		if config.Start != "" {
			logger.Infof("first key: %q", config.Start)
		}
		if config.End != "" {
			logger.Infof("last key: %q", config.End)
		}
		config.concurrentList = make(chan int, config.ListThreads)
		err := startProducer(tasks, src, dst, "", config)
		if err != nil {
			return err
		}
		close(tasks)
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
	pending.SetCurrent(0)
	total := handled.GetTotal()
	progress.Done()

	if config.Manager == "" {
		msg := fmt.Sprintf("Found: %d, skipped: %d (%s), copied: %d (%s)",
			total, skipped.Current(), formatSize(skippedBytes.Current()), copied.Current(), formatSize(copiedBytes.Current()))
		if checked != nil {
			msg += fmt.Sprintf(", checked: %d (%s)", checked.Current(), formatSize(checkedBytes.Current()))
		}
		if deleted != nil {
			msg += fmt.Sprintf(", deleted: %d", deleted.Current())
		}
		if failed != nil {
			msg += fmt.Sprintf(", failed: %d", failed.Current())
		}
		if total-handled.Current() > 0 {
			msg += fmt.Sprintf(", lost: %d", total-handled.Current())
		}
		logger.Info(msg)
	} else {
		sendStats(config.Manager)
		logger.Debugf("This worker process has already completed its task")
	}
	if failed != nil {
		if n := failed.Current(); n > 0 || total > handled.Current() {
			return fmt.Errorf("failed to handle %d objects", n+total-handled.Current())
		}
	}
	return nil
}

func initSyncMetrics(config *Config) {
	if config.Registerer != nil {
		config.Registerer.MustRegister(
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "scanned",
				Help: "Scanned objects",
			}, func() float64 {
				return float64(handled.Total())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "handled",
				Help: "Handled objects",
			}, func() float64 {
				return float64(handled.Current())
			}),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "pending",
				Help: "Pending objects",
			}, func() float64 {
				return float64(pending.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "copied",
				Help: "Copied objects",
			}, func() float64 {
				return float64(copied.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "copied_bytes",
				Help: "Copied bytes",
			}, func() float64 {
				return float64(copiedBytes.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "skipped",
				Help: "Skipped objects",
			}, func() float64 {
				return float64(skipped.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "skipped_bytes",
				Help: "Skipped bytes",
			}, func() float64 {
				return float64(skippedBytes.Current())
			}),
		)
		if failed != nil {
			config.Registerer.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "failed",
				Help: "Failed objects",
			}, func() float64 {
				return float64(failed.Current())
			}))
		}
		if deleted != nil {
			config.Registerer.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "deleted",
				Help: "Deleted objects",
			}, func() float64 {
				return float64(deleted.Current())
			}))
		}
		if checked != nil && checkedBytes != nil {
			config.Registerer.MustRegister(
				prometheus.NewCounterFunc(prometheus.CounterOpts{
					Name: "checked",
					Help: "Checked objects",
				}, func() float64 {
					return float64(checked.Current())
				}),
				prometheus.NewCounterFunc(prometheus.CounterOpts{
					Name: "checked_bytes",
					Help: "Checked bytes",
				}, func() float64 {
					return float64(checkedBytes.Current())
				}))
		}
	}
}
