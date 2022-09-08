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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewCacheStore(t *testing.T) {
	s := newCacheStore(nil, defaultConf.CacheDir, 1<<30, 1, &defaultConf, nil)
	if s == nil {
		t.Fatalf("Create new cache store failed")
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
