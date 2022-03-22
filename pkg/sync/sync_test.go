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
	"io/ioutil"
	"math"
	"os"
	"reflect"
	"testing"

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
	m, _ := object.CreateStorage("mem", "", "", "")
	m.Put("a", bytes.NewReader([]byte("a")))
	m.Put("b", bytes.NewReader([]byte("a")))
	m.Put("aa", bytes.NewReader([]byte("a")))
	m.Put("c", bytes.NewReader([]byte("a")))

	ch, _ := ListAll(m, "a", "b")
	keys := collectAll(ch)
	if len(keys) != 3 {
		t.Fatalf("length should be 3, but got %d", len(keys))
	}
	if !reflect.DeepEqual(keys, []string{"a", "aa", "b"}) {
		t.Fatalf("result wrong: %s", keys)
	}

	// Single object
	s, _ := object.CreateStorage("mem", "", "", "")
	s.Put("a", bytes.NewReader([]byte("a")))
	ch, _ = ListAll(s, "", "")
	keys = collectAll(ch)
	if !reflect.DeepEqual(keys, []string{"a"}) {
		t.Fatalf("result wrong: %s", keys)
	}
}

func TestIeratorSingleEmptyKey(t *testing.T) {
	// utils.SetLogLevel(logrus.DebugLevel)

	// Construct mem storage
	s, _ := object.CreateStorage("mem", "", "", "")
	err := s.Put("abc", bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("Put error: %q", err)
	}

	// Simulate command line prefix in SRC or DST
	s = object.WithPrefix(s, "abc")
	ch, _ := ListAll(s, "", "")
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
	defer func() {
		_ = os.RemoveAll("/tmp/a")
		_ = os.RemoveAll("/tmp/b")
	}()
	config := &Config{
		Start:     "",
		End:       "",
		Threads:   50,
		Update:    true,
		Perms:     true,
		Dry:       false,
		DeleteSrc: false,
		DeleteDst: false,
		Exclude:   []string{"c*"},
		Include:   []string{"a[1-9]", "a*"},
		Verbose:   false,
		Quiet:     true,
	}
	os.Args = []string{"--include", "a[1-9]", "--exclude", "a*", "--exclude", "c*"}
	a, _ := object.CreateStorage("file", "/tmp/a/", "", "")
	a.Put("a1", bytes.NewReader([]byte("a1")))
	a.Put("a2", bytes.NewReader([]byte("a2")))
	a.Put("abc", bytes.NewReader([]byte("abc")))
	a.Put("c1", bytes.NewReader([]byte("c1")))
	a.Put("c2", bytes.NewReader([]byte("c2")))

	b, _ := object.CreateStorage("file", "/tmp/b/", "", "")
	b.Put("a1", bytes.NewReader([]byte("a1")))
	b.Put("ba", bytes.NewReader([]byte("a1")))

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
	// No copy occured
	if c := copied.Current(); c != 0 {
		t.Fatalf("should copy 0 keys, but got %d", c)
	}

	// Now a: {"a1", "a2", "abc","c1","c2"}, b: {"a1", "ba"}
	// Copy "ba" from b to a
	os.Args = []string{}
	config.Exclude = nil
	if err := Sync(b, a, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	if c := copied.Current(); c != 1 {
		t.Fatalf("should copy 1 keys, but got %d", c)
	}
	// Now a: {"a1", "a2", "abc","c1","c2","ba"}, b: {"a1", "ba"}
	aRes, _ := a.ListAll("", "")
	bRes, _ := b.ListAll("", "")

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
		t.FailNow()
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
	defer func() {
		_ = os.RemoveAll("/tmp/a")
		_ = os.RemoveAll("/tmp/b")
	}()
	config := &Config{
		Start:     "",
		End:       "",
		Threads:   50,
		Update:    true,
		Perms:     true,
		Dry:       false,
		DeleteSrc: false,
		DeleteDst: false,
		Verbose:   false,
		Quiet:     true,
		Exclude:   []string{"1"},
	}
	a, _ := object.CreateStorage("file", "/tmp/a/", "", "")
	b, _ := object.CreateStorage("file", "/tmp/b/", "", "")

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
		_ = os.RemoveAll("/tmp/a/")
		_ = os.RemoveAll("/tmp/b/")
		os.Args = testCase.args
		for _, k := range testCase.srcKey {
			a.Put(k, bytes.NewReader([]byte(k)))
		}
		if err := Sync(a, b, config); err != nil {
			t.Fatalf("sync: %s", err)
		}

		bRes, _ := b.ListAll("", "")
		var bKeys []string
		for obj := range bRes {
			bKeys = append(bKeys, obj.Key())
		}
		if !reflect.DeepEqual(bKeys[1:], testCase.want) {
			t.Errorf("sync args  %v, want %v, but get %v", os.Args, testCase.want, bKeys)
		}
	}
}

func TestInitRules(t *testing.T) {
	tests := []struct {
		args      []string
		wantRules []*rule
	}{
		{
			args:      []string{"--include", "a"},
			wantRules: []*rule{{pattern: "a", include: true}},
		},
		{
			args:      []string{"--exclude", "a", "--include", "b"},
			wantRules: []*rule{{pattern: "a", include: false}, {pattern: "b", include: true}},
		},
		{
			args:      []string{"--include", "a", "--test", "t", "--exclude", "b"},
			wantRules: []*rule{{pattern: "a", include: true}, {pattern: "b", include: false}},
		},
		{
			args:      []string{"--include", "a", "--test", "t", "--exclude"},
			wantRules: []*rule{{pattern: "a", include: true}},
		},
		{
			args:      []string{"--include", "a", "--exclude", "b", "--include", "c", "--exclude", "d"},
			wantRules: []*rule{{pattern: "a", include: true}, {pattern: "b", include: false}, {pattern: "c", include: true}, {pattern: "d", include: false}},
		},
		{
			args:      []string{"--include", "a", "--include", "b", "--test", "--exclude", "c", "--exclude", "d"},
			wantRules: []*rule{{pattern: "a", include: true}, {pattern: "b", include: true}, {pattern: "c", include: false}, {pattern: "d", include: false}},
		},
		{
			args:      []string{"--include=a", "--include=b", "--exclude=c", "--exclude=d", "--test=aaa"},
			wantRules: []*rule{{pattern: "a", include: true}, {pattern: "b", include: true}, {pattern: "c", include: false}, {pattern: "d", include: false}},
		},
		{
			args:      []string{"-include=a", "--test", "t", "--include=b", "--exclude=c", "-exclude="},
			wantRules: []*rule{{pattern: "a", include: true}, {pattern: "b", include: true}, {pattern: "c", include: false}},
		},
	}
	a, _ := object.CreateStorage("file", "/tmp/a/", "", "")
	b, _ := object.CreateStorage("file", "/tmp/b/", "", "")
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			os.Args = tt.args
			if gotRules := initRules(a, b); !reflect.DeepEqual(gotRules, tt.wantRules) {
				t.Errorf("initRules() = %v, want %v", gotRules, tt.wantRules)
			}
		})
	}
}

func TestAlignPatternAndKey(t *testing.T) {
	tests := []struct {
		pattern string
		key     string
		wantKey string
	}{
		{pattern: "a*", key: "a1", wantKey: "a1"},
		{pattern: "a*/b*", key: "a1/b1", wantKey: "a1/b1"},
		{pattern: "/a*", key: "/a1", wantKey: "/a1"},
		{pattern: "a*/b?", key: "a1/b1/c2/d1", wantKey: "a1/b1"},
		{pattern: "a*/b?/", key: "a1/", wantKey: "a1/"},
		{pattern: "a*/b?/c.txt", key: "a1/b1", wantKey: "a1/b1"},
		{pattern: "a*/b?/", key: "a1/b1/", wantKey: "a1/b1/"},
		{pattern: "a*/b?/", key: "a1/b1/c.txt", wantKey: "a1/b1/"},
		{pattern: "a*/b*/", key: "a1/b1/c1/d.txt/", wantKey: "a1/b1/"},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			gotKey := alignPatternAndKey(tt.pattern, tt.key)
			if gotKey != tt.wantKey {
				t.Errorf("alignPatternAndKey() gotKey = %v, want %v", gotKey, tt.wantKey)
			}
		})
	}
}

func TestSyncLink(t *testing.T) {
	defer func() {
		_ = os.RemoveAll("/tmp/a")
		_ = os.RemoveAll("/tmp/b")
	}()

	a, _ := object.CreateStorage("file", "/tmp/a/", "", "")
	a.Put("a1", bytes.NewReader([]byte("test")))
	a.Put("d1/a.txt", bytes.NewReader([]byte("test")))
	as := a.(object.SupportSymlink)
	as.Symlink("/tmp/a/a1", "l1")
	as.Symlink("./../a1", "d1/l2")
	as.Symlink("./../notExist", "l3")

	b, _ := object.CreateStorage("file", "/tmp/b/", "", "")
	bs := b.(object.SupportSymlink)
	bs.Symlink("/tmp/b/a1", "l1")

	if err := Sync(a, b, &Config{
		Threads:     50,
		Update:      true,
		Perms:       true,
		Links:       true,
		Quiet:       true,
		ForceUpdate: true,
	}); err != nil {
		t.Fatalf("sync: %s", err)
	}

	l1, err := bs.Readlink("l1")
	if err != nil || l1 != "/tmp/a/a1" {
		t.Fatalf("readlink: %s content: %s", err, l1)
	}
	content, err := b.Get("l1", 0, -1)
	if err != nil {
		t.Fatalf("get content failed: %s", err)
	}
	if c, err := ioutil.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content failed: err %s content %s", err, string(c))
	}

	l2, err := bs.Readlink("d1/l2")
	if err != nil || l2 != "./../a1" {
		t.Fatalf("readlink: %s", err)
	}
	content, err = b.Get("d1/l2", 0, -1)
	if err != nil {
		t.Fatalf("content failed: %s", err)
	}
	if c, err := ioutil.ReadAll(content); err != nil || string(c) != "test" {
		t.Fatalf("read content failed: err %s content %s", err, string(c))
	}

	l3, err := bs.Readlink("l3")
	if err != nil || l3 != "./../notExist" {
		t.Fatalf("readlink: %s", err)
	}
}
