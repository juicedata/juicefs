// Copyright (C) 2018-present Juicedata Inc.

package sync

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicesync/config"
	"github.com/juicedata/juicesync/object"
	"github.com/juicedata/juicesync/utils"
	"github.com/mattn/go-isatty"
)

// The max number of key per listing request
const (
	maxResults      = 10240
	defaultPartSize = 5 << 20
	maxBlock        = defaultPartSize * 2
	markDelete      = -1
)

var (
	found       uint64
	missing     uint64
	copied      uint64
	copiedBytes uint64
	failed      uint64
	deleted     uint64
	concurrent  chan int
)

var logger = utils.GetLogger("juicesync")

// Iterate on all the keys that starts at marker from object storage.
func Iterate(store object.ObjectStorage, marker, end string) (<-chan *object.Object, error) {
	start := time.Now()
	logger.Debugf("Listing objects from %s marker %q", store, marker)
	objs, err := store.List("", marker, maxResults)
	if err != nil {
		logger.Errorf("Can't list %s: %s", store, err.Error())
		return nil, err
	}
	logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Now().Sub(start))
	out := make(chan *object.Object, maxResults)
	go func() {
		lastkey := ""
		first := true
	END:
		for len(objs) > 0 {
			for _, obj := range objs {
				key := obj.Key
				if !first && key <= lastkey {
					logger.Fatalf("The keys are out of order: marker %q, last %q current %q", marker, lastkey, key)
				}
				if end != "" && key >= end {
					break END
				}
				lastkey = key
				out <- obj
				first = false
			}
			marker = lastkey
			start = time.Now()
			logger.Debugf("Continue listing objects from %s marker %q", store, marker)
			objs, err = store.List("", marker, maxResults)
			for err != nil {
				logger.Warnf("Fail to list: %s, retry again", err.Error())
				// slow down
				time.Sleep(time.Millisecond * 100)
				objs, err = store.List("", marker, maxResults)
			}
			logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Now().Sub(start))
			if err != nil {
				// Telling that the listing has failed
				out <- nil
				logger.Errorf("Fail to list after %s: %s", marker, err.Error())
				break
			}
		}
		close(out)
	}()
	return out, nil
}

func copyObject(src, dst object.ObjectStorage, obj *object.Object) error {
	concurrent <- 1
	defer func() {
		<-concurrent
	}()
	key := obj.Key
	firstBlock := -1
	if obj.Size > maxBlock {
		firstBlock = maxBlock
	}
	in, e := src.Get(key, 0, int64(firstBlock))
	if e != nil {
		if src.Exists(key) != nil {
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
	_, e = io.Copy(f, in)
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

func copyInParallel(src, dst object.ObjectStorage, obj *object.Object) error {
	if obj.Size < maxBlock {
		return try(3, func() error {
			return copyObject(src, dst, obj)
		})
	}
	upload, err := dst.CreateMultipartUpload(obj.Key)
	if err != nil {
		return try(3, func() error {
			return copyObject(src, dst, obj)
		})
	}
	partSize := int64(upload.MinPartSize)
	if partSize == 0 {
		partSize = defaultPartSize
	}
	if obj.Size > partSize*int64(upload.MaxCount) {
		partSize = obj.Size / int64(upload.MaxCount)
		partSize = ((partSize-1)>>20 + 1) << 20 // align to MB
	}
	n := int((obj.Size-1)/partSize) + 1
	key := obj.Key
	logger.Debugf("Copying object %s as %d parts (size: %d): %s", key, n, partSize, upload.UploadID)
	parts := make([]*object.Part, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(num int) {
			sz := partSize
			if num == n-1 {
				sz = obj.Size - int64(num)*partSize
			}
			var err error
			concurrent <- 1
			defer func() {
				<-concurrent
			}()
			data := make([]byte, sz)
			err = try(3, func() error {
				r, err := src.Get(key, int64(num)*partSize, int64(sz))
				if err != nil {
					return nil
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

func doSync(src, dst object.ObjectStorage, srckeys, dstkeys <-chan *object.Object, config *config.Config) {
	todo := make(chan *object.Object, 10240)
	wg := sync.WaitGroup{}
	concurrent = make(chan int, config.Threads)
	for i := 0; i < config.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				obj, ok := <-todo
				if !ok {
					break
				}
				start := time.Now()
				var err error
				if obj.Size == markDelete {
					if config.DeleteSrc {
						if config.Dry {
							logger.Debugf("Will delete %s from %s", obj.Key, src)
							continue
						}
						if err = try(3, func() error {
							return src.Delete(obj.Key)
						}); err == nil {
							logger.Debugf("Deleted %s from %s", obj.Key, src)
							atomic.AddUint64(&deleted, 1)
						} else {
							logger.Errorf("Failed to delete %s from %s: %s", obj.Key, src, err.Error())
							atomic.AddUint64(&failed, 1)
						}
					}
					if config.DeleteDst {
						if config.Dry {
							logger.Debugf("Will delete %s from %s", obj.Key, dst)
							continue
						}
						if err = try(3, func() error {
							return dst.Delete(obj.Key)
						}); err == nil {
							logger.Debugf("Deleted %s from %s", obj.Key, dst)
							atomic.AddUint64(&deleted, 1)
						} else {
							logger.Errorf("Failed to delete %s from %s: %s", obj.Key, dst, err.Error())
							atomic.AddUint64(&failed, 1)
						}
					}
					continue
				}
				if config.Dry {
					logger.Debugf("Will copy %s (%d bytes)", obj.Key, obj.Size)
					continue
				}
				err = copyInParallel(src, dst, obj)
				if err != nil {
					atomic.AddUint64(&failed, 1)
					logger.Errorf("Failed to copy %s: %s", obj.Key, err.Error())
				} else {
					atomic.AddUint64(&copied, 1)
					atomic.AddUint64(&copiedBytes, uint64(obj.Size))
					logger.Debugf("Copied %s (%d bytes) in %s", obj.Key, obj.Size, time.Now().Sub(start))
				}
			}
		}()
	}

	var dstobj *object.Object
	hasMore := true
OUT:
	for obj := range srckeys {
		if obj == nil {
			logger.Errorf("Listing failed, stop syncing, waiting for pending ones")
			hasMore = false
			break
		}
		atomic.AddUint64(&found, 1)
		for hasMore && (dstobj == nil || obj.Key > dstobj.Key) {
			var ok bool
			if config.DeleteDst && dstobj != nil && dstobj.Key < obj.Key {
				dstobj.Size = markDelete
				todo <- dstobj
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
		if !hasMore || obj.Key < dstobj.Key || config.Update && obj.Key == dstobj.Key && obj.Mtime > dstobj.Mtime {
			todo <- obj
			atomic.AddUint64(&missing, 1)
		} else if config.DeleteSrc && dstobj != nil && obj.Key == dstobj.Key && obj.Size == dstobj.Size {
			obj.Size = markDelete
			todo <- obj
		}
		if dstobj != nil && dstobj.Key == obj.Key {
			dstobj = nil
		}
	}
	if config.DeleteDst && hasMore {
		if dstobj != nil {
			dstobj.Size = markDelete
			todo <- dstobj
		}
		for obj := range dstkeys {
			if obj != nil {
				obj.Size = markDelete
				todo <- obj
			}
		}
	}
	close(todo)
	wg.Wait()
}

func showProgress() {
	var lastCopied, lastBytes uint64
	var lastTime = time.Now()
	for {
		if found == 0 {
			time.Sleep(time.Millisecond * 10)
			continue
		}
		same := atomic.LoadUint64(&found) - atomic.LoadUint64(&missing)
		var width uint64 = 45
		a := width * same / found
		b := width * copied / found
		var bar [80]byte
		var i uint64
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
		fps := float64(copied-lastCopied) / now.Sub(lastTime).Seconds()
		bw := float64(copiedBytes-lastBytes) / now.Sub(lastTime).Seconds() / 1024 / 1024
		lastCopied = copied
		lastBytes = copiedBytes
		lastTime = now
		fmt.Printf("[%s] % 8d % 2d%% % 4.0f/s % 4.1f MB/s \r", string(bar[:]), found, (found-missing+copied)*100/found, fps, bw)
		time.Sleep(time.Millisecond * 300)
	}
}

// Sync syncs all the keys between to object storage
func Sync(src, dst object.ObjectStorage, config *config.Config) error {
	start, end := config.Start, config.End
	logger.Infof("Syncing from %s to %s", src, dst)
	if start != "" {
		logger.Infof("first key: %q", start)
	}
	if end != "" {
		logger.Infof("last key: %q", end)
	}
	logger.Debugf("maxResults: %d, defaultPartSize: %d, maxBlock: %d", maxResults, defaultPartSize, maxBlock)

	srcCh, err := Iterate(src, start, end)
	if err != nil {
		return err
	}

	dstCh, err := Iterate(dst, start, end)
	if err != nil {
		return err
	}

	tty := isatty.IsTerminal(os.Stdout.Fd())
	if tty && !config.Verbose && !config.Quiet {
		go showProgress()
	}
	doSync(src, dst, srcCh, dstCh, config)
	logger.Infof("Found: %d, copied: %d, deleted: %d, failed: %d", found, copied, deleted, failed)
	return nil
}
