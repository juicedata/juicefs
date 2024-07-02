/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setState(s *cacheStore, state int) {
	s.stateLock.Lock()
	defer s.stateLock.Unlock()
	s.state.stop()
	s.state = newDCState(state, s)
}

func testDiskCacheState(t *testing.T, cacheNum int) {
	oriTickDurForUnstable, oriMinIOSuccToNormal, oriMaxDurToDown := tickDurForUnstable, minIOSuccToNormal, maxDurToDown
	defer func() {
		tickDurForUnstable, minIOSuccToNormal, maxDurToDown = oriTickDurForUnstable, oriMinIOSuccToNormal, oriMaxDurToDown
	}()

	genDirs := func(num int) []string {
		dirs := make([]string, 0, num)
		for i := 0; i < num; i++ {
			dirs = append(dirs, fmt.Sprintf("/tmp/diskCache%d", i))
		}
		return dirs
	}

	conf := defaultConf
	dirs := genDirs(cacheNum)
	conf.CacheDir = strings.Join(dirs, ":")
	conf.AutoCreate = true
	defer func() {
		for _, dir := range dirs {
			_ = os.RemoveAll(dir)
		}
	}()

	manager := newCacheManager(&conf, nil, nil)
	require.False(t, manager.isEmpty())

	m, ok := manager.(*cacheManager)
	require.True(t, ok)
	require.Equal(t, cacheNum, m.length())

	// case: cache
	data := []byte{1, 2, 3}
	page := NewPage(data)
	defer page.Release()
	k1 := probeCacheKey(0, len(data))
	m.cache(k1, page, true, false)
	time.Sleep(time.Second)

	// case: normal -> unstable
	s1 := m.getStore(k1)
	for i := 0; i <= int(numIOErrToUnstable); i++ {
		s1.state.onIOErr()
	}
	require.Equal(t, dcUnstable, s1.state.state())

	// case: probe in unstable
	time.Sleep(time.Second)
	require.GreaterOrEqual(t, atomic.LoadUint32(&s1.state.(*unstableDC).ioCnt), uint32(1))

	// case: unstable concurrency limit
	for i := 0; i < int(maxConcurrencyForUnstable); i++ {
		s1.state.beforeCacheOp()
	}
	_, err := m.load(k1)
	assert.Equal(t, errUnstableCoLimit, err)
	for i := 0; i < int(maxConcurrencyForUnstable); i++ {
		s1.state.afterCacheOp()
	}

	// case: unstable -> normal
	tickDurForUnstable = time.Second
	minIOSuccToNormal = 1
	setState(s1, dcUnstable)
	s1.state.(*unstableDC).doProbe(k1, page)
	time.Sleep(2 * time.Second)
	require.Equal(t, dcNormal, s1.state.state())

	// case: unstable -> down
	tickDurForUnstable = time.Second
	maxDurToDown = 1
	minIOSuccToNormal = 5 * 60
	setState(s1, dcUnstable)
	time.Sleep(2 * time.Second)
	require.Equal(t, dcDown, s1.state.state())
}

func TestDiskCacheState(t *testing.T) {
	testDiskCacheState(t, 1)
	testDiskCacheState(t, 10)
}
