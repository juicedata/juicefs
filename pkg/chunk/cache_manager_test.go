/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestMetrics(t *testing.T) {
	conf := testConf()
	defer os.RemoveAll(conf.CacheDir)
	m := newCacheManager(&conf, nil, nil)
	metrics := m.(*cacheManager).metrics
	s := m.(*cacheManager).stores[0]
	content := []byte("helloworld")
	p := NewPage(content)
	s.cache("test", p, true, false)
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
	stagingPath, err := m.stage(key, content, 0)
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

func shutdownStore(s *cacheStore) {
	s.stateLock.Lock()
	defer s.stateLock.Unlock()
	s.state.stop()
	s.state = newDCState(dcDown, s)
}

func TestCacheManager(t *testing.T) {
	conf := defaultConf
	dir0, dir1, dir2 := t.TempDir(), t.TempDir(), t.TempDir()
	conf.CacheDir = dir0 + ":" + dir1 + ":" + dir2
	conf.AutoCreate = true
	manager := newCacheManager(&conf, nil, nil)
	require.True(t, !manager.isEmpty())

	m, ok := manager.(*cacheManager)
	require.True(t, ok)
	require.Equal(t, 3, m.length())

	// case: key rehash after store removal
	k1 := "k1"
	p1 := NewPage([]byte{1, 2, 3})
	defer p1.Release()
	m.cache(k1, p1, true, false)

	s1 := m.getStore(k1)
	require.NotNil(t, s1)

	m.Lock()
	shutdownStore(s1)
	m.Unlock()
	time.Sleep(3 * time.Second)

	rc, _ := m.load(k1)
	require.Nil(t, rc)
	_, exist := m.exist(k1)
	require.False(t, exist)

	s2 := m.getStore(k1)
	require.NotNil(t, s2)

	// case: remove all store
	m.Lock()
	for _, s := range m.storeMap {
		shutdownStore(s)
	}
	m.Unlock()
	time.Sleep(3 * time.Second)
	require.True(t, m.isEmpty())
}
