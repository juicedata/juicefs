//go:build libjfs

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
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
)

// libjfsConfKeys returns the JSON keys of javaConf in libjfs.
func libjfsConfKeys(t *testing.T) map[string]bool {
	f, err := parser.ParseFile(token.NewFileSet(), "../java/libjfs/main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "javaConf" {
			return true
		}
		for _, field := range ts.Type.(*ast.StructType).Fields.List {
			tag, _ := strconv.Unquote(field.Tag.Value)
			name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
			keys[name] = true
		}
		return false
	})
	return keys
}

// The values are parsed back as jfs_init does.
func TestJavaConf(t *testing.T) {
	conf := DefaultConfig()
	conf.OpenCache = 1500 * time.Millisecond
	conf.CacheExpire = 36 * time.Hour
	conf.CacheSize = 1<<40 + 1
	conf.FreeSpaceRatio = 0.25
	conf.CachePartialOnly = true
	conf.MaxReadahead = 32 << 20
	conf.UploadLimit = 100
	j := conf.javaConf("sqlite3://x.db")

	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	known := libjfsConfKeys(t)
	for k := range m {
		if !known[k] {
			t.Errorf("key %q is not in javaConf of libjfs", k)
		}
	}

	for name, d := range map[string]struct {
		got  string
		want time.Duration
	}{
		"openCache":         {j.OpenCache, conf.OpenCache},
		"backupMeta":        {j.BackupMeta, conf.BackupMeta},
		"heartbeat":         {j.Heartbeat, conf.Heartbeat},
		"cacheScanInterval": {j.CacheScanInterval, conf.CacheScanInterval},
		"cacheExpire":       {j.CacheExpire, conf.CacheExpire},
		"skipDirMtime":      {j.SkipDirMtime, conf.SkipDirMtime},
		"getTimeout":        {j.GetTimeout, conf.GetTimeout},
		"putTimeout":        {j.PutTimeout, conf.PutTimeout},
		"attrTimeout":       {j.AttrTimeout, conf.AttrCache},
		"entryTimeout":      {j.EntryTimeout, conf.EntryCache},
		"dirEntryTimeout":   {j.DirEntryTimeout, conf.DirEntryCache},
		"pushInterval":      {j.PushInterval, conf.PushInterval},
	} {
		if got := utils.Duration(d.got); got != d.want {
			t.Errorf("%s: %q parsed as %s, want %s", name, d.got, got, d.want)
		}
	}
	for name, b := range map[string]struct {
		got  string
		want uint64
	}{
		"cacheSize":  {j.CacheSize, conf.CacheSize},
		"memorySize": {j.MemorySize, conf.BufferSize},
		"readahead":  {j.Readahead, conf.MaxReadahead},
	} {
		if got := utils.ParseBytesStr(name, b.got, 'M'); got != b.want {
			t.Errorf("%s: %q parsed as %d, want %d", name, b.got, got, b.want)
		}
	}
	if utils.ParseMbpsStr("upload", j.UploadLimit) != 100 || utils.ParseMbpsStr("download", j.DownloadLimit) != 0 {
		t.Errorf("limits: %q %q", j.UploadLimit, j.DownloadLimit)
	}
	if f, err := strconv.ParseFloat(j.FreeSpace, 64); err != nil || f != conf.FreeSpaceRatio {
		t.Errorf("freeSpace: %q", j.FreeSpace)
	}
	if j.MetaURL != "sqlite3://x.db" || !j.AutoCreate || j.CacheFullBlock || j.MaxDownloads != 200 ||
		j.Caller != 1 || j.UsageTag != "go-sdk" || j.MaxDeletes != conf.MaxDeletes {
		t.Errorf("javaConf: %+v", j)
	}
}

// TestLibjfsNULName checks that names containing NUL give EINVAL instead of
// acting on the part before NUL, where a C string would cut them.
func TestLibjfsNULName(t *testing.T) {
	c := newTestClient(t)
	if err := c.Mkdir("a", 0755); err != nil {
		t.Fatal(err)
	}
	const old, nw = "a\x00b", "n\x00x"
	for _, x := range []struct {
		desc string
		call func() error
	}{
		{"Stat", func() error { return second(c.Stat(old)) }},
		{"Mkdir", func() error { return c.Mkdir(nw, 0755) }},
		{"Rename new", func() error { return c.Rename("a", nw) }},
		{"Symlink target", func() error { return c.Symlink(old, "x") }},
		{"WriteFile", func() error { return c.WriteFile(nw, nil, 0644) }},
		{"Setxattr attr", func() error { return c.Setxattr("a", "user.x\x00y", []byte("v"), 0) }},
		{"Clone dst", func() error { return c.Clone("a", nw, false) }},
		{"SetQuota", func() error { return c.SetQuota(old, QuotaOptions{Inodes: 10}) }},
	} {
		if err := x.call(); errnoOf(err) != syscall.EINVAL {
			t.Errorf("%s: %v", x.desc, err)
		}
	}
	if entries, err := c.ReadDir("."); err != nil || len(entries) != 1 || entries[0].Name() != "a" {
		t.Errorf("ReadDir(.) = %v, %v; want only a", entries, err)
	}
	if attrs, err := c.Listxattr("a"); err != nil || len(attrs) != 0 {
		t.Errorf("Listxattr(a) = %q, %v", attrs, err)
	}
	if qs, err := c.ListQuota(); err != nil || len(qs) != 0 {
		t.Errorf("ListQuota() = %v, %v", qs, err)
	}
}

// TestLibjfsSameName checks that a new Client of the same volume name, which
// makes libjfs rebuild the identities from the user names, keeps the identity
// of an existing Client.
func TestLibjfsSameName(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the identity from the names is root, the same as the euid")
	}
	url := newTestVolume(t)
	a, err := New("test", url, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New("test", url, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	createFile(t, a, "f")
	fi, err := a.Stat("f")
	if err != nil {
		t.Fatal(err)
	}
	if uid := fi.Sys().(*Stat).Uid; uid != uint32(os.Geteuid()) {
		t.Fatalf("owner of f = %d, want %d", uid, os.Geteuid())
	}
}

func TestLibjfsErrno(t *testing.T) {
	for r, want := range map[int32]error{
		0:                            nil,
		3:                            nil,
		-2:                           syscall.ENOENT,
		-int32(syscall.EACCES):       syscall.EACCES,
		-39:                          syscall.ENOTEMPTY,
		-40:                          syscall.ELOOP,
		-61:                          syscall.ENODATA,
		-69:                          syscall.EDQUOT, // P56
		-95:                          syscall.ENOTSUP,
		-int32(syscall.ENAMETOOLONG): syscall.ENAMETOOLONG,
	} {
		if got := libjfsErrno(r); got != want {
			t.Errorf("%d: %v, want %v", r, got, want)
		}
	}
}

func TestDecodeTreeSummary(t *testing.T) {
	data, err := json.Marshal(&meta.TreeSummary{
		Inode: 2, Path: "d", Type: meta.TypeDirectory, Size: 3, Files: 4, Dirs: 5, Duration: time.Second,
		Children: []*meta.TreeSummary{
			{Path: "a", Type: meta.TypeFile, Size: 1, Files: 1},
			{Path: "l", Type: meta.TypeSymlink},
			{Path: "p", Type: meta.TypeFIFO},
			{Path: "b", Type: meta.TypeBlockDev},
			{Path: "c", Type: meta.TypeCharDev},
			{Path: "s", Type: meta.TypeSocket},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ms metaTreeSummary
	if err = json.Unmarshal(data, &ms); err != nil {
		t.Fatal(err)
	}
	s := ms.convert()
	if s.Path != "d" || s.Type != fs.ModeDir || s.Size != 3 || s.Files != 4 || s.Dirs != 5 || len(s.Children) != 6 {
		t.Fatalf("got %+v", s)
	}
	var types []fs.FileMode
	for _, c := range s.Children {
		types = append(types, c.Type)
	}
	want := []fs.FileMode{0, fs.ModeSymlink, fs.ModeNamedPipe, fs.ModeDevice, fs.ModeDevice | fs.ModeCharDevice, fs.ModeSocket}
	if !slices.Equal(types, want) || s.Children[0].Size != 1 || s.Children[0].Files != 1 {
		t.Fatalf("children: %v, want %v", types, want)
	}
}

func TestDecodeStatus(t *testing.T) {
	expire := time.Unix(1700000000, 0)
	format := &meta.Format{
		Name:          "v",
		UUID:          "u",
		Storage:       "s3",
		Bucket:        "https://ak:sk@host/b",
		Tiers:         object.NewTiers("STANDARD_IA"),
		BlockSize:     4096,
		Compression:   "lz4",
		EncryptKey:    "removed",
		EncryptAlgo:   "aes256gcm-rsa",
		Capacity:      1 << 40,
		Inodes:        1000,
		TrashDays:     1,
		UploadLimit:   10,
		DownloadLimit: 20,
		EnableACL:     true,
	}
	session := &meta.Session{
		Sid:    7,
		Expire: expire,
		SessionInfo: meta.SessionInfo{
			Version: "1.3.0", HostName: "h", IPAddrs: []string{"10.0.0.1"},
			MountPoint: "/jfs", MountTime: expire, ProcessID: 42,
		},
		Sustained: []meta.Ino{100},
		Flocks:    []meta.Flock{{Inode: 101, Owner: 1, Ltype: "W"}},
	}
	data, err := json.Marshal(&meta.Sections{
		Setting:  format,
		Sessions: []*meta.Session{session},
		Stat:     &meta.Statistic{UsedSpace: 1, AvailableSpace: 2, UsedInodes: 3, AvailableInodes: 4, TrashFileCount: 5, PendingDeletedSliceSize: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := decodeStatus(data, true)
	if err != nil {
		t.Fatal(err)
	}
	want := VolumeSetting{
		Name: "v", UUID: "u", Storage: "s3", Bucket: "https://ak:****@host/b", StorageClass: "STANDARD_IA",
		BlockSize: 4 << 20, Compression: "lz4", EncryptAlgo: "aes256gcm-rsa", Capacity: 1 << 40, Inodes: 1000,
		TrashDays: 1, UploadLimit: 10, DownloadLimit: 20, EnableACL: true,
	}
	if r.Setting != want {
		t.Fatalf("setting: %+v, want %+v", r.Setting, want)
	}
	st := r.Stat
	if st.UsedSpace != 1 || st.AvailableSpace != 2 || st.UsedInodes != 3 || st.AvailableInodes != 4 ||
		st.Trash == nil || *st.Trash != (TrashStat{TrashFileCount: 5, PendingDeletedSliceSize: 6}) {
		t.Fatalf("stat: %+v %+v", st, st.Trash)
	}
	got := r.Sessions[0]
	if len(r.Sessions) != 1 || got.Sid != 7 || !got.Expire.Equal(expire) || got.Version != "1.3.0" ||
		got.HostName != "h" || !slices.Equal(got.IPAddrs, []string{"10.0.0.1"}) || got.MountPoint != "/jfs" ||
		!got.MountTime.Equal(expire) || got.ProcessID != 42 || !slices.Equal(got.Sustained, []uint64{100}) ||
		!slices.Equal(got.Flocks, []Flock{{Inode: 101, Owner: 1, Type: "W"}}) || got.Plocks != nil {
		t.Fatalf("session: %+v", got)
	}
	if plockType(meta.F_RDLCK) != "R" || plockType(meta.F_WRLCK) != "W" {
		t.Fatalf("plock types: %s %s", plockType(meta.F_RDLCK), plockType(meta.F_WRLCK))
	}

	if r, err = decodeStatus(data, false); err != nil || r.Stat.Trash != nil {
		t.Fatalf("without trash: %+v, %v", r, err)
	}
	format.EncryptKey = ""
	if data, err = json.Marshal(&meta.Sections{Setting: format, Stat: &meta.Statistic{}}); err != nil {
		t.Fatal(err)
	}
	if r, err = decodeStatus(data, false); err != nil || r.Setting.EncryptAlgo != "" {
		t.Fatalf("not encrypted: %+v, %v", r, err)
	}
}
