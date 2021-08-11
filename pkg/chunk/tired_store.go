package chunk

import (
	"bytes"
	"fmt"
	"github.com/juicedata/juicefs/pkg/object"
	"os"
	"time"
)

type TieredConfig struct {
	Config
	TieredDiskDir        string
	TieredDiskMode       os.FileMode
	TieredDiskSize       int64
	TieredDiskFreeSpace  float32
	TieredDiskBufferSize int
}

type tieredWChunk struct {
	rChunk
	tiredStore  *tieredStore
	pages       [][]*Page
	uploaded    int
	errors      chan error
	uploadError error
	pendings    int
}

func (c *tieredWChunk) SetID(id uint64) {
	c.id = id
}

func (c *tieredWChunk) WriteAt(p []byte, off int64) (n int, err error) {
	if int(off)+len(p) > chunkSize {
		return 0, fmt.Errorf("write out of chunk boudary: %d > %d", int(off)+len(p), chunkSize)
	}
	if off < int64(c.uploaded) {
		return 0, fmt.Errorf("Cannot overwrite uploaded block: %d < %d", off, c.uploaded)
	}

	// Fill previous blocks with zeros
	if c.length < int(off) {
		zeros := make([]byte, int(off)-c.length)
		_, _ = c.WriteAt(zeros, int64(c.length))
	}

	for n < len(p) {
		indx := c.index(int(off) + n)
		boff := (int(off) + n) % c.store.conf.BlockSize
		var bs = pageSize
		if indx > 0 || bs > c.store.conf.BlockSize {
			bs = c.store.conf.BlockSize
		}
		bi := boff / bs
		bo := boff % bs
		var page *Page
		if bi < len(c.pages[indx]) {
			page = c.pages[indx][bi]
		} else {
			page = allocPage(bs)
			page.Data = page.Data[:0]
			c.pages[indx] = append(c.pages[indx], page)
		}
		left := len(p) - n
		if bo+left > bs {
			page.Data = page.Data[:bs]
		} else if len(page.Data) < bo+left {
			page.Data = page.Data[:bo+left]
		}
		n += copy(page.Data[bo:], p[n:])
	}
	if int(off)+n > c.length {
		c.length = int(off) + n
	}
	return n, nil
}

func (c *tieredWChunk) put(key string, p *Page) error {
	p.Acquire()
	return withTimeout(func() error {
		defer p.Release()
		st := time.Now()
		err := c.store.storage.Put(key, bytes.NewReader(p.Data))
		used := time.Since(st)
		logger.Debugf("PUT %s (%s, %.3fs)", key, err, used.Seconds())
		if used > SlowRequest {
			logger.Infof("slow request: PUT %v (%s, %.3fs)", key, err, used.Seconds())
		}
		return err
	}, c.store.conf.PutTimeout)
}

func (c *tieredWChunk) syncUpload(key string, block *Page) {
	blen := len(block.Data)
	bufSize := c.store.compressor.CompressBound(blen)
	var buf *Page
	if bufSize > blen {
		buf = NewOffPage(bufSize)
	} else {
		buf = block
		buf.Acquire()
	}
	n, err := c.store.compressor.Compress(buf.Data, block.Data)
	if err != nil {
		logger.Fatalf("compress chunk %v: %s", c.id, err)
		return
	}
	buf.Data = buf.Data[:n]
	block.Release()

	c.store.currentUpload <- true
	defer func() {
		buf.Release()
		<-c.store.currentUpload
	}()

	try := 0
	for try <= 10 && c.uploadError == nil {
		err = c.put(key, buf)
		if err == nil {
			c.errors <- nil
			return
		}
		try++
		logger.Warnf("upload %s: %s (try %d)", key, err, try)
		time.Sleep(time.Second * time.Duration(try*try))
	}
	c.errors <- fmt.Errorf("upload block %s: %s (after %d tries)", key, err, try)
}

func (c *tieredWChunk) upload(indx int) {
	blen := c.blockSize(indx)
	key := c.key(indx)
	pages := c.pages[indx]
	c.pages[indx] = nil
	c.pendings++

	go func() {
		var block *Page
		if len(pages) == 1 {
			block = pages[0]
		} else {
			block = NewOffPage(blen)
			var off int
			for _, b := range pages {
				off += copy(block.Data[off:], b.Data)
				freePage(b)
			}
			if off != blen {
				logger.Fatalf("block length does not match: %v != %v", off, blen)
			}
		}

		tieredDiskPath, err := c.tiredStore.tieredManager.save(key, block.Data)
		if err != nil {
			logger.Infof("write %s to disk: %s, spill to object store", tieredDiskPath, err)
			c.syncUpload(key, block)
		}
		c.errors <- nil
	}()
}

func (c *tieredWChunk) ID() uint64 {
	return c.id
}

func (c *tieredWChunk) Len() int {
	return c.length
}

func (c *tieredWChunk) FlushTo(offset int) error {
	if offset < c.uploaded {
		logger.Fatalf("Invalid offset: %d < %d", offset, c.uploaded)
	}
	for i, block := range c.pages {
		start := i * c.store.conf.BlockSize
		end := start + c.store.conf.BlockSize
		if start >= c.uploaded && end <= offset {
			if block != nil {
				c.upload(i)
			}
			c.uploaded = end
		}
	}

	return nil
}

func (c *tieredWChunk) Finish(length int) error {
	if c.length != length {
		return fmt.Errorf("Length mismatch: %v != %v", c.length, length)
	}

	n := (length-1)/c.store.conf.BlockSize + 1
	if err := c.FlushTo(n * c.store.conf.BlockSize); err != nil {
		return err
	}
	for i := 0; i < c.pendings; i++ {
		if err := <-c.errors; err != nil {
			c.uploadError = err
			return err
		}
	}
	return nil
}

func (c *tieredWChunk) Abort() {
	for i := range c.pages {
		for _, b := range c.pages[i] {
			freePage(b)
		}
		c.pages[i] = nil
	}
	// delete uploaded blocks
	c.length = c.uploaded
	_ = c.Remove()
}

type tieredStore struct {
	storage       *object.ObjectStorage
	cachedStore   *cachedStore
	tieredManager TieredManager
	TiredConfig   TieredConfig
}

func tieredChunkForWrite(id uint64, store *tieredStore) *tieredWChunk {
	return &tieredWChunk{
		rChunk:     rChunk{id, 0, store.cachedStore},
		pages:      make([][]*Page, chunkSize/store.cachedStore.conf.BlockSize),
		errors:     make(chan error, chunkSize/store.cachedStore.conf.BlockSize),
		tiredStore: store,
	}
}

func NewTieredStore(c ChunkStore, storage *object.ObjectStorage, config TieredConfig) ChunkStore {

	s := &tieredStore{
		storage:     storage,
		cachedStore: c.(*cachedStore),
		TiredConfig: config,
	}
	s.tieredManager = newTieredManager(s, &config)
	return s
}

func (store *tieredStore) NewReader(chunkid uint64, length int) Reader {
	return store.cachedStore.NewReader(chunkid, length)
}

func (store *tieredStore) NewWriter(chunkid uint64) Writer {
	return tieredChunkForWrite(chunkid, store)
}

func (store *tieredStore) Remove(chunkid uint64, length int) error {
	c := chunkForRead(chunkid, length, store.cachedStore)
	if c.length == 0 {
		// no block
		return nil
	}

	lastIndx := (c.length - 1) / c.store.conf.BlockSize
	var err error
	for i := 0; i <= lastIndx; i++ {
		// there could be multiple clients try to remove the same chunk in the same time,
		// any of them should succeed if any blocks is removed
		key := c.key(i)
		c.store.pendingMutex.Lock()
		delete(c.store.pendingKeys, key)
		c.store.pendingMutex.Unlock()
		// delete cache first
		c.store.bcache.remove(key)
		// delete local disk
		store.tieredManager.remove(key)

		// todo: optimize when the data is not in remote
		// delete remote
		if e := c.delete(i); e != nil {
			err = e
		}
	}
	return err
}

func (store *tieredStore) FillCache(chunkid uint64, length uint32) error {
	return store.cachedStore.FillCache(chunkid, length)
}

func (store *tieredStore) UsedMemory() int64 {
	return store.tieredManager.usedMemory()
}
