/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
package sync

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

func collectAll(c <-chan object.Object) []string {
	r := make([]string, 0)
	for s := range c {
		r = append(r, s.Key())
	}
	return r
}

// nolint:errcheck
func TestIterator(t *testing.T) {
	m, _ := object.CreateStorage("mem", "", "", "", "")
	m.Put(ctx, "a", bytes.NewReader([]byte("a")))
	m.Put(ctx, "b", bytes.NewReader([]byte("a")))
	m.Put(ctx, "aa", bytes.NewReader([]byte("a")))
	m.Put(ctx, "c", bytes.NewReader([]byte("a")))

	ch, _ := ListAll(m, "", "a", "b", true)
	keys := collectAll(ch)
	if len(keys) != 3 {
		t.Fatalf("length should be 3, but got %d", len(keys))
	}
	if !reflect.DeepEqual(keys, []string{"a", "aa", "b"}) {
		t.Fatalf("result wrong: %s", keys)
	}

	// Single object
	s, _ := object.CreateStorage("mem", "", "", "", "")
	s.Put(ctx, "a", bytes.NewReader([]byte("a")))
	ch, _ = ListAll(s, "", "", "", true)
	keys = collectAll(ch)
	if !reflect.DeepEqual(keys, []string{"a"}) {
		t.Fatalf("result wrong: %s", keys)
	}
}

func TestIeratorSingleEmptyKey(t *testing.T) {
	// utils.SetLogLevel(logrus.DebugLevel)

	// Construct mem storage
	s, _ := object.CreateStorage("mem", "", "", "", "")
	err := s.Put(ctx, "abc", bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("Put error: %q", err)
	}

	// Simulate command line prefix in SRC or DST
	s = object.WithPrefix(s, "abc")
	ch, _ := ListAll(s, "", "", "", true)
	keys := collectAll(ch)
	if !reflect.DeepEqual(keys, []string{""}) {
		t.Fatalf("result wrong: %s", keys)
	}
}

func deepEqualWithOutMtime(a, b object.Object) bool {
	return a.IsDir() == b.IsDir() && a.Key() == b.Key() && a.Size() == b.Size() &&
		math.Abs(a.Mtime().Sub(b.Mtime()).Seconds()) < 1
}

// nolint:errcheck
func TestSync(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"
	config := &Config{
		Start:       "",
		End:         "",
		Threads:     50,
		ListThreads: 1,
		Update:      true,
		Perms:       true,
		Dry:         false,
		DeleteSrc:   false,
		Limit:       -1,
		DeleteDst:   false,
		Exclude:     []string{"c*"},
		Include:     []string{"a[1-9]", "a*"},
		MaxSize:     math.MaxInt64,
		Verbose:     false,
		Quiet:       true,
	}
	os.Args = []string{"--include", "a[1-9]", "--exclude", "a*", "--exclude", "c*"}
	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	a.Put(ctx, "a1", bytes.NewReader([]byte("a1")))
	a.Put(ctx, "a2", bytes.NewReader([]byte("a2")))
	a.Put(ctx, "abc", bytes.NewReader([]byte("abc")))
	a.Put(ctx, "c1", bytes.NewReader([]byte("c1")))
	a.Put(ctx, "c2", bytes.NewReader([]byte("c2")))

	b, _ := object.CreateStorage("file", tmpB, "", "", "")
	b.Put(ctx, "a1", bytes.NewReader([]byte("a1")))
	b.Put(ctx, "ba", bytes.NewReader([]byte("a1")))

	// Copy a2
	if err := Sync(a, b, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	if c := copied.Current(); c != 1 {
		t.Fatalf("should copy 1 keys, but got %d", c)
	}

	if err := Sync(a, b, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	// No copy occurred
	if c := copied.Current(); c != 0 {
		t.Fatalf("should copy 0 keys, but got %d", c)
	}

	// Now a: {"a1", "a2", "abc", "c1", "c2"}, b: {"a1", "a2", "ba"}
	// Copy "ba" from b to a
	os.Args = []string{}
	config.Exclude = nil
	config.rules = nil
	if err := Sync(b, a, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	if c := copied.Current(); c != 1 {
		t.Fatalf("should copy 1 keys, but got %d", c)
	}
	// Now a: {"a1", "a2", "abc", "ba", "c1", "c2"}, b: {"a1", "a2", "ba"}
	aRes, _ := ListAll(a, "", "", "", true)
	bRes, _ := ListAll(b, "", "", "", true)

	var aObjs, bObjs []object.Object
	for obj := range aRes {
		aObjs = append(aObjs, obj)
	}
	for obj := range bRes {
		bObjs = append(bObjs, obj)
	}

	if !deepEqualWithOutMtime(aObjs[1], bObjs[1]) {
		t.FailNow()
	}

	if !deepEqualWithOutMtime(aObjs[4], bObjs[len(bObjs)-1]) {
		t.Fatalf("expect %+v but got %+v", aObjs[4], bObjs[len(bObjs)-1])
	}
	// Test --force-update option
	config.ForceUpdate = true
	// Forcibly copy {"a1", "a2", "abc","c1","c2","ba"} from a to b.
	if err := Sync(a, b, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
}

// nolint:errcheck
func TestSyncIncludeAndExclude(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"
	config := &Config{
		Start:       "",
		End:         "",
		Threads:     50,
		ListThreads: 1,
		Update:      true,
		Perms:       true,
		Dry:         false,
		DeleteSrc:   false,
		DeleteDst:   false,
		Verbose:     false,
		Limit:       -1,
		Quiet:       true,
		MaxSize:     math.MaxInt64,
		Exclude:     []string{"1"},
	}
	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	b, _ := object.CreateStorage("file", tmpB, "", "", "")

	simple := []string{"a1/z1/z2", "a2", "ab1", "ab2", "b1", "b2", "c1", "c2"}
	testCases := []struct {
		srcKey, args, want []string
	}{
		{
			srcKey: simple,
			args:   []string{"--include", "xx*", "--include", "xxx*"},
			want:   []string{"a1/", "a1/z1/", "a1/z1/z2", "a2", "ab1", "ab2", "b1", "b2", "c1", "c2"},
		},
		{
			srcKey: simple,
			args:   []string{"--exclude", "a*", "--exclude", "c*"},
			want:   []string{"b1", "b2"},
		},
		{
			srcKey: simple,
			args:   []string{"--exclude", "a[1-2]", "--include", "a*"},
			want:   []string{"ab1", "ab2", "b1", "b2", "c1", "c2"},
		},
		{
			srcKey: simple,
			args:   []string{"--exclude", "ab?", "--include", "a*"},
			want:   []string{"a1/", "a1/z1/", "a1/z1/z2", "a2", "b1", "b2", "c1", "c2"},
		},
		{
			srcKey: simple,
			args:   []string{"--include", "a*", "--exclude", "c*"},
			want:   []string{"a1/", "a1/z1/", "a1/z1/z2", "a2", "ab1", "ab2", "b1", "b2"},
		},
		{
			srcKey: simple,
			args:   []string{"--exclude", "a*", "--exclude", "c*"},
			want:   []string{"b1", "b2"},
		},
		{
			srcKey: []string{"a1/b1/c1", "a1/b1/c2", "a1/b2/c1", "a1/b2/c2", "a2/b1/c2", "a3/b2/c2", "a4"},
			args:   []string{"--exclude", "a*/b[1-2]/c1", "--exclude", "a4"},
			want:   []string{"a1/", "a1/b1/", "a1/b1/c2", "a1/b2/", "a1/b2/c2", "a2/", "a2/b1/", "a2/b1/c2", "a3/", "a3/b2/", "a3/b2/c2"},
		},
	}

	for _, testCase := range testCases {
		_ = os.RemoveAll(tmpA)
		_ = os.RemoveAll(tmpB)
		os.Args = testCase.args
		for _, k := range testCase.srcKey {
			a.Put(ctx, k, bytes.NewReader([]byte(k)))
		}
		if err := Sync(a, b, config); err != nil {
			t.Fatalf("sync: %s", err)
		}

		bRes, _ := ListAll(b, "", "", "", true)
		var bKeys []string
		for obj := range bRes {
			bKeys = append(bKeys, obj.Key())
		}
		if !reflect.DeepEqual(bKeys[1:], testCase.want) {
			t.Errorf("sync args  %v, want %v, but get %v", os.Args, testCase.want, bKeys)
		}
	}
}

func TestParseRules(t *testing.T) {
	tests := []struct {
		args      []string
		wantRules []rule
	}{
		{
			args:      []string{"--include", "a"},
			wantRules: []rule{{pattern: "a", include: true}},
		},
		{
			args:      []string{"--exclude", "a", "--include", "b"},
			wantRules: []rule{{pattern: "a"}, {pattern: "b", include: true}},
		},
		{
			args:      []string{"--include", "a", "--test", "t", "--exclude", "b"},
			wantRules: []rule{{pattern: "a", include: true}, {pattern: "b"}},
		},
		{
			args:      []string{"--include", "a", "--test", "t", "--exclude"},
			wantRules: []rule{{pattern: "a", include: true}},
		},
		{
			args:      []string{"--include", "a", "--exclude", "b", "--include", "c", "--exclude", "d"},
			wantRules: []rule{{pattern: "a", include: true}, {pattern: "b"}, {pattern: "c", include: true}, {pattern: "d"}},
		},
		{
			args:      []string{"--include", "a", "--include", "b", "--test", "--exclude", "c", "--exclude", "d"},
			wantRules: []rule{{pattern: "a", include: true}, {pattern: "b", include: true}, {pattern: "c"}, {pattern: "d"}},
		},
		{
			args:      []string{"--include=a", "--include=b", "--exclude=c", "--exclude=d", "--test=aaa"},
			wantRules: []rule{{pattern: "a", include: true}, {pattern: "b", include: true}, {pattern: "c"}, {pattern: "d"}},
		},
		{
			args:      []string{"-include=a", "--test", "t", "--include=b", "--exclude=c", "-exclude="},
			wantRules: []rule{{pattern: "a", include: true}, {pattern: "b", include: true}, {pattern: "c"}},
		},
	}
	for _, tt := range tests {
		if gotRules := parseIncludeRules(tt.args); !reflect.DeepEqual(gotRules, tt.wantRules) {
			t.Errorf("got %+v, want %+v", gotRules, tt.wantRules)
		}
	}
}

func TestSyncLink(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"

	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	a.Put(ctx, "a1", bytes.NewReader([]byte("test")))
	as := a.(object.SupportSymlink)
	as.Symlink(tmpA+"a1", "l1")
	as.Symlink("./../a1", "d1/l2")
	as.Symlink("./../notExist", "l3")

	b, _ := object.CreateStorage("file", tmpB, "", "", "")
	bs := b.(object.SupportSymlink)
	bs.Symlink(tmpB+"a1", "l1")

	if err := Sync(a, b, &Config{
		Threads:     50,
		Update:      true,
		Perms:       true,
		ListThreads: 1,
		Links:       true,
		Quiet:       true,
		Limit:       -1,
		ForceUpdate: true,
		MaxSize:     math.MaxInt64,
	}); err != nil {
		t.Fatalf("sync: %s", err)
	}

	l1, err := bs.Readlink("l1")
	if err != nil || l1 != tmpA+"a1" {
		t.Fatalf("readlink: %s content: %s", err, l1)
	}
	content, err := b.Get(ctx, "l1", 0, -1)
	if err != nil {
		t.Fatalf("get content failed: %s", err)
	}
	if c, err := io.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content failed: err %s content %s", err, string(c))
	}

	l2, err := bs.Readlink("d1/l2")
	if err != nil || l2 != "./../a1" {
		t.Fatalf("readlink: %s", err)
	}
	content, err = b.Get(ctx, "d1/l2", 0, -1)
	if err != nil {
		t.Fatalf("content failed: %s", err)
	}
	if c, err := io.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content failed: err %s content %s", err, string(c))
	}

	l3, err := bs.Readlink("l3")
	if err != nil || l3 != "./../notExist" {
		t.Fatalf("readlink: %s", err)
	}
}

func TestSyncFilesFromSymlinkDirWithLinks(t *testing.T) {
	for _, entry := range []string{"dir-link"} { // TODO: "dir-link/" would failed
		t.Run(entry, func(t *testing.T) {
			srcDir := t.TempDir()
			dstDir := t.TempDir()
			filesFrom := srcDir + "/files-from"
			if err := os.WriteFile(filesFrom, []byte(entry+"\ndir1\n"), 0644); err != nil {
				t.Fatalf("write files-from: %s", err)
			}

			src, _ := object.CreateStorage("file", srcDir+"/", "", "", "")
			if err := src.Put(ctx, "dir1/file1", bytes.NewReader([]byte("test"))); err != nil {
				t.Fatalf("put file: %s", err)
			}
			if err := src.(object.SupportSymlink).Symlink("dir1", "dir-link"); err != nil {
				t.Fatalf("symlink dir-link: %s", err)
			}

			dst, _ := object.CreateStorage("file", dstDir+"/", "", "", "")
			if err := Sync(src, dst, &Config{
				Threads:     2,
				ListThreads: 1,
				Links:       true,
				Quiet:       true,
				FilesFrom:   filesFrom,
				Limit:       -1,
				MaxSize:     math.MaxInt64,
			}); err != nil {
				t.Fatalf("sync: %s", err)
			}

			target, err := os.Readlink(dstDir + "/dir-link")
			if err != nil {
				t.Fatalf("readlink dir-link: %s", err)
			}
			if target != "dir1" {
				t.Fatalf("dir-link target = %q, want %q", target, "dir1")
			}
			if _, err := dst.Head(ctx, "dir1/file1"); err != nil {
				t.Fatalf("head dir1/file1: %s", err)
			}
		})
	}
}

func TestSyncLinkWithOutFollow(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"

	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	a.Put(ctx, "a1", bytes.NewReader([]byte("test")))
	as := a.(object.SupportSymlink)
	as.Symlink(tmpA+"a1", "l1")
	as.Symlink("./../notExist", "l3")

	b, _ := object.CreateStorage("file", tmpB, "", "", "")

	if err := Sync(a, b, &Config{
		Threads:     50,
		ListThreads: 1,
		Update:      true,
		Perms:       true,
		Quiet:       true,
		ForceUpdate: true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
	}); err != nil {
		t.Fatalf("sync: %s", err)
	}
	content, err := b.Get(ctx, "l1", 0, -1)
	if err != nil {
		t.Fatalf("get content error: %s", err)
	}
	if c, err := io.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content error: %s", err)
	}

	if lstat, err := os.Lstat(tmpB + "l1"); err != nil && lstat.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("should follow link")
	}
	if _, err := os.Stat(tmpB + "l3"); !os.IsNotExist(err) {
		t.Fatalf("should not copy broken link")
	}
}

func TestSingleLink(t *testing.T) {
	tmpDir := t.TempDir()
	tmpA := tmpDir + "/a"
	tmpB := tmpDir + "/b"
	tmpTarget := tmpDir + "/aa"
	_ = os.Symlink(tmpTarget, tmpA)
	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	b, _ := object.CreateStorage("file", tmpB, "", "", "")
	if err := Sync(a, b, &Config{
		Threads:     50,
		ListThreads: 1,
		Update:      true,
		Perms:       true,
		Links:       true,
		Quiet:       true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		ForceUpdate: true,
	}); err != nil {
		t.Fatalf("sync: %s", err)
	}
	readlink, _ := os.Readlink(tmpA)
	readlink2, err := os.Readlink(tmpB)
	if err != nil {
		t.Fatalf("sync err: %v", err)
	}

	if readlink != readlink2 || readlink != tmpTarget {
		t.Fatalf("sync link failed")
	}
}

func TestSyncCheckAllLink(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"

	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	a.Put(ctx, "a1", bytes.NewReader([]byte("test")))
	as := a.(object.SupportSymlink)
	as.Symlink(tmpA+"a1", "l1")

	b, _ := object.CreateStorage("file", tmpB, "", "", "")
	bs := b.(object.SupportSymlink)
	bs.Symlink(tmpB+"a1", "l1")

	if err := Sync(a, b, &Config{
		Threads:     50,
		Perms:       true,
		Links:       true,
		Quiet:       true,
		ListThreads: 1,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		CheckAll:    true,
	}); err != nil {
		t.Fatalf("sync: %s", err)
	}

	l1, err := bs.Readlink("l1")
	if err != nil || l1 != tmpA+"a1" {
		t.Fatalf("readlink: %s content: %s", err, l1)
	}
	content, err := b.Get(ctx, "l1", 0, -1)
	if err != nil {
		t.Fatalf("get content failed: %s", err)
	}
	if c, err := io.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content failed: err %s content %s", err, string(c))
	}
}

func TestSyncCheckNewLink(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"

	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	a.Put(ctx, "a1", bytes.NewReader([]byte("test")))
	as := a.(object.SupportSymlink)
	as.Symlink(tmpA+"a1", "l1")

	b, _ := object.CreateStorage("file", tmpB, "", "", "")
	bs := b.(object.SupportSymlink)

	if err := Sync(a, b, &Config{
		Threads:     50,
		Perms:       true,
		Links:       true,
		Quiet:       true,
		ListThreads: 1,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		CheckNew:    true,
	}); err != nil {
		t.Fatalf("sync: %s", err)
	}

	l1, err := bs.Readlink("l1")
	if err != nil || l1 != tmpA+"a1" {
		t.Fatalf("readlink: %s content: %s", err, l1)
	}
	content, err := b.Get(ctx, "l1", 0, -1)
	if err != nil {
		t.Fatalf("get content failed: %s", err)
	}
	if c, err := io.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content failed: err %s content %s", err, string(c))
	}
}

func TestLimits(t *testing.T) {
	tmpA := t.TempDir() + "/"
	tmpB := t.TempDir() + "/"
	tmpC := t.TempDir() + "/"
	a, _ := object.CreateStorage("file", tmpA, "", "", "")
	b, _ := object.CreateStorage("file", tmpB, "", "", "")
	c, _ := object.CreateStorage("file", tmpC, "", "", "")
	put := func(storage object.ObjectStorage, keys []string) {
		for _, key := range keys {
			if key != "" {
				_ = storage.Put(ctx, key, bytes.NewReader([]byte{}))
			}
		}
	}
	commonKeys := []string{"", "a1", "a2", "a3", "a4", "a5", "a6"}
	put(a, commonKeys)
	put(c, []string{"c1", "c2", "c3"})
	type subConfig struct {
		dst          object.ObjectStorage
		limit        int64
		deleteDst    bool
		expectedKeys []string
	}
	testCases := []subConfig{
		{b, 2, false, []string{"", "a1", "a2"}},
		{b, -1, false, commonKeys},
		{b, 0, false, commonKeys},
		{c, 7, true, append(commonKeys, "c2", "c3")},
	}
	config := &Config{
		Threads:     50,
		Update:      true,
		Perms:       true,
		MaxSize:     math.MaxInt64,
		ListThreads: 1,
	}
	setConfig := func(config *Config, subC subConfig) {
		config.Limit = subC.limit
		config.DeleteDst = subC.deleteDst
	}

	for _, tcase := range testCases {
		setConfig(config, tcase)
		if err := Sync(a, tcase.dst, config); err != nil {
			t.Fatalf("sync: %s", err)
		}

		all, err := ListAll(tcase.dst, "", "", "", true)
		if err != nil {
			t.Fatalf("list all b: %s", err)
		}

		err = testKeysEqual(all, tcase.expectedKeys)
		if err != nil {
			t.Fatalf("testKeysEqual fail: %s", err)
		}
	}
}

// Regression test for the shared --limit budget between source processing and
// destination-extra deletion. The budget is consumed by both deletions and
// synced source objects; a source object is only counted in total after it
// passes the limit check, so the run must not report it as "lost".
func TestSyncLimitDeleteDstBoundary(t *testing.T) {
	tmpSrc := t.TempDir() + "/"
	tmpDst := t.TempDir() + "/"
	src, _ := object.CreateStorage("file", tmpSrc, "", "", "")
	dst, _ := object.CreateStorage("file", tmpDst, "", "", "")

	// Source has a single new file "a2" that does not exist on the destination.
	if err := src.Put(ctx, "a2", bytes.NewReader([]byte{})); err != nil {
		t.Fatalf("put src a2: %s", err)
	}
	// Destination has a single extra file "a1" (sorts before "a2") only on dst.
	if err := dst.Put(ctx, "a1", bytes.NewReader([]byte{})); err != nil {
		t.Fatalf("put dst a1: %s", err)
	}

	// With --limit 2 the budget is exactly enough to delete "a1" and copy "a2":
	// deleting "a1" consumes one unit and syncing "a2" consumes the last one.
	// The current source object "a2" must still be synced rather than dropped.
	config := &Config{
		Threads:     50,
		Update:      true,
		Perms:       true,
		MaxSize:     math.MaxInt64,
		ListThreads: 1,
		DeleteDst:   true,
		Limit:       2,
	}
	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("sync: %s", err)
	}

	all, err := ListAll(dst, "", "", "", true)
	if err != nil {
		t.Fatalf("list all dst: %s", err)
	}
	if err := testKeysEqual(all, []string{"", "a2"}); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}
}

// Regression test: while deleting an extra dst object consumes part of the
// --limit budget, the producer must still scan dstkeys to locate the current
// source object's matching dst. Otherwise the object is treated as missing on
// dst and gets copied/overwritten, breaking --ignore-existing (and similarly
// --existing/--update).
func TestSyncLimitDeleteDstIgnoreExisting(t *testing.T) {
	tmpSrc := t.TempDir() + "/"
	tmpDst := t.TempDir() + "/"
	src, _ := object.CreateStorage("file", tmpSrc, "", "", "")
	dst, _ := object.CreateStorage("file", tmpDst, "", "", "")

	// Source has "a2" with new content.
	if err := src.Put(ctx, "a2", bytes.NewReader([]byte("new"))); err != nil {
		t.Fatalf("put src a2: %s", err)
	}
	// Destination has an extra "a1" (sorts before "a2") and an existing "a2"
	// with different content that --ignore-existing must preserve.
	if err := dst.Put(ctx, "a1", bytes.NewReader([]byte{})); err != nil {
		t.Fatalf("put dst a1: %s", err)
	}
	if err := dst.Put(ctx, "a2", bytes.NewReader([]byte("old"))); err != nil {
		t.Fatalf("put dst a2: %s", err)
	}

	// With --limit 2, deleting "a1" consumes one unit and locating/skipping the
	// matching "a2" consumes the last one. The current source object "a2"
	// already exists on dst, so --ignore-existing must skip it rather than
	// overwrite it.
	config := &Config{
		Threads:        50,
		Perms:          true,
		MaxSize:        math.MaxInt64,
		ListThreads:    1,
		DeleteDst:      true,
		IgnoreExisting: true,
		Limit:          2,
	}
	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("sync: %s", err)
	}

	all, err := ListAll(dst, "", "", "", true)
	if err != nil {
		t.Fatalf("list all dst: %s", err)
	}
	if err := testKeysEqual(all, []string{"", "a2"}); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}
	c, err := dst.Get(ctx, "a2", 0, -1)
	if err != nil {
		t.Fatalf("get dst a2: %s", err)
	}
	data, _ := io.ReadAll(c)
	if string(data) != "old" {
		t.Fatalf("a2 should be preserved by --ignore-existing, got %q", string(data))
	}
}

// Regression test for the "leftover dst" branch: a dst object retained from a
// previous source iteration is deleted as extra for the current source object,
// consuming part of the --limit budget. The producer must still scan dstkeys to
// find the current object's matching dst instead of treating it as missing, so
// that --ignore-existing is honored (the same applies to --existing/--update).
func TestSyncLimitDeleteDstLeftoverIgnoreExisting(t *testing.T) {
	tmpSrc := t.TempDir() + "/"
	tmpDst := t.TempDir() + "/"
	src, _ := object.CreateStorage("file", tmpSrc, "", "", "")
	dst, _ := object.CreateStorage("file", tmpDst, "", "", "")

	// Source: "a1" (new on dst) and "a4" (also present on dst).
	if err := src.Put(ctx, "a1", bytes.NewReader([]byte("a1"))); err != nil {
		t.Fatalf("put src a1: %s", err)
	}
	if err := src.Put(ctx, "a4", bytes.NewReader([]byte("new"))); err != nil {
		t.Fatalf("put src a4: %s", err)
	}
	// Destination: extra "a2" (sorts between a1 and a4) and existing "a4".
	if err := dst.Put(ctx, "a2", bytes.NewReader([]byte{})); err != nil {
		t.Fatalf("put dst a2: %s", err)
	}
	if err := dst.Put(ctx, "a4", bytes.NewReader([]byte("old"))); err != nil {
		t.Fatalf("put dst a4: %s", err)
	}

	// Processing "a1" reads and retains dst "a2" (a2 > a1). When processing
	// "a4", the retained "a2" is deleted as extra, which consumes one unit of
	// the shared budget (limit 3 = copy a1 + delete a2 + sync a4). The matching
	// dst "a4" must still be located so --ignore-existing skips it instead of
	// overwriting.
	config := &Config{
		Threads:        50,
		Perms:          true,
		MaxSize:        math.MaxInt64,
		ListThreads:    1,
		DeleteDst:      true,
		IgnoreExisting: true,
		Limit:          3,
	}
	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("sync: %s", err)
	}

	all, err := ListAll(dst, "", "", "", true)
	if err != nil {
		t.Fatalf("list all dst: %s", err)
	}
	if err := testKeysEqual(all, []string{"", "a1", "a4"}); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}
	c, err := dst.Get(ctx, "a4", 0, -1)
	if err != nil {
		t.Fatalf("get dst a4: %s", err)
	}
	data, _ := io.ReadAll(c)
	if string(data) != "old" {
		t.Fatalf("a4 should be preserved by --ignore-existing, got %q", string(data))
	}
}

func testKeysEqual(objsCh <-chan object.Object, expectedKeys []string) error {
	var gottenKeys []string
	for obj := range objsCh {
		gottenKeys = append(gottenKeys, obj.Key())
	}
	if len(gottenKeys) != len(expectedKeys) {
		return fmt.Errorf("expected {%s}, got {%s}", strings.Join(expectedKeys, ", "),
			strings.Join(gottenKeys, ", "))
	}

	for idx, key := range gottenKeys {
		if key != expectedKeys[idx] {
			return fmt.Errorf("expected {%s}, got {%s}", strings.Join(expectedKeys, ", "),
				strings.Join(gottenKeys, ", "))
		}
	}
	return nil
}

func TestMatchObjects(t *testing.T) {
	type tcase struct {
		rules []rule
		key   string
		want  bool
	}
	tests := []tcase{
		{rules: []rule{{pattern: "a*"}}, key: "a1"},
		{rules: []rule{{pattern: "a*/b*"}}, key: "a1/b1"},
		{rules: []rule{{pattern: "/a*"}}, key: "/a1"},
		{rules: []rule{{pattern: "/a"}}, key: "/a1", want: true},
		{rules: []rule{{pattern: "/a/b/c"}}, key: "/a1", want: true},
		{rules: []rule{{pattern: "a*/b?"}}, key: "a1/b1/c2/d1"},
		{rules: []rule{{pattern: "a*/b?/"}}, key: "a1/", want: true},
		{rules: []rule{{pattern: "a*/b?/c.txt"}}, key: "a1/b1", want: true},
		{rules: []rule{{pattern: "a*/b?/"}}, key: "a1/b1/"},
		{rules: []rule{{pattern: "a*/b?/"}}, key: "a1/b1/c.txt"},
		{rules: []rule{{pattern: "a*/"}}, key: "a1/b1"},
		{rules: []rule{{pattern: "a*/b*/"}}, key: "a1/b1/c1/d.txt/"},
		{rules: []rule{{pattern: "/a*/b*"}}, key: "/a1/b1/c1/d.txt/"},
		{rules: []rule{{pattern: "a*/b*/c"}}, key: "a1/b1/c1/d.txt/", want: true},
		{rules: []rule{{pattern: "a"}}, key: "a/b/c/d/"},
		{rules: []rule{{pattern: "a.go", include: true}, {pattern: "pkg"}}, key: "a/pkg/c/a.go"},
		{rules: []rule{{pattern: "a"}, {pattern: "pkg", include: true}}, key: "a/pkg/c/a.go"},
		{rules: []rule{{pattern: "a.go", include: true}, {pattern: "pkg"}}, key: "", want: true},
		{rules: []rule{{pattern: "a", include: true}, {pattern: "b/"}, {pattern: "c", include: true}}, key: "a/b/c"},
		{rules: []rule{{pattern: "a/", include: true}, {pattern: "a"}}, key: "a/b", want: true},
		{rules: []rule{{pattern: "/***"}}, key: "a"},
		{rules: []rule{{pattern: "/***"}}, key: "a/b"},
		{rules: []rule{{pattern: "/a/***"}}, key: "a/"},
		{rules: []rule{{pattern: "/a/***"}}, key: "a/b"},
		{rules: []rule{{pattern: "/a/***"}}, key: "a/b/c"},
		{rules: []rule{{pattern: "/a/***"}}, key: "b/a/", want: true},
		{rules: []rule{{pattern: "a/***"}}, key: "a/"},
		{rules: []rule{{pattern: "a/***"}}, key: "a/b"},
		{rules: []rule{{pattern: "a/***"}}, key: "a/b/c"},
		{rules: []rule{{pattern: "a/***"}}, key: "d/a/b/c"},
		{rules: []rule{{pattern: "a/***"}}, key: "a", want: true},
		{rules: []rule{{pattern: "a/***"}}, key: "ba", want: true},
		{rules: []rule{{pattern: "a/***"}}, key: "ba/", want: true},
		{rules: []rule{{pattern: "*/a/***"}}, key: "/a/"},
		{rules: []rule{{pattern: "*/a/***"}}, key: "b/a/"},
		{rules: []rule{{pattern: "*/a/***"}}, key: "b/a/c"},
		{rules: []rule{{pattern: "/*/a/***"}}, key: "/b/a/"},
		{rules: []rule{{pattern: "/*/a/***"}}, key: "/b/a/c"},
		{rules: []rule{{pattern: "/*/a/***"}}, key: "c/b/a/", want: true},
		{rules: []rule{{pattern: "a/**/b"}}, key: "a/c/b"},
		{rules: []rule{{pattern: "a/**/b"}}, key: "a/c/d/b"},
		{rules: []rule{{pattern: "a/**/b"}}, key: "a/c/d/e/b"},
		{rules: []rule{{pattern: "/**/b"}}, key: "a/c/b"},
		{rules: []rule{{pattern: "/**/b"}}, key: "a/c/d/b/"},
		{rules: []rule{{pattern: "a**/b"}}, key: "a/c/d/b/"},
		{rules: []rule{{pattern: "a**/b"}}, key: "a/c/d/ab/", want: true},
		{rules: []rule{{pattern: "a**b"}}, key: "a/c/d/b/"},
		{rules: []rule{{pattern: "a**b"}}, key: "b/c/d/b/", want: true},
		{rules: []rule{{pattern: "a?**"}}, key: "a/a", want: true},
		{rules: []rule{{pattern: "**a"}}, key: "a"},
		{rules: []rule{{pattern: "a**"}}, key: "a"},
		{rules: []rule{{pattern: "a**a"}}, key: "a", want: true},
		{rules: []rule{{pattern: "aa**a"}}, key: "aa", want: true},
		{rules: []rule{{pattern: "**/d2/**a"}}, key: "/d2/d3/1a"},
		{rules: []rule{{pattern: "**/d2/**a"}}, key: "d2/d3/1a"},
		{rules: []rule{{pattern: "a/**/a"}}, key: "a", want: true},
		{rules: []rule{{pattern: "a/**/a"}}, key: "a/", want: true},
		{rules: []rule{{pattern: "**aa**", include: true}, {pattern: "a"}}, key: "aa/a", want: true},
	}
	for _, c := range tests {
		if got := matchLeveledPath(c.rules, c.key); got != c.want {
			t.Errorf("matchKey(%+v, %s) = %v, want %v", c.rules, c.key, got, c.want)
		}
	}
}

func TestMatchFullPatch(t *testing.T) {
	type tcase struct {
		rules []rule
		key   string
	}
	matchedCases := []tcase{
		{rules: []rule{{pattern: "a"}}, key: "b/a"},
		{rules: []rule{{pattern: "a*"}}, key: "a1"},
		{rules: []rule{{pattern: "a*/b*"}}, key: "a1/b1"},
		{rules: []rule{{pattern: "/a*"}}, key: "/a1"},
		{rules: []rule{{pattern: "a*/b?/"}}, key: "a1/b1/"},
		{rules: []rule{{pattern: "a/**/b"}}, key: "a/c/b"},
		{rules: []rule{{pattern: "a/**/b"}}, key: "a/c/d/b"},
		{rules: []rule{{pattern: "a/**/b"}}, key: "a/c/d/e/b"},
		{rules: []rule{{pattern: "/**/b"}}, key: "a/c/b"},
		{rules: []rule{{pattern: "a**/b"}}, key: "a/c/d/b"},
		{rules: []rule{{pattern: "a**b"}}, key: "a/c/d/b"},
		{rules: []rule{{pattern: "**a"}}, key: "a"},
		{rules: []rule{{pattern: "a**"}}, key: "a"},
		{rules: []rule{{pattern: "**/d2/**a"}}, key: "/d2/d3/1a"},
		{rules: []rule{{pattern: "**/d2/**a"}}, key: "d2/d3/1a"},
	}
	for _, c := range matchedCases {
		if got := matchFullPath(c.rules, c.key); got != false {
			t.Errorf("matchKey(%+v, %s) = %v, want %v", c.rules, c.key, got, false)
		}
	}
	unmatchedCases := []tcase{
		{rules: []rule{{pattern: "/a"}}, key: "/a1"},
		{rules: []rule{{pattern: "a*/b?"}}, key: "a1/b1/c2/d1"},
		{rules: []rule{{pattern: "/a/b/c"}}, key: "/a1"},
		{rules: []rule{{pattern: "a*/b?/"}}, key: "a1/"},
		{rules: []rule{{pattern: "a*/b?/c.txt"}}, key: "a1/b1"},
		{rules: []rule{{pattern: "a*/b?/"}}, key: "a1/b1/c.txt"},
		{rules: []rule{{pattern: "a*/"}}, key: "a1/b1"},
		{rules: []rule{{pattern: "a*/b*/"}}, key: "a1/b1/c1/d.txt/"},
		{rules: []rule{{pattern: "/a*/b*"}}, key: "/a1/b1/c1/d.txt/"},
		{rules: []rule{{pattern: "a"}}, key: "a/b/c/d/"},
		{rules: []rule{{pattern: "a*/b*/c"}}, key: "a1/b1/c1/d.txt/"},
		{rules: []rule{{pattern: "a**/b"}}, key: "a/c/d/ab/"},
		{rules: []rule{{pattern: "a**b"}}, key: "b/c/d/b"},
		{rules: []rule{{pattern: "/**/b"}}, key: "a/c/d/b/"},
		{rules: []rule{{pattern: "a?**"}}, key: "a/a"},
		{rules: []rule{{pattern: "a**a"}}, key: "a"},
		{rules: []rule{{pattern: "aa**a"}}, key: "aa"},
		{rules: []rule{{pattern: "a/**/a"}}, key: "a"},
		{rules: []rule{{pattern: "a/**/a"}}, key: "a/"},
		{rules: []rule{{pattern: "**aa**", include: true}, {pattern: "a"}}, key: "aa/a"},
	}
	for _, c := range unmatchedCases {
		if got := matchFullPath(c.rules, c.key); got != true {
			t.Errorf("matchKey(%+v, %s) = %v, want %v", c.rules, c.key, got, true)
		}
	}
}

func TestParseFilterRule(t *testing.T) {
	type tcase struct {
		args  []string
		rules []rule
	}
	cases := []tcase{
		{[]string{"--include", "a"}, []rule{{pattern: "a", include: true}}},
		{[]string{"--exclude", "a", "--include", "b"}, []rule{{pattern: "a"}, {pattern: "b", include: true}}},
		{[]string{"--include", "a", "--test", "t", "--exclude", "b"}, []rule{{pattern: "a", include: true}, {pattern: "b"}}},
		{[]string{"--include=a", "--test", "t", "--exclude"}, []rule{{pattern: "a", include: true}}},
		{[]string{"--include", "a", "--test", "t", "--exclude"}, []rule{{pattern: "a", include: true}}},
		{[]string{"-include=", "a", "--test", "t", "--exclude=*"}, []rule{{pattern: "*"}}},
	}

	for _, c := range cases {
		if got := parseIncludeRules(c.args); !reflect.DeepEqual(got, c.rules) {
			t.Errorf("parseIncludeRules(%+v) = %v, want %v", c.args, got, c.rules)
		}
	}
}

type mockObject struct {
	size  int64
	mtime time.Time
}

func (o *mockObject) Key() string          { return "" }
func (o *mockObject) IsDir() bool          { return false }
func (o *mockObject) IsSymlink() bool      { return false }
func (o *mockObject) Size() int64          { return o.size }
func (o *mockObject) Mtime() time.Time     { return o.mtime }
func (o *mockObject) StorageClass() string { return "" }
func (o *mockObject) Status() string       { return "" }

func TestFilterSizeAndAge(t *testing.T) {
	config := &Config{
		MaxSize: 100,
		MinSize: 10,
		MaxAge:  time.Second * 100,
		MinAge:  time.Second * 10,
	}
	now := time.Now()
	if !filterKey(&mockObject{10, now.Add(-time.Second * 15)}, now, nil, config) {
		t.Fatalf("filterKey failed")
	}
	if filterKey(&mockObject{200, now.Add(-time.Second * 200)}, now, nil, config) {
		t.Fatalf("filterKey should fail")
	}

	config = &Config{
		MaxSize:   math.MaxInt64,
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now().Add(-time.Minute),
	}
	if !filterKey(&mockObject{200, now.Add(-time.Minute * 30)}, now, nil, config) {
		t.Fatalf("filterKey fail")
	}

	if filterKey(&mockObject{200, now.Add(-time.Hour * 2)}, now, nil, config) {
		t.Fatalf("filterKey should fail")
	}
}

// nolint:errcheck
func TestSyncEncrypt(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %s", err)
	}
	kc := object.NewRSAEncryptor(rsaKey)
	enc, err := object.NewDataEncryptor(kc, object.AES256GCM_RSA)
	if err != nil {
		t.Fatalf("create encryptor: %s", err)
	}

	// sync plaintext src -> encrypted dst
	src, _ := object.CreateStorage("mem", "", "", "", "")
	dst, _ := object.CreateStorage("mem", "", "", "", "")

	testData := map[string]string{
		"file1.txt":     "hello world",
		"dir/file2.txt": "foo bar baz",
		"empty.txt":     "x",
	}
	for k, v := range testData {
		src.Put(ctx, k, bytes.NewReader([]byte(v)))
	}

	encDst := object.NewChunkedEncrypted(dst, enc)
	if err := Sync(src, encDst, &Config{
		Threads:     10,
		ListThreads: 1,
		Update:      true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		Quiet:       true,
	}); err != nil {
		t.Fatalf("sync to encrypted dst: %s", err)
	}

	// Verify dst has encrypted data (raw read should differ from plaintext)
	for k, v := range testData {
		r, err := dst.Get(ctx, k, 0, -1)
		if err != nil {
			t.Fatalf("get raw %s: %s", k, err)
		}
		raw, _ := io.ReadAll(r)
		if string(raw) == v {
			t.Fatalf("data for %s should be encrypted but got plaintext", k)
		}
	}

	// sync encrypted src -> plaintext dst (decrypt)
	dst2, _ := object.CreateStorage("mem", "", "", "", "")
	encSrc := object.NewChunkedEncrypted(dst, enc)
	if err := Sync(encSrc, dst2, &Config{
		Threads:     10,
		ListThreads: 1,
		Update:      true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		Quiet:       true,
	}); err != nil {
		t.Fatalf("sync from encrypted src: %s", err)
	}

	// Verify dst2 has original plaintext
	for k, v := range testData {
		r, err := dst2.Get(ctx, k, 0, -1)
		if err != nil {
			t.Fatalf("get decrypted %s: %s", k, err)
		}
		data, _ := io.ReadAll(r)
		if string(data) != v {
			t.Fatalf("decrypted %s: got %q, want %q", k, string(data), v)
		}
	}

	// decrypt with wrong key should fail
	rsaKey2, _ := rsa.GenerateKey(rand.Reader, 2048)
	kc2 := object.NewRSAEncryptor(rsaKey2)
	enc2, _ := object.NewDataEncryptor(kc2, object.AES256GCM_RSA)
	wrongSrc := object.NewChunkedEncrypted(dst, enc2)
	dst3, _ := object.CreateStorage("mem", "", "", "", "")
	err = Sync(wrongSrc, dst3, &Config{
		Threads:     10,
		ListThreads: 1,
		Update:      true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		Quiet:       true,
	})
	// Sync should complete but with failures (wrong key can't decrypt)
	ch, _ := ListAll(dst3, "", "", "", true)
	var count int
	for range ch {
		count++
	}
	if count == len(testData) {
		t.Fatalf("decrypting with wrong key should not produce all files")
	}
}

// nolint:errcheck
func TestSyncEncryptLargeFile(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %s", err)
	}
	kc := object.NewRSAEncryptor(rsaKey)
	enc, err := object.NewDataEncryptor(kc, object.AES256GCM_RSA)
	if err != nil {
		t.Fatalf("create encryptor: %s", err)
	}

	src, _ := object.CreateStorage("mem", "", "", "", "")
	dst, _ := object.CreateStorage("mem", "", "", "", "")

	// Create a large file that spans multiple chunks (>8 MiB)
	largeData := make([]byte, 9<<20) // 9 MiB
	for i := range largeData {
		largeData[i] = byte(i % 253)
	}
	src.Put(ctx, "large.bin", bytes.NewReader(largeData))

	encDst := object.NewChunkedEncrypted(dst, enc)
	if err := Sync(src, encDst, &Config{
		Threads:     10,
		ListThreads: 1,
		Update:      true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		Quiet:       true,
	}); err != nil {
		t.Fatalf("sync large file to encrypted dst: %s", err)
	}

	// Verify encrypted data differs from plaintext
	r, err := dst.Get(ctx, "large.bin", 0, -1)
	if err != nil {
		t.Fatalf("get raw large.bin: %s", err)
	}
	raw, _ := io.ReadAll(r)
	if bytes.Equal(raw, largeData) {
		t.Fatalf("large file should be encrypted")
	}

	// Decrypt back
	dst2, _ := object.CreateStorage("mem", "", "", "", "")
	encSrc := object.NewChunkedEncrypted(dst, enc)
	if err := Sync(encSrc, dst2, &Config{
		Threads:     10,
		ListThreads: 1,
		Update:      true,
		Limit:       -1,
		MaxSize:     math.MaxInt64,
		Quiet:       true,
	}); err != nil {
		t.Fatalf("sync large file from encrypted src: %s", err)
	}

	r, err = dst2.Get(ctx, "large.bin", 0, -1)
	if err != nil {
		t.Fatalf("get decrypted large.bin: %s", err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, largeData) {
		t.Fatalf("decrypted large file mismatch: got %d bytes, want %d", len(got), len(largeData))
	}
}
