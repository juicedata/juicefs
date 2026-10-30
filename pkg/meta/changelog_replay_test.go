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

package meta

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newTestReplayer(t *testing.T) (*Replayer, *baseMeta) {
	dir := t.TempDir()
	uris := []string{"sqlite3://" + filepath.Join(dir, "src.db"), "sqlite3://" + filepath.Join(dir, "dst.db")}
	for _, uri := range uris {
		m := NewClient(uri, DefaultConf())
		if err := m.Init(&Format{Name: "test", UUID: "replay", Storage: "file", Bucket: dir + "/", BlockSize: 4096, DirStats: true, UserGroupQuota: true}, true); err != nil {
			t.Fatalf("init %s: %s", uri, err)
		}
		_ = m.Shutdown()
	}
	src := NewClient(uris[0], DefaultConf())
	if _, err := src.Load(true); err != nil {
		t.Fatalf("load source: %s", err)
	}
	r, err := NewReplayer(uris[1], src, false)
	if err != nil {
		t.Fatalf("new replayer: %s", err)
	}
	if r.progressLog, err = os.Create(filepath.Join(dir, "progress")); err != nil {
		t.Fatalf("create progress log: %s", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = src.Shutdown() })
	return r, r.dst.getBase()
}

func replayEntry(t *testing.T, r *Replayer, entry string) {
	t.Helper()
	e, err := ParseChangeEntry(1, "1.0|"+entry+"|(3,1)")
	if err != nil {
		t.Fatalf("parse %s: %s", entry, err)
	}
	if err = r.Replay(Background(), e); err != nil {
		t.Fatalf("replay %s: %s", entry, err)
	}
}

func TestReplaySetQuotaRefreshesCache(t *testing.T) {
	r, dst := newTestReplayer(t)
	if _, err := dst.en.doSetQuota(Background(), UserQuotaType, 1000, &Quota{MaxSpace: -1, MaxInodes: 1, UsedSpace: 4096, UsedInodes: 1}); err != nil {
		t.Fatalf("set baseline quota: %s", err)
	}
	dst.loadQuotas()
	replayEntry(t, r, fmt.Sprintf("SETQUOTA(%d,1000,-1,100,-1,-1)", UserQuotaType))
	replayEntry(t, r, "CREATE(1,new,1000,1000,1,420,18,,Keep,true,0,420):5000")
}

func TestReplayDirStatWithPendingUpdates(t *testing.T) {
	r, dst := newTestReplayer(t)
	var d, e Ino
	var attr Attr
	if st := dst.Mkdir(Background(), RootInode, "d", 0755, 0, 0, &d, &attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := dst.Mkdir(Background(), RootInode, "e", 0755, 0, 0, &e, &attr); st != 0 {
		t.Fatalf("mkdir e: %s", st)
	}
	dst.doFlushDirStat()
	replayEntry(t, r, fmt.Sprintf("CREATE(%d,f,0,0,1,420,18,,Keep,true,0,420):5000", d))
	replayEntry(t, r, fmt.Sprintf("DIRSTAT(%d,0,4096,1)", d))
	replayEntry(t, r, fmt.Sprintf("LINK(5000,%d,g,true):2", e))
	replayEntry(t, r, "WRITE(5000,0,0,1,4096,1716440753,0):1") // hard-linked: parents are resolved separately
	replayEntry(t, r, fmt.Sprintf("DIRSTAT(%d,4096,4096,1)", d))
	replayEntry(t, r, fmt.Sprintf("DIRSTAT(%d,4096,4096,1)", e))
	dst.doFlushDirStat()
	for _, ino := range []Ino{d, e} {
		if st, eno := dst.en.doGetDirStat(Background(), ino, false); eno != 0 || *st != (dirStat{4096, 4096, 1}) {
			t.Fatalf("dir stat of %d: %+v, %s", ino, st, eno)
		}
	}
}
