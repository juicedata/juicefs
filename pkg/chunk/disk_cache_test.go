/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package chunk

import (
	"os"
	"testing"
	"time"
)

func TestNewCacheStore(t *testing.T) {
	s := newCacheStore("/tmp/diskCache", 1<<30, 1, &defaultConf, nil)
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

	_ = os.Mkdir("/tmp/aaa1", 0755)
	_ = os.Mkdir("/tmp/aaa2", 0755)
	_ = os.Mkdir("/tmp/aaa3", 0755)
	_ = os.Mkdir("/tmp/aaa3/jfscache", 0755)
	_ = os.Mkdir("/tmp/aaa3/jfscache/jfs", 0755)

	rs = expandDir("/tmp/aaa*/jfscache/jfs")
	if len(rs) != 3 || rs[0] != "/tmp/aaa1/jfscache/jfs" {
		t.Errorf("expand: %v", rs)
		t.FailNow()
	}
}

func BenchmarkLoadCached(b *testing.B) {
	s := newCacheStore("/tmp/diskCache", 1<<30, 1, &defaultConf, nil)
	p := NewPage(make([]byte, 1024))
	key := "/chunks/1_1024"
	s.cache(key, p, false)
	time.Sleep(time.Millisecond * 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, e := s.load(key); e == nil {
			f.Close()
		} else {
			b.FailNow()
		}
	}
}

func BenchmarkLoadUncached(b *testing.B) {
	s := newCacheStore("/tmp/diskCache", 1<<30, 1, &defaultConf, nil)
	key := "/chunks/222_1024"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, e := s.load(key); e != nil {
			f.Close()
		}
	}
}
