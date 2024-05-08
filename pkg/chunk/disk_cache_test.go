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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// Copy from https://github.com/prometheus/client_golang/blob/v1.14.0/prometheus/testutil/testutil.go
func toFloat64(c prometheus.Collector) float64 {
	var (
		m      prometheus.Metric
		mCount int
		mChan  = make(chan prometheus.Metric)
		done   = make(chan struct{})
	)

	go func() {
		for m = range mChan {
			mCount++
		}
		close(done)
	}()

	c.Collect(mChan)
	close(mChan)
	<-done

	if mCount != 1 {
		panic(fmt.Errorf("collected %d metrics instead of exactly 1", mCount))
	}

	pb := &dto.Metric{}
	if err := m.Write(pb); err != nil {
		panic(fmt.Errorf("error happened while collecting metrics: %w", err))
	}
	if pb.Gauge != nil {
		return pb.Gauge.GetValue()
	}
	if pb.Counter != nil {
		return pb.Counter.GetValue()
	}
	if pb.Untyped != nil {
		return pb.Untyped.GetValue()
	}
	panic(fmt.Errorf("collected a non-gauge/counter/untyped metric: %s", pb))
}

func TestNewCacheStore(t *testing.T) {
	s := newCacheStore(nil, defaultConf.CacheDir, 1<<30, 1, &defaultConf, nil)
	if s == nil {
		t.Fatalf("Create new cache store failed")
	}
}

func TestMetrics(t *testing.T) {
	m := newCacheManager(&defaultConf, nil, nil)
	metrics := m.(*cacheManager).metrics
	s := m.(*cacheManager).stores[0]
	content := []byte("helloworld")
	p := NewPage(content)
	s.cache("test", p, true)
	// Waiting for the cache to be flushed
	time.Sleep(time.Millisecond * 100)
	if toFloat64(metrics.cacheWrites) != 1.0 {
		t.Fatalf("expect the cacheWrites is 1")
	}

	if toFloat64(metrics.cacheWriteBytes) != float64(len(content)) {
		t.Fatalf("expect the cacheWriteBytes is %d", len(content))
	}

	if toFloat64(metrics.stageBlocks) != 0.0 {
		t.Fatalf("expect the stageBlocks is %d", len(content))
	}

	if toFloat64(metrics.stageBlockBytes) != 0.0 {
		t.Fatalf("expect the stageBlockBytes is %d", len(content))
	}
	key := fmt.Sprintf("chunks/0/5/5000_2_%d", len(content))
	stagingPath, err := m.stage(key, content, false)
	if err != nil {
		t.Fatalf("stage failed: %s", err)
	}
	if toFloat64(metrics.stageBlocks) != 1.0 {
		t.Fatalf("expect the stageBlocks is %d", len(content))
	}

	if toFloat64(metrics.stageBlockBytes) != float64(len(content)) {
		t.Fatalf("expect the stageBlockBytes is %d", len(content))
	}
	err = m.removeStage(key)
	if err != nil {
		t.Fatalf("faild to remove stage")
	}

	if toFloat64(metrics.stageBlocks) != 0.0 {
		t.Fatalf("expect the stageBlocks is %d", len(content))
	}

	if toFloat64(metrics.stageBlockBytes) != 0.0 {
		t.Fatalf("expect the stageBlockBytes is %d", len(content))
	}

	if _, err := os.Stat(stagingPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("expect the stageingPath %s not exists", stagingPath)
	}
}

func TestChecksum(t *testing.T) {
	m := newCacheManager(&defaultConf, nil, nil)
	s := m.(*cacheManager).stores[0]
	k1 := "0_0_10" // no checksum
	k2 := "1_0_10"
	k3 := "2_1_102400"
	k4 := "3_5_102400" // corrupt data
	k5 := "4_8_1048576"

	p := NewPage([]byte("helloworld"))
	s.cache(k1, p, true)

	s.checksum = CsFull
	s.cache(k2, p, true)

	buf := make([]byte, 102400)
	utils.RandRead(buf)
	s.cache(k3, NewPage(buf), true)

	fpath := s.cachePath(k4)
	f, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE, s.mode)
	if err != nil {
		t.Fatalf("Create cache file %s: %s", fpath, err)
	}
	if _, err = f.Write(buf); err != nil {
		_ = f.Close()
		t.Fatalf("Write cache file %s: %s", fpath, err)
	}
	corrupt := make([]byte, 102400)
	copy(corrupt, buf)
	for i := 98304; i < 102400; i++ { // reset 96K ~ 100K
		corrupt[i] = 0
	}
	if _, err = f.Write(checksum(corrupt)); err != nil {
		_ = f.Close()
		t.Fatalf("Write checksum to cache file %s: %s", fpath, err)
	}
	_ = f.Close()
	s.add(k4, 102400, uint32(time.Now().Unix()))

	buf = make([]byte, 1048576)
	utils.RandRead(buf)
	s.cache(k5, NewPage(buf), true)
	time.Sleep(time.Second * 5) // wait for cache file flushed

	check := func(key string, off int64, size int) error {
		rc, err := s.load(key)
		if err != nil {
			t.Fatalf("CacheStore load key %s: %s", key, err)
		}
		defer rc.Close()
		buf := make([]byte, size)
		_, err = rc.ReadAt(buf, off)
		return err
	}
	cases := []struct {
		key    string
		off    int64
		size   int
		expect bool
	}{
		{k1, 0, 10, true},
		{k1, 3, 5, true},
		{k2, 0, 10, true},
		{k2, 3, 5, true},
		{k3, 0, 102400, true},
		{k3, 8192, 92160, true}, // 8K ~ 98K
		{k4, 0, 102400, true},
		{k4, 8192, 92160, true}, // only CsExtend can detect the error
		{k5, 0, 1048576, true},
		{k5, 131072, 131072, true},
		{k5, 102400, 512000, true},
	}
	for _, l := range []string{CsNone, CsFull, CsShrink, CsExtend} {
		s.checksum = l
		if l != CsNone {
			cases[6].expect = false
		}
		if l == CsExtend {
			cases[7].expect = false
		}
		for _, c := range cases {
			if err = check(c.key, c.off, c.size); (err == nil) != c.expect {
				t.Fatalf("CacheStore check level %s case %+v: %s", l, c, err)
			}
		}
	}
}

func TestExpand(t *testing.T) {
	rs := expandDir("/not/exists/jfsCache")
	if len(rs) != 1 || rs[0] != "/not/exists/jfsCache" {
		t.Errorf("expand: %v", rs)
		t.FailNow()
	}

	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "aaa1"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa2"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa3"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa3", "jfscache"), 0755)
	_ = os.Mkdir(filepath.Join(dir, "aaa3", "jfscache", "jfs"), 0755)

	rs = expandDir(filepath.Join(dir, "aaa*", "jfscache", "jfs"))
	if len(rs) != 3 || rs[0] != filepath.Join(dir, "aaa1", "jfscache", "jfs") {
		t.Errorf("expand: %v", rs)
		t.FailNow()
	}
}

func BenchmarkLoadCached(b *testing.B) {
	dir := b.TempDir()
	s := newCacheStore(nil, filepath.Join(dir, "diskCache"), 1<<30, 1, &defaultConf, nil)
	p := NewPage(make([]byte, 1024))
	key := "/chunks/1_1024"
	s.cache(key, p, false)
	time.Sleep(time.Millisecond * 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, e := s.load(key); e == nil {
			_ = f.Close()
		} else {
			b.FailNow()
		}
	}
}

func BenchmarkLoadUncached(b *testing.B) {
	dir := b.TempDir()
	s := newCacheStore(nil, filepath.Join(dir, "diskCache"), 1<<30, 1, &defaultConf, nil)
	key := "chunks/222_1024"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, e := s.load(key); e != nil {
			_ = f.Close()
		}
	}
}

func TestCheckPath(t *testing.T) {
	cases := []struct {
		path     string
		expected bool
	}{
		// unix path style
		{path: "chunks/111/222/3333_3333_3333", expected: true},
		{path: "chunks/111/222/3333_3333_0", expected: true},
		{path: "chunks/0/0/0_0_0", expected: true},
		{path: "chunks/01/10/0_01_0", expected: true},
		{path: "achunks/111/222/3333_3333_3333", expected: false},
		{path: "chunksa/111/222/3333_3333_3333", expected: false},
		{path: "chunksa", expected: false},
		{path: "chunks/111", expected: false},
		{path: "chunks/111/2222", expected: false},
		{path: "chunks/111/2222/3", expected: false},
		{path: "chunks/111/2222/3333_3333", expected: false},
		{path: "chunks/111/2222/3333_3333_3333_4444", expected: false},
		{path: "chunks/111/2222/3333_3333_3333/4444", expected: false},
		{path: "chunks/111_/2222/3333_3333_3333", expected: false},
		{path: "chunks/111/22_22/3333_3333_3333", expected: false},
		{path: "chunks/111/22_22/3333_3333_3333", expected: false},
	}
	for _, c := range cases {
		if res := pathReg.MatchString(c.path); res != c.expected {
			t.Fatalf("check path %s expected %v but got %v", c.path, c.expected, res)
		}
	}
}

func shutdownStore(s diskCache) {
	if c, ok := s.(*cacheStore); ok {
		atomic.StoreUint32(&c.ioErrCnt, uint32(maxIOErrors))
		atomic.StoreUint32(&c.ioCnt, uint32(maxIOErrors))
		c.tryShutdown()
	}
}

func TestCacheManager(t *testing.T) {
	conf := defaultConf
	conf.CacheDir = "/tmp/diskCache0:/tmp/diskCache1:/tmp/diskCache2"
	conf.AutoCreate = true
	defer os.RemoveAll("/tmp/diskCache0")
	defer os.RemoveAll("/tmp/diskCache1")
	defer os.RemoveAll("/tmp/diskCache2")

	checkInterval = 3600 // never check
	numTriesToRestore = 1

	manager := newCacheManager(&conf, nil, nil)
	require.True(t, !manager.isEmpty())

	m, ok := manager.(*cacheManager)
	require.True(t, ok)
	require.Equal(t, 3, m.length())

	// case: io err
	cs := m.getStore("random").(*cacheStore)
	m.Lock()
	atomic.StoreUint32(&cs.ioErrCnt, uint32(maxIOErrors)-1)
	atomic.StoreUint32(&cs.ioCnt, uint32(maxIOErrors)*2)
	cs.tryShutdown()
	require.True(t, cs.available())

	atomic.StoreUint32(&cs.ioCnt, uint32(maxIOErrors)-1)
	cs.tryShutdown()
	require.False(t, cs.available())
	m.Unlock()

	// case: key rehash after store removal
	k1 := "key1"
	s1 := m.getStore(k1)
	data := []byte{1, 2, 3}
	p1 := NewPage(data)
	defer p1.Release()
	m.cache(k1, p1, true)

	m.Lock()
	shutdownStore(s1)
	m.Unlock()
	time.Sleep(3 * time.Second)
	require.Equal(t, 1, m.length())
	s2 := m.getStore(k1)
	require.NotNil(t, s2)
	_, err := m.load(k1)
	require.True(t, errors.Is(err, errNotCached))

	// case: restore cache
	m.restore(cs)
	require.Equal(t, 2, m.length())

	// case: remove all store
	m.Lock()
	for _, s := range m.storeMap {
		shutdownStore(s)
	}
	m.Unlock()
	time.Sleep(3 * time.Second)
	require.True(t, m.isEmpty())
}

func testRestoreCache(t *testing.T, dir string, shutdown int) {
	conf := defaultConf
	conf.CacheDir = dir
	dirs := strings.Split(dir, ":")
	require.True(t, len(dirs) >= shutdown)
	defer func() {
		for _, dir := range dirs {
			os.RemoveAll(dir)
		}
	}()
	conf.AutoCreate = true

	checkInterval, numTriesToRestore = 1, 1
	manager := newCacheManager(&conf, nil, nil)
	m, _ := manager.(*cacheManager)
	require.Equal(t, m.length(), len(dirs))

	genKey := func(id, indx int) string {
		return fmt.Sprintf("chunks/%v/%v/%v_%v_%v", id/1000/1000, id/1000, id, indx, 3)
	}

	// cache
	p := NewPage([]byte{1, 2, 3})
	defer p.Release()
	for i := 0; i < len(dirs)*100; i++ {
		m.cache(genKey(i+1, 0), p, true)
	}

	// shutdown
	m.Lock()
	cnt := 0
	for _, s := range m.storeMap {
		shutdownStore(s)
		cnt++
		if cnt >= shutdown {
			break
		}
	}
	m.Unlock()

	// wait for restore
	time.Sleep(3 * time.Second)
	require.Equal(t, len(dirs), m.length())
}

func TestRestoreCache(t *testing.T) {
	genDir := func(num int) string {
		dirs := make([]string, 0, num)
		for i := 0; i < num; i++ {
			dirs = append(dirs, fmt.Sprintf("/tmp/diskCache%d", i))
		}
		return strings.Join(dirs, ":")
	}

	testRestoreCache(t, genDir(1), 1)
	testRestoreCache(t, genDir(10), 5)
	testRestoreCache(t, genDir(20), 10)
}
