//go:build windows
// +build windows

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

package winfsp

import (
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/winfsp/cgofuse/fuse"
)

func newTestJuice(t *testing.T) *juice {
	t.Helper()
	v := newTestVFS(t)
	j := &juice{vfs: v, conf: v.Conf, asRoot: true}
	var err error
	if j.fs, err = fs.NewFileSystem(v.Conf, v.Meta, v.Store, nil); err != nil {
		t.Fatalf("new filesystem: %s", err)
	}
	j.Init()
	return j
}

func (j *juice) mustStat(t *testing.T, p string) *fuse.Stat_t {
	t.Helper()
	var st fuse.Stat_t
	if e := j.Getattr(p, &st, invalidFileHandle); e != 0 {
		t.Fatalf("getattr %s: %d", p, e)
	}
	return &st
}

func (j *juice) mustCreate(t *testing.T, p string) {
	t.Helper()
	e, fh := j.Create(p, fuse.O_CREAT|fuse.O_RDWR, 0644)
	if e != 0 {
		t.Fatalf("create %s: %d", p, e)
	}
	j.Release(p, fh)
}

var crtime0 = fuse.Timespec{Sec: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Unix()}

// TestCreationTime checks that the Windows creation time survives the
// operations that used to reset it: Utimens (SetBasicInfo with access/write
// times), deleting and re-creating a name, and the "write temp file, then
// rename over the original" save pattern.
func TestCreationTime(t *testing.T) {
	j := newTestJuice(t)

	j.mustCreate(t, "/a.txt")
	if e := j.Setcrtime("/a.txt", crtime0); e != 0 {
		t.Fatalf("setcrtime: %d", e)
	}
	if got := j.mustStat(t, "/a.txt").Birthtim.Sec; got != crtime0.Sec {
		t.Fatalf("setcrtime not stored: got %d want %d", got, crtime0.Sec)
	}

	// Utimens must change mtime only
	mt := fuse.Timespec{Sec: crtime0.Sec + 86400}
	if e := j.Utimens("/a.txt", []fuse.Timespec{{Sec: time.Now().Unix()}, mt}); e != 0 {
		t.Fatalf("utimens: %d", e)
	}
	st := j.mustStat(t, "/a.txt")
	if st.Birthtim.Sec != crtime0.Sec {
		t.Fatalf("utimens changed creation time: %d", st.Birthtim.Sec)
	}
	if st.Mtim.Sec != mt.Sec {
		t.Fatalf("utimens did not set mtime: got %d want %d", st.Mtim.Sec, mt.Sec)
	}

	// delete + re-create within the tunnel period keeps the creation time
	if e := j.Unlink("/a.txt"); e != 0 {
		t.Fatalf("unlink: %d", e)
	}
	j.mustCreate(t, "/a.txt")
	if got := j.mustStat(t, "/a.txt").Birthtim.Sec; got != crtime0.Sec {
		t.Fatalf("re-created file lost creation time: %d", got)
	}

	// rename a new file over the original keeps the original's creation time
	j.mustCreate(t, "/a.tmp")
	if e := j.Rename("/a.tmp", "/a.txt"); e != 0 {
		t.Fatalf("rename over: %d", e)
	}
	if got := j.mustStat(t, "/a.txt").Birthtim.Sec; got != crtime0.Sec {
		t.Fatalf("rename over original lost creation time: %d", got)
	}

	// rename original away, then rename temp file to its name
	j.mustCreate(t, "/a.tmp")
	if e := j.Rename("/a.txt", "/a.bak"); e != 0 {
		t.Fatalf("rename away: %d", e)
	}
	if e := j.Rename("/a.tmp", "/a.txt"); e != 0 {
		t.Fatalf("rename back: %d", e)
	}
	if got := j.mustStat(t, "/a.txt").Birthtim.Sec; got != crtime0.Sec {
		t.Fatalf("tunneled rename lost creation time: %d", got)
	}

	// an unrelated new file gets the current time
	j.mustCreate(t, "/b.txt")
	if got := j.mustStat(t, "/b.txt").Birthtim.Sec; time.Since(time.Unix(got, 0)) > time.Minute {
		t.Fatalf("new file has unexpected creation time %d", got)
	}
}

func TestNameTunnelExpire(t *testing.T) {
	var tn nameTunnel
	tn.put("/Dir/File.TXT", 1, 2)
	if s, n, ok := tn.take("/dir/file.txt"); !ok || s != 1 || n != 2 {
		t.Fatalf("take: %d %d %v", s, n, ok)
	}
	if _, _, ok := tn.take("/dir/file.txt"); ok {
		t.Fatal("entry should be consumed")
	}
	tn.put("/x", 3, 4)
	e := tn.entries["/x"]
	e.expire = time.Now().Add(-time.Second)
	tn.entries["/x"] = e
	if _, _, ok := tn.take("/x"); ok {
		t.Fatal("expired entry should not be returned")
	}
}
