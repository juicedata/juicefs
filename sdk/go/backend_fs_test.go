//go:build !libjfs

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

package juicefs

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

func testFSBackend(t *testing.T) *fsBackend {
	t.Helper()
	return newTestClient(t).backend.(*fsBackend)
}

func TestFSBackendIdentity(t *testing.T) {
	b := testFSBackend(t)
	if b.uid != uint32(os.Geteuid()) || b.gids[0] != uint32(os.Getegid()) {
		t.Fatalf("uid %d gids %v, want euid %d egid %d first", b.uid, b.gids, os.Geteuid(), os.Getegid())
	}
	groups, _ := os.Getgroups()
	for _, g := range groups {
		if !slices.Contains(b.gids, uint32(g)) {
			t.Fatalf("group %d missing from %v", g, b.gids)
		}
	}
	for i, g := range b.gids {
		if slices.Contains(b.gids[i+1:], g) {
			t.Fatalf("duplicate gid %d in %v", g, b.gids)
		}
	}
	ctx := b.ctx()
	if ctx.Uid() != b.uid || !slices.Equal(ctx.Gids(), b.gids) || ctx.Pid() != uint32(os.Getpid()) {
		t.Fatalf("ctx uid %d gids %v pid %d", ctx.Uid(), ctx.Gids(), ctx.Pid())
	}
}

func TestFSBackendGatherer(t *testing.T) {
	mfs, err := testFSBackend(t).gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather: %s", err)
	}
	var found bool
	for _, mf := range mfs {
		if !strings.HasPrefix(mf.GetName(), "juicefs_") {
			t.Fatalf("metric %s has no juicefs_ prefix", mf.GetName())
		}
		found = found || mf.GetName() == "juicefs_go_goroutines"
	}
	if !found {
		t.Fatal("juicefs_go_goroutines not gathered")
	}
}

func TestFSBackendErrors(t *testing.T) {
	empty := "sqlite3://" + filepath.Join(t.TempDir(), "empty.db")
	if _, err := New("test", empty, testConfig()); err == nil || !strings.Contains(err.Error(), "load setting") {
		t.Fatalf("unformatted volume: got %v", err)
	}
}

func TestToStat(t *testing.T) {
	for typ, want := range map[uint8]uint32{
		meta.TypeFile:      syscall.S_IFREG,
		meta.TypeDirectory: syscall.S_IFDIR,
		meta.TypeSymlink:   syscall.S_IFLNK,
		meta.TypeFIFO:      syscall.S_IFIFO,
		meta.TypeBlockDev:  syscall.S_IFBLK,
		meta.TypeCharDev:   syscall.S_IFCHR,
		meta.TypeSocket:    syscall.S_IFSOCK,
	} {
		st := toStat(1, &meta.Attr{Typ: typ, Mode: 02755})
		if st.Mode != want|02755 {
			t.Errorf("type %d: mode %#o, want %#o", typ, st.Mode, want|02755)
		}
	}

	attr := &meta.Attr{
		Typ:       meta.TypeCharDev,
		Mode:      0620,
		Uid:       1001,
		Gid:       1002,
		Rdev:      0x0501,
		Atime:     1700000001,
		Atimensec: 1,
		Mtime:     1700000002,
		Mtimensec: 999999999,
		Ctime:     1700000003,
		Ctimensec: 500,
		Nlink:     3,
		Length:    1 << 40,
	}
	st := toStat(12345, attr)
	want := Stat{
		Inode: 12345,
		Mode:  syscall.S_IFCHR | 0620,
		Nlink: 3,
		Uid:   1001,
		Gid:   1002,
		Size:  1 << 40,
		Rdev:  0x0501,
		Atime: time.Unix(1700000001, 1),
		Mtime: time.Unix(1700000002, 999999999),
		Ctime: time.Unix(1700000003, 500),
	}
	if *st != want {
		t.Fatalf("got %+v, want %+v", *st, want)
	}
	if fi := newFileInfo("/dev/tty", st); fi.Mode() != fs.ModeDevice|fs.ModeCharDevice|0620 || !fi.ModTime().Equal(want.Mtime) {
		t.Fatalf("FileInfo: mode %v mtime %v", fi.Mode(), fi.ModTime())
	}
}

// The expected values follow jfs_init in sdk/java/libjfs/main.go.
func TestConfigConversion(t *testing.T) {
	conf := DefaultConfig()
	conf.ReadOnly = true
	conf.NoBGJob = false
	conf.NoSession = true
	conf.CachePartialOnly = true
	conf.MaxReadahead = 32 << 20
	conf.DownloadLimit = 80
	format := &meta.Format{
		Name:          "test",
		BlockSize:     4096,
		Compression:   "lz4",
		HashPrefix:    true,
		UploadLimit:   100,
		DownloadLimit: 200,
	}

	mc := conf.metaConfig()
	if mc.Retries != 10 || mc.MaxDeletes != 10 || mc.SkipDirNlink != 20 ||
		mc.SkipDirMtime != 100*time.Millisecond || !mc.ReadOnly ||
		mc.OpenCache != 0 || mc.Heartbeat != 12*time.Second {
		t.Fatalf("meta config: %+v", mc)
	}
	if !mc.NoBGJob {
		t.Fatal("NoSession must imply NoBGJob")
	}
	// Fields not set by jfs_init keep the values of meta.DefaultConf.
	if def := meta.DefaultConf(); mc.AtimeMode != def.AtimeMode || mc.DirStatFlushPeriod != def.DirStatFlushPeriod {
		t.Fatalf("meta config defaults changed: %+v", mc)
	}

	cc := conf.chunkConfig(format)
	if cc.BlockSize != 4<<20 || cc.Compress != "lz4" || !cc.HashPrefix {
		t.Fatalf("chunk config from format: %+v", cc)
	}
	if cc.CacheDir != "memory" || cc.CacheMode != 0644 || cc.CacheSize != 100<<20 ||
		cc.FreeSpace != float32(0.1) || !cc.AutoCreate || cc.CacheFullBlock ||
		cc.CacheChecksum != "extend" || cc.CacheEviction != "2-random" ||
		cc.CacheScanInterval != time.Hour || cc.CacheExpire != 0 || !cc.OSCache {
		t.Fatalf("chunk cache config: %+v", cc)
	}
	if cc.MaxUpload != 20 || cc.MaxDownload != 200 || cc.MaxRetries != 10 ||
		cc.Prefetch != 1 || cc.Writeback || cc.GetTimeout != 5*time.Second ||
		cc.PutTimeout != 60*time.Second || cc.BufferSize != 300<<20 || cc.Readahead != 32<<20 {
		t.Fatalf("chunk io config: %+v", cc)
	}
	// A zero limit in conf falls back to the volume; both are in bytes/s.
	if cc.UploadLimit != 100*1e6/8 || cc.DownloadLimit != 80*1e6/8 {
		t.Fatalf("bandwidth limits: upload %d, download %d", cc.UploadLimit, cc.DownloadLimit)
	}

	vc := conf.vfsConfig(format, mc, cc)
	if vc.Meta != mc || vc.Chunk != cc || vc.Format.Name != "test" ||
		vc.AttrTimeout != time.Second || vc.EntryTimeout != 0 || vc.DirEntryTimeout != time.Second ||
		vc.AccessLog != "" || vc.FastResolve || vc.BackupMeta != time.Hour || vc.BackupSkipTrash {
		t.Fatalf("vfs config: %+v", vc)
	}
}

func TestFSBackendQuotaCmd(t *testing.T) {
	cmds := []uint8{quotaSet, quotaGet, quotaDel, quotaList, quotaCheck}
	want := []uint8{meta.QuotaSet, meta.QuotaGet, meta.QuotaDel, meta.QuotaList, meta.QuotaCheck}
	if !slices.Equal(cmds, want) {
		t.Fatalf("quota commands %v, want those of meta %v", cmds, want)
	}
}

func TestToTreeSummary(t *testing.T) {
	for typ, want := range map[uint8]fs.FileMode{
		meta.TypeFile:      0,
		meta.TypeDirectory: fs.ModeDir,
		meta.TypeSymlink:   fs.ModeSymlink,
		meta.TypeFIFO:      fs.ModeNamedPipe,
		meta.TypeBlockDev:  fs.ModeDevice,
		meta.TypeCharDev:   fs.ModeDevice | fs.ModeCharDevice,
		meta.TypeSocket:    fs.ModeSocket,
	} {
		if s := toTreeSummary(&meta.TreeSummary{Type: typ}); s.Type != want {
			t.Errorf("type %d: %v, want %v", typ, s.Type, want)
		}
	}
	s := toTreeSummary(&meta.TreeSummary{
		Inode: 2, Path: "d", Type: meta.TypeDirectory, Size: 3, Files: 4, Dirs: 5, Duration: time.Second,
		Children: []*meta.TreeSummary{{Path: "a", Type: meta.TypeFile, Size: 1, Files: 1}},
	})
	if c := s.Children[0]; s.Path != "d" || s.Size != 3 || s.Files != 4 || s.Dirs != 5 || len(s.Children) != 1 ||
		c.Path != "a" || c.Type != 0 || c.Size != 1 || c.Files != 1 || c.Children != nil {
		t.Fatalf("got %+v", s)
	}
}

func TestToSession(t *testing.T) {
	expire := time.Unix(1700000000, 0)
	s := &meta.Session{
		Sid:    7,
		Expire: expire,
		SessionInfo: meta.SessionInfo{
			Version: "1.3.0", HostName: "h", IPAddrs: []string{"10.0.0.1"},
			MountPoint: "/jfs", MountTime: expire, ProcessID: 42,
		},
		Sustained: []meta.Ino{100},
		Flocks:    []meta.Flock{{Inode: 101, Owner: 1, Ltype: "W"}},
	}
	got := toSession(s)
	if got.Sid != 7 || !got.Expire.Equal(expire) || got.Version != "1.3.0" || got.HostName != "h" ||
		!slices.Equal(got.IPAddrs, []string{"10.0.0.1"}) || got.MountPoint != "/jfs" || !got.MountTime.Equal(expire) ||
		got.ProcessID != 42 || !slices.Equal(got.Sustained, []uint64{100}) ||
		!slices.Equal(got.Flocks, []Flock{{Inode: 101, Owner: 1, Type: "W"}}) || got.Plocks != nil {
		t.Fatalf("got %+v", got)
	}
	if plockType(meta.F_RDLCK) != "R" || plockType(meta.F_WRLCK) != "W" {
		t.Fatalf("plock types: %s %s", plockType(meta.F_RDLCK), plockType(meta.F_WRLCK))
	}
}

func TestToVolumeSetting(t *testing.T) {
	f := &meta.Format{
		Name:          "v",
		UUID:          "u",
		Storage:       "redis",
		Bucket:        "redis://user:secret@host:6379/1",
		StorageClass:  "OLD",
		Tiers:         object.NewTiers("STANDARD_IA"),
		AccessKey:     "ak",
		SecretKey:     "sk",
		BlockSize:     4096,
		Compression:   "lz4",
		EncryptAlgo:   "aes256gcm-rsa",
		Capacity:      1 << 40,
		Inodes:        1000,
		TrashDays:     1,
		UploadLimit:   10,
		DownloadLimit: 20,
		EnableACL:     true,
	}
	want := VolumeSetting{
		Name:          "v",
		UUID:          "u",
		Storage:       "redis",
		Bucket:        "redis://user:****@host:6379/1",
		StorageClass:  "STANDARD_IA",
		BlockSize:     4 << 20,
		Compression:   "lz4",
		Capacity:      1 << 40,
		Inodes:        1000,
		TrashDays:     1,
		UploadLimit:   10,
		DownloadLimit: 20,
		EnableACL:     true,
	}
	if got := toVolumeSetting(f); got != want {
		t.Fatalf("not encrypted: got %+v, want %+v", got, want)
	}
	f.EncryptKey = "removed"
	want.EncryptAlgo = "aes256gcm-rsa"
	if got := toVolumeSetting(f); got != want {
		t.Fatalf("encrypted: got %+v, want %+v", got, want)
	}
}
