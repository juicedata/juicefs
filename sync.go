package main

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"osync/object"
	"sync"
	"sync/atomic"
)

// The max number of key per listing request
const MaxResults = 10240
const ReplicateThreads = 50
const maxBlock = 10 << 20

var copied uint64

// Iterate on all the keys that starts at marker from object storage.
func Iterate(store object.ObjectStorage, marker string) (<-chan *object.Object, error) {
	objs, err := store.List("", marker, MaxResults)
	if err != nil {
		logger.Errorf("Can't list %s: %s", store, err.Error())
		return nil, err
	}
	// Sending the keys into two channel, one used as source, another used as destination
	out := make(chan *object.Object, MaxResults)
	go func() {
		lastkey := ""
		for len(objs) > 0 {
			for _, obj := range objs {
				key := obj.Key
				if key != "" && key <= lastkey {
					logger.Fatalf("The keys are out of order: %q >= %q", lastkey, key)
				}
				lastkey = key
				out <- obj
			}
			marker = lastkey
			objs, err = store.List("", marker, MaxResults)
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

func Duplicate(in <-chan *object.Object) (<-chan string, <-chan string) {
	out1 := make(chan string, MaxResults)
	out2 := make(chan string, MaxResults)
	go func() {
		for s := range in {
			if s != nil {
				out1 <- s.Key
				out2 <- s.Key
			} else {
				out1 <- ""
				out2 <- ""
			}
		}
		close(out1)
		close(out2)
	}()
	return out1, out2
}

// Sync replicate the key from secondary to primary
func replicate(src, dst object.ObjectStorage, key string) error {
	in, e := src.Get(key, 0, maxBlock)
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
	if len(data) < maxBlock {
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

// Replicate a key from one object storage to another
func rep(src, dst object.ObjectStorage, key string) error {
	logger.Debugf("replicating %s", key)
	if err := replicate(src, dst, key); err != nil {
		logger.Warningf("Failed to replicate %s from %s to %s: %s", key, src, dst, err.Error())
		return err
	}
	atomic.AddUint64(&copied, 1)
	return nil
}

// Sync comparing all the ordered keys from two object storage, replicate the missed ones.
func Sync(src, dst object.ObjectStorage, srckeys, dstkeys <-chan string, waiter *sync.WaitGroup) {
	defer waiter.Done()
	todo := make(chan string, 1024)
	wg := sync.WaitGroup{}
	for i := 0; i < ReplicateThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				key, ok := <-todo
				if !ok {
					break
				}
				if rep(src, dst, key) != nil {
					if err := rep(src, dst, key); err != nil {
						logger.Infof("Failed to replicate %s from %s to %s", key, src, dst)
					}
				}
			}
		}()
	}

	dstkey := ""
OUT:
	for key := range srckeys {
		if key == "" {
			logger.Errorf("Listing failed, stop replicating, waiting for pending ones")
			break
		}
		for key > dstkey {
			var ok bool
			dstkey, ok = <-dstkeys
			if !ok {
				// the maximum key for JuiceFS
				dstkey = "zzzzzzzz"
			} else if dstkey == "" {
				// Listing failed, stop
				logger.Errorf("Listing failed, stop replicating, waiting for pending ones")
				break OUT
			}
		}
		if key < dstkey {
			todo <- key
		}
	}
	close(todo)
	// consume all the keys to unblock the producer
	for dstkey = range dstkeys {
	}
	wg.Wait()
}

// SyncAll syncs all the keys between to object storage
func SyncAll(a, b object.ObjectStorage, marker string) error {
	logger.Infof("syncing between %s and %s (starting from %q)", a, b, marker)
	cha, err := Iterate(a, marker)
	if err != nil {
		return err
	}
	srca, dsta := Duplicate(cha)
	chb, err := Iterate(b, marker)
	if err != nil {
		return err
	}
	srcb, dstb := Duplicate(chb)

	var wg sync.WaitGroup
	wg.Add(2)
	go Sync(a, b, srca, dstb, &wg)
	go Sync(b, a, srcb, dsta, &wg)
	wg.Wait()
	logger.Infof("all synchronized (copied %d blocks)", copied)
	return nil
}
