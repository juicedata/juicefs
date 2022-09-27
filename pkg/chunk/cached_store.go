/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package chunk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
)

const chunkSize = 1 << 26 // 64M
const pageSize = 1 << 16  // 64K
const SlowRequest = time.Second * time.Duration(10)

var (
	logger = utils.GetLogger("juicefs")
)

type pendingItem struct {
	key   string
	fpath string
}

// slice for read and remove
type rSlice struct {
	id     uint64
	length int
	store  *cachedStore
}

func sliceForRead(id uint64, length int, store *cachedStore) *rSlice {
	return &rSlice{id, length, store}
}

func (s *rSlice) blockSize(indx int) int {
	bsize := s.length - indx*s.store.conf.BlockSize
	if bsize > s.store.conf.BlockSize {
		bsize = s.store.conf.BlockSize
	}
	return bsize
}

func (s *rSlice) key(indx int) string {
	if s.store.conf.HashPrefix {
		return fmt.Sprintf("chunks/%02X/%v/%v_%v_%v", s.id%256, s.id/1000/1000, s.id, indx, s.blockSize(indx))
	}
	return fmt.Sprintf("chunks/%v/%v/%v_%v_%v", s.id/1000/1000, s.id/1000, s.id, indx, s.blockSize(indx))
}

func (s *rSlice) index(off int) int {
	return off / s.store.conf.BlockSize
}

func (s *rSlice) keys() []string {
	if s.length <= 0 {
		return nil
	}
	lastIndx := (s.length - 1) / s.store.conf.BlockSize
	keys := make([]string, lastIndx+1)
	for i := 0; i <= lastIndx; i++ {
		keys[i] = s.key(i)
	}
	return keys
}

func (s *rSlice) ReadAt(ctx context.Context, page *Page, off int) (n int, err error) {
	p := page.Data
	if len(p) == 0 {
		return 0, nil
	}
	if off >= s.length {
		return 0, io.EOF
	}

	indx := s.index(off)
	boff := off % s.store.conf.BlockSize
	blockSize := s.blockSize(indx)
	if boff+len(p) > blockSize {
		// read beyond currend page
		var got int
		for got < len(p) {
			// aligned to current page
			l := utils.Min(len(p)-got, s.blockSize(s.index(off))-off%s.store.conf.BlockSize)
			pp := page.Slice(got, l)
			n, err = s.ReadAt(ctx, pp, off)
			pp.Release()
			if err != nil {
				return got + n, err
			}
			if n == 0 {
				return got, io.EOF
			}
			got += n
			off += n
		}
		return got, nil
	}

	key := s.key(indx)
	if s.store.conf.CacheSize > 0 {
		start := time.Now()
		r, err := s.store.bcache.load(key)
		if err == nil {
			n, err = r.ReadAt(p, int64(boff))
			_ = r.Close()
			if err == nil {
				s.store.cacheHits.Add(1)
				s.store.cacheHitBytes.Add(float64(n))
				s.store.cacheReadHist.Observe(time.Since(start).Seconds())
				return n, nil
			}
			if f, ok := r.(*os.File); ok {
				logger.Warnf("remove partial cached block %s: %d %s", f.Name(), n, err)
				_ = os.Remove(f.Name())
			}
		}
	}

	s.store.cacheMiss.Add(1)
	s.store.cacheMissBytes.Add(float64(len(p)))

	if s.store.seekable && boff > 0 && len(p) <= blockSize/4 {
		if s.store.downLimit != nil {
			s.store.downLimit.Wait(int64(len(p)))
		}
		// partial read
		st := time.Now()
		in, err := s.store.storage.Get(key, int64(boff), int64(len(p)))
		if err == nil {
			n, err = io.ReadFull(in, p)
			_ = in.Close()
		}
		used := time.Since(st)
		logger.Debugf("GET %s RANGE(%d,%d) (%s, %.3fs)", key, boff, len(p), err, used.Seconds())
		if used > SlowRequest {
			logger.Infof("slow request: GET %s (%v, %.3fs)", key, err, used.Seconds())
		}
		s.store.objectDataBytes.WithLabelValues("GET").Add(float64(n))
		s.store.objectReqsHistogram.WithLabelValues("GET").Observe(used.Seconds())
		s.store.fetcher.fetch(key)
		if err == nil {
			return n, nil
		} else {
			s.store.objectReqErrors.Add(1)
		}
	}

	block, err := s.store.group.Execute(key, func() (*Page, error) {
		tmp := page
		if boff > 0 || len(p) < blockSize {
			tmp = NewOffPage(blockSize)
		} else {
			tmp.Acquire()
		}
		tmp.Acquire()
		err := utils.WithTimeout(func() error {
			defer tmp.Release()
			return s.store.load(key, tmp, s.store.shouldCache(blockSize), false)
		}, s.store.conf.GetTimeout)
		return tmp, err
	})
	defer block.Release()
	if err != nil {
		return 0, err
	}
	if block != page {
		copy(p, block.Data[boff:])
	}
	return len(p), nil
}

func (s *rSlice) delete(indx int) error {
	key := s.key(indx)
	st := time.Now()
	err := s.store.storage.Delete(key)
	used := time.Since(st)
	logger.Debugf("DELETE %v (%v, %.3fs)", key, err, used.Seconds())
	if used > SlowRequest {
		logger.Infof("slow request: DELETE %v (%v, %.3fs)", key, err, used.Seconds())
	}
	s.store.objectReqsHistogram.WithLabelValues("DELETE").Observe(used.Seconds())
	if err != nil {
		s.store.objectReqErrors.Add(1)
	}
	return err
}

func (s *rSlice) Remove() error {
	if s.length == 0 {
		// no block
		return nil
	}

	lastIndx := (s.length - 1) / s.store.conf.BlockSize
	for i := 0; i <= lastIndx; i++ {
		// there could be multiple clients try to remove the same chunk in the same time,
		// any of them should succeed if any blocks is removed
		key := s.key(i)
		s.store.removePending(key)
		s.store.bcache.remove(key)
	}

	if s.store.conf.MaxDeletes == 0 {
		return errors.New("skip deleting objects because MaxDeletes is 0")
	}
	var err error
	s.store.currentDelete <- struct{}{}
	for i := 0; i <= lastIndx; i++ {
		if e := s.delete(i); e != nil {
			err = e
		}
	}
	<-s.store.currentDelete
	return err
}

var pagePool = make(chan *Page, 128)

func allocPage(sz int) *Page {
	if sz != pageSize {
		return NewOffPage(sz)
	}
	select {
	case p := <-pagePool:
		return p
	default:
		return NewOffPage(pageSize)
	}
}

func freePage(p *Page) {
	if cap(p.Data) != pageSize {
		p.Release()
		return
	}
	select {
	case pagePool <- p:
	default:
		p.Release()
	}
}

// slice for write only
type wSlice struct {
	rSlice
	pages       [][]*Page
	uploaded    int
	errors      chan error
	uploadError error
	pendings    int
}

func sliceForWrite(id uint64, store *cachedStore) *wSlice {
	return &wSlice{
		rSlice: rSlice{id, 0, store},
		pages:  make([][]*Page, chunkSize/store.conf.BlockSize),
		errors: make(chan error, chunkSize/store.conf.BlockSize),
	}
}

func (s *wSlice) SetID(id uint64) {
	s.id = id
}

func (s *wSlice) WriteAt(p []byte, off int64) (n int, err error) {
	if int(off)+len(p) > chunkSize {
		return 0, fmt.Errorf("write out of chunk boudary: %d > %d", int(off)+len(p), chunkSize)
	}
	if off < int64(s.uploaded) {
		return 0, fmt.Errorf("Cannot overwrite uploaded block: %d < %d", off, s.uploaded)
	}

	// Fill previous blocks with zeros
	if s.length < int(off) {
		zeros := make([]byte, int(off)-s.length)
		_, _ = s.WriteAt(zeros, int64(s.length))
	}

	for n < len(p) {
		indx := s.index(int(off) + n)
		boff := (int(off) + n) % s.store.conf.BlockSize
		var bs = pageSize
		if indx > 0 || bs > s.store.conf.BlockSize {
			bs = s.store.conf.BlockSize
		}
		bi := boff / bs
		bo := boff % bs
		var page *Page
		if bi < len(s.pages[indx]) {
			page = s.pages[indx][bi]
		} else {
			page = allocPage(bs)
			page.Data = page.Data[:0]
			s.pages[indx] = append(s.pages[indx], page)
		}
		left := len(p) - n
		if bo+left > bs {
			page.Data = page.Data[:bs]
		} else if len(page.Data) < bo+left {
			page.Data = page.Data[:bo+left]
		}
		n += copy(page.Data[bo:], p[n:])
	}
	if int(off)+n > s.length {
		s.length = int(off) + n
	}
	return n, nil
}

func (store *cachedStore) put(key string, p *Page) error {
	if store.upLimit != nil {
		store.upLimit.Wait(int64(len(p.Data)))
	}
	p.Acquire()
	return utils.WithTimeout(func() error {
		defer p.Release()
		st := time.Now()
		err := store.storage.Put(key, bytes.NewReader(p.Data))
		used := time.Since(st)
		logger.Debugf("PUT %s (%s, %.3fs)", key, err, used.Seconds())
		if used > SlowRequest {
			logger.Infof("slow request: PUT %v (%v, %.3fs)", key, err, used.Seconds())
		}
		store.objectDataBytes.WithLabelValues("PUT").Add(float64(len(p.Data)))
		store.objectReqsHistogram.WithLabelValues("PUT").Observe(used.Seconds())
		if err != nil {
			store.objectReqErrors.Add(1)
		}
		return err
	}, store.conf.PutTimeout)
}

func (store *cachedStore) upload(key string, block *Page, s *wSlice) error {
	sync := s != nil
	blen := len(block.Data)
	bufSize := store.compressor.CompressBound(blen)
	var buf *Page
	if bufSize > blen {
		buf = NewOffPage(bufSize)
	} else {
		buf = block
		buf.Acquire()
	}
	defer buf.Release()
	if sync && blen < store.conf.BlockSize {
		// block will be freed after written into disk
		store.bcache.cache(key, block, false)
	}
	n, err := store.compressor.Compress(buf.Data, block.Data)
	block.Release()
	if err != nil {
		return fmt.Errorf("Compress block key %s: %s", key, err)
	}
	buf.Data = buf.Data[:n]

	try, max := 0, 3
	if sync {
		max = store.conf.MaxRetries + 1
	}
	for ; try < max; try++ {
		time.Sleep(time.Second * time.Duration(try*try))
		if s != nil && s.uploadError != nil {
			err = fmt.Errorf("(cancelled) upload block %s: %s (after %d tries)", key, err, try)
			break
		}
		if err = store.put(key, buf); err == nil {
			break
		}
		logger.Warnf("Upload %s: %s (try %d)", key, err, try+1)
	}
	if err != nil && try >= max {
		err = fmt.Errorf("(max tries) upload block %s: %s (after %d tries)", key, err, try)
	}
	return err
}

func (s *wSlice) upload(indx int) {
	blen := s.blockSize(indx)
	key := s.key(indx)
	pages := s.pages[indx]
	s.pages[indx] = nil
	s.pendings++

	go func() {
		var block *Page
		var off int
		if len(pages) == 1 {
			block = pages[0]
			off = len(block.Data)
		} else {
			block = NewOffPage(blen)
			for _, b := range pages {
				off += copy(block.Data[off:], b.Data)
				freePage(b)
			}
		}
		if off != blen {
			panic(fmt.Sprintf("block length does not match: %v != %v", off, blen))
		}
		if s.store.conf.Writeback {
			stagingPath, err := s.store.bcache.stage(key, block.Data, s.store.shouldCache(blen))
			if err != nil {
				logger.Warnf("write %s to disk: %s, upload it directly", stagingPath, err)
			} else {
				s.errors <- nil
				if s.store.conf.UploadDelay == 0 {
					select {
					case s.store.currentUpload <- true:
						defer func() { <-s.store.currentUpload }()
						if err = s.store.upload(key, block, nil); err == nil {
							s.store.bcache.uploaded(key, blen)
							if os.Remove(stagingPath) == nil {
								if m, ok := s.store.bcache.(*cacheManager); ok {
									m.stageBlocks.Sub(1)
									m.stageBlockBytes.Sub(float64(blen))
								}
							}
						} else { // add to delay list and wait for later scanning
							s.store.addDelayedStaging(key, stagingPath, time.Now().Add(time.Second*30), false)
						}
						return
					default:
					}
				}
				block.Release()
				s.store.addDelayedStaging(key, stagingPath, time.Now(), s.store.conf.UploadDelay == 0)
				return
			}
		}
		s.store.currentUpload <- true
		defer func() { <-s.store.currentUpload }()
		s.errors <- s.store.upload(key, block, s)
	}()
}

func (s *wSlice) ID() uint64 {
	return s.id
}

func (s *wSlice) Len() int {
	return s.length
}

func (s *wSlice) FlushTo(offset int) error {
	if offset < s.uploaded {
		panic(fmt.Sprintf("Invalid offset: %d < %d", offset, s.uploaded))
	}
	for i, block := range s.pages {
		start := i * s.store.conf.BlockSize
		end := start + s.store.conf.BlockSize
		if start >= s.uploaded && end <= offset {
			if block != nil {
				s.upload(i)
			}
			s.uploaded = end
		}
	}

	return nil
}

func (s *wSlice) Finish(length int) error {
	if s.length != length {
		return fmt.Errorf("Length mismatch: %v != %v", s.length, length)
	}

	n := (length-1)/s.store.conf.BlockSize + 1
	if err := s.FlushTo(n * s.store.conf.BlockSize); err != nil {
		return err
	}
	for i := 0; i < s.pendings; i++ {
		if err := <-s.errors; err != nil {
			s.uploadError = err
			return err
		}
	}
	return nil
}

func (s *wSlice) Abort() {
	for i := range s.pages {
		for _, b := range s.pages[i] {
			freePage(b)
		}
		s.pages[i] = nil
	}
	// delete uploaded blocks
	s.length = s.uploaded
	_ = s.Remove()
}

// Config contains options for cachedStore
type Config struct {
	CacheDir          string
	CacheMode         os.FileMode
	CacheSize         int64
	CacheChecksum     string
	CacheScanInterval time.Duration
	FreeSpace         float32
	AutoCreate        bool
	Compress          string
	MaxUpload         int
	MaxDeletes        int
	MaxRetries        int
	UploadLimit       int64 // bytes per second
	DownloadLimit     int64 // bytes per second
	Writeback         bool
	UploadDelay       time.Duration
	HashPrefix        bool
	BlockSize         int
	GetTimeout        time.Duration
	PutTimeout        time.Duration
	CacheFullBlock    bool
	BufferSize        int
	Readahead         int
	Prefetch          int
}

func (c *Config) SelfCheck(uuid string) {
	if c.CacheSize == 0 {
		logger.Warnf("cache-size is 0, writeback and prefetch will be disabled")
		c.Writeback = false
		c.Prefetch = 0
		c.CacheDir = "memory"
	}
	if !c.Writeback && c.UploadDelay > 0 {
		logger.Warnf("delayed upload is disabled in non-writeback mode")
		c.UploadDelay = 0
	}
	if c.MaxUpload <= 0 {
		logger.Warnf("max-uploads should be greater than 0, set it to 1")
		c.MaxUpload = 1
	}
	if c.BufferSize <= 32<<20 {
		logger.Warnf("buffer-size should be more than 32 MiB")
		c.BufferSize = 32 << 20
	}
	if c.CacheDir != "memory" {
		ds := utils.SplitDir(c.CacheDir)
		for i := range ds {
			ds[i] = filepath.Join(ds[i], uuid)
		}
		c.CacheDir = strings.Join(ds, string(os.PathListSeparator))
	}
	if cs := []string{CsNone, CsFull, CsShrink, CsExtend}; !utils.StringContains(cs, c.CacheChecksum) {
		logger.Warnf("verify-cache-checksum should be one of %v", cs)
		c.CacheChecksum = CsFull
	}
}

type cachedStore struct {
	storage       object.ObjectStorage
	bcache        CacheManager
	fetcher       *prefetcher
	conf          Config
	group         *Controller
	currentUpload chan bool
	currentDelete chan struct{}
	pendingCh     chan pendingItem
	pendingKeys   map[string]time.Time
	pendingMutex  sync.Mutex
	compressor    compress.Compressor
	seekable      bool
	upLimit       *ratelimit.Bucket
	downLimit     *ratelimit.Bucket

	cacheHits           prometheus.Counter
	cacheMiss           prometheus.Counter
	cacheHitBytes       prometheus.Counter
	cacheMissBytes      prometheus.Counter
	cacheReadHist       prometheus.Histogram
	objectReqsHistogram *prometheus.HistogramVec
	objectReqErrors     prometheus.Counter
	objectDataBytes     *prometheus.CounterVec
}

func (store *cachedStore) load(key string, page *Page, cache bool, forceCache bool) (err error) {
	defer func() {
		e := recover()
		if e != nil {
			err = fmt.Errorf("recovered from %s", e)
		}
	}()
	needed := store.compressor.CompressBound(len(page.Data))
	compressed := needed > len(page.Data)
	// we don't know the actual size for compressed block
	if store.downLimit != nil && !compressed {
		store.downLimit.Wait(int64(len(page.Data)))
	}
	err = errors.New("Not downloaded")
	var in io.ReadCloser
	tried := 0
	start := time.Now()
	// it will be retried outside
	for err != nil && tried < 2 {
		time.Sleep(time.Second * time.Duration(tried*tried))
		if tried > 0 {
			logger.Warnf("GET %s: %s; retrying", key, err)
			store.objectReqErrors.Add(1)
			start = time.Now()
		}
		in, err = store.storage.Get(key, 0, -1)
		tried++
	}
	var n int
	var buf []byte
	if err == nil {
		if compressed {
			c := NewOffPage(needed)
			defer c.Release()
			buf = c.Data
		} else {
			buf = page.Data
		}
		n, err = io.ReadFull(in, buf)
		_ = in.Close()
	}
	if compressed && err == io.ErrUnexpectedEOF {
		err = nil
	}
	used := time.Since(start)
	logger.Debugf("GET %s (%s, %.3fs)", key, err, used.Seconds())
	if used > SlowRequest {
		logger.Infof("slow request: GET %s (%v, %.3fs)", key, err, used.Seconds())
	}
	if store.downLimit != nil && compressed {
		store.downLimit.Wait(int64(n))
	}
	store.objectDataBytes.WithLabelValues("GET").Add(float64(n))
	store.objectReqsHistogram.WithLabelValues("GET").Observe(used.Seconds())
	if err != nil {
		store.objectReqErrors.Add(1)
		return fmt.Errorf("get %s: %s", key, err)
	}
	if compressed {
		n, err = store.compressor.Decompress(page.Data, buf[:n])
	}
	if err != nil || n < len(page.Data) {
		return fmt.Errorf("read %s fully: %s (%d < %d) after %s (tried %d)", key, err, n, len(page.Data),
			used, tried)
	}
	if cache {
		store.bcache.cache(key, page, forceCache)
	}
	return nil
}

// NewCachedStore create a cached store.
func NewCachedStore(storage object.ObjectStorage, config Config, reg prometheus.Registerer) ChunkStore {
	compressor := compress.NewCompressor(config.Compress)
	if compressor == nil {
		logger.Fatalf("unknown compress algorithm: %s", config.Compress)
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 10
	}
	if config.GetTimeout == 0 {
		config.GetTimeout = time.Second * 60
	}
	if config.PutTimeout == 0 {
		config.PutTimeout = time.Second * 60
	}
	store := &cachedStore{
		storage:       storage,
		conf:          config,
		currentUpload: make(chan bool, config.MaxUpload),
		currentDelete: make(chan struct{}, config.MaxDeletes),
		compressor:    compressor,
		seekable:      compressor.CompressBound(0) == 0,
		pendingCh:     make(chan pendingItem, 100*config.MaxUpload),
		pendingKeys:   make(map[string]time.Time),
		group:         &Controller{},
	}
	if config.UploadLimit > 0 {
		// there are overheads coming from HTTP/TCP/IP
		store.upLimit = ratelimit.NewBucketWithRate(float64(config.UploadLimit)*0.85, config.UploadLimit)
	}
	if config.DownloadLimit > 0 {
		store.downLimit = ratelimit.NewBucketWithRate(float64(config.DownloadLimit)*0.85, config.DownloadLimit)
	}
	store.initMetrics()
	store.bcache = newCacheManager(&config, reg, func(key, fpath string, force bool) bool {
		if force {
			return store.addDelayedStaging(key, fpath, time.Time{}, true)
		} else if fi, err := os.Stat(fpath); err == nil {
			return store.addDelayedStaging(key, fpath, fi.ModTime(), false)
		} else {
			return false
		}
	})
	if config.CacheSize == 0 {
		config.Prefetch = 0 // disable prefetch if cache is disabled
	}
	store.fetcher = newPrefetcher(config.Prefetch, func(key string) {
		size := parseObjOrigSize(key)
		if size == 0 || size > store.conf.BlockSize {
			return
		}
		p := NewOffPage(size)
		defer p.Release()
		_ = store.load(key, p, true, true)
	})

	if store.conf.CacheDir != "memory" && store.conf.Writeback {
		for i := 0; i < store.conf.MaxUpload; i++ {
			go store.uploader()
		}
		interval := time.Minute
		if d := store.conf.UploadDelay; d > 0 {
			logger.Infof("delay uploading by %s", d)
			if d < time.Minute {
				interval = d
			}
		}
		go func() {
			for {
				time.Sleep(interval)
				store.scanDelayedStaging()
			}
		}()
	}
	store.regMetrics(reg)
	return store
}

func (store *cachedStore) initMetrics() {
	store.cacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_hits",
		Help: "read from cached block",
	})
	store.cacheMiss = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_miss",
		Help: "missed read from cached block",
	})
	store.cacheHitBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_hit_bytes",
		Help: "read bytes from cached block",
	})
	store.cacheMissBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "blockcache_miss_bytes",
		Help: "missed bytes from cached block",
	})
	store.cacheReadHist = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "blockcache_read_hist_seconds",
		Help:    "read cached block latency distribution",
		Buckets: prometheus.ExponentialBuckets(0.00001, 2, 20),
	})
	store.objectReqsHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "object_request_durations_histogram_seconds",
		Help:    "Object requests latency distributions.",
		Buckets: prometheus.ExponentialBuckets(0.01, 1.5, 25),
	}, []string{"method"})
	store.objectReqErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "object_request_errors",
		Help: "failed requests to object store",
	})
	store.objectDataBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "object_request_data_bytes",
		Help: "Object requests size in bytes.",
	}, []string{"method"})
}

func (store *cachedStore) regMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(store.cacheHits)
	reg.MustRegister(store.cacheHitBytes)
	reg.MustRegister(store.cacheMiss)
	reg.MustRegister(store.cacheMissBytes)
	reg.MustRegister(store.cacheReadHist)
	reg.MustRegister(store.objectReqsHistogram)
	reg.MustRegister(store.objectReqErrors)
	reg.MustRegister(store.objectDataBytes)
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "blockcache_blocks",
			Help: "number of cached blocks",
		},
		func() float64 {
			cnt, _ := store.bcache.stats()
			return float64(cnt)
		}))
	reg.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "blockcache_bytes",
			Help: "number of cached bytes",
		},
		func() float64 {
			_, used := store.bcache.stats()
			return float64(used)
		}))
}

func (store *cachedStore) shouldCache(size int) bool {
	return store.conf.CacheFullBlock || size < store.conf.BlockSize || store.conf.UploadDelay > 0
}

func parseObjOrigSize(key string) int {
	p := strings.LastIndexByte(key, '_')
	l, _ := strconv.Atoi(key[p+1:])
	return l
}

func (store *cachedStore) uploadStagingFile(key string, stagingPath string) {
	store.currentUpload <- true
	defer func() {
		<-store.currentUpload
	}()

	store.pendingMutex.Lock()
	addTime, ok := store.pendingKeys[key]
	store.pendingMutex.Unlock()
	if !ok {
		logger.Debugf("Key %s is not needed, drop it", key)
		return
	}
	blen := parseObjOrigSize(key)
	f, err := openCacheFile(stagingPath, blen, store.conf.CacheChecksum)
	if err != nil {
		store.pendingMutex.Lock()
		_, ok = store.pendingKeys[key]
		store.pendingMutex.Unlock()
		if ok {
			logger.Errorf("Open staging file %s: %s", stagingPath, err)
		} else {
			logger.Debugf("Key %s is not needed, drop it", key)
		}
		return
	}
	block := NewOffPage(blen)
	_, err = f.ReadAt(block.Data, 0)
	_ = f.Close()
	if err != nil {
		block.Release()
		logger.Errorf("Read staging file %s: %s", stagingPath, err)
		return
	}

	if m, ok := store.bcache.(*cacheManager); ok {
		m.stageBlockDelay.Add(time.Since(addTime).Seconds())
	}
	if err = store.upload(key, block, nil); err == nil {
		store.bcache.uploaded(key, blen)
		store.removePending(key)
		if os.Remove(stagingPath) == nil {
			if m, ok := store.bcache.(*cacheManager); ok {
				m.stageBlocks.Sub(1)
				m.stageBlockBytes.Sub(float64(blen))
			}
		}
	}
}

func (store *cachedStore) addDelayedStaging(key, stagingPath string, added time.Time, force bool) bool {
	store.pendingMutex.Lock()
	store.pendingKeys[key] = added
	store.pendingMutex.Unlock()
	if force || time.Since(added) > store.conf.UploadDelay {
		select {
		case store.pendingCh <- pendingItem{key, stagingPath}:
			return true
		default:
		}
	}
	return false
}

func (store *cachedStore) removePending(key string) {
	store.pendingMutex.Lock()
	delete(store.pendingKeys, key)
	store.pendingMutex.Unlock()
}

func (store *cachedStore) scanDelayedStaging() {
	cutoff := time.Now().Add(-store.conf.UploadDelay)
	store.pendingMutex.Lock()
	defer store.pendingMutex.Unlock()
	for key, added := range store.pendingKeys {
		store.pendingMutex.Unlock()
		if added.Before(cutoff) {
			store.pendingCh <- pendingItem{key, store.bcache.stagePath(key)}
		}
		store.pendingMutex.Lock()
	}
}

func (store *cachedStore) uploader() {
	for it := range store.pendingCh {
		store.uploadStagingFile(it.key, it.fpath)
	}
}

func (store *cachedStore) NewReader(id uint64, length int) Reader {
	return sliceForRead(id, length, store)
}

func (store *cachedStore) NewWriter(id uint64) Writer {
	return sliceForWrite(id, store)
}

func (store *cachedStore) Remove(id uint64, length int) error {
	r := sliceForRead(id, length, store)
	return r.Remove()
}

func (store *cachedStore) FillCache(id uint64, length uint32) error {
	r := sliceForRead(id, int(length), store)
	keys := r.keys()
	var err error
	for _, k := range keys {
		f, e := store.bcache.load(k)
		if e == nil { // already cached
			_ = f.Close()
			continue
		}
		size := parseObjOrigSize(k)
		if size == 0 || size > store.conf.BlockSize {
			logger.Warnf("Invalid size: %s %d", k, size)
			continue
		}
		p := NewOffPage(size)
		defer p.Release()
		if e := store.load(k, p, true, true); e != nil {
			logger.Warnf("Failed to load key: %s %s", k, e)
			err = e
		}
	}
	return err
}

func (store *cachedStore) UsedMemory() int64 {
	return store.bcache.usedMemory()
}

var _ ChunkStore = &cachedStore{}
