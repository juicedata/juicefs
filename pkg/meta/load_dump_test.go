/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"bytes"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const sampleFile = "metadata.sample"
const subSampleFile = "metadata-sub.sample"

func TestEscape(t *testing.T) {
	cases := []struct {
		value            []rune
		gbkStart, gbkEnd int
	}{
		{value: []rune("%1F果汁数据科技有限公司%2B"), gbkStart: 0, gbkEnd: 0},
		{value: []rune("果汁数据科技有限公司%1F"), gbkStart: 0, gbkEnd: 1},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 1, gbkEnd: 2},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 5, gbkEnd: 10},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 0, gbkEnd: 10},
		{value: []rune("GBK果汁数据科技有限公司文件"), gbkStart: 0, gbkEnd: 15},
		{value: []rune("%果汁数据科%技有限公司%"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("\"果汁数据科\"技有限公司%"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("\\果汁数\\据科技有限公司"), gbkStart: 1, gbkEnd: 4},
	}
	for _, c := range cases {
		var v []byte
		prefix := c.value[:c.gbkStart]
		middle := c.value[c.gbkStart:c.gbkEnd]
		suffix := c.value[c.gbkEnd:]
		gbk, err := Utf8ToGbk([]byte(string(middle)))
		if err != nil {
			t.Fatalf("Utf8ToGbk error: %v", err)
		}
		v = append(v, []byte(string(prefix))...)
		v = append(v, gbk...)
		v = append(v, []byte(string(suffix))...)
		s := escape(string(v))
		t.Log("escape value: ", s)
		r := unescape(s)
		if bytes.Compare(r, v) != 0 {
			t.Fatalf("expected %v, but got %v", v, r)
		}
	}
}

func Utf8ToGbk(s []byte) ([]byte, error) {
	reader := transform.NewReader(bytes.NewReader(s), simplifiedchinese.GBK.NewEncoder())
	d, e := ioutil.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	return d, nil
}

func GbkToUtf8(s []byte) ([]byte, error) {
	reader := transform.NewReader(bytes.NewReader(s), simplifiedchinese.GBK.NewDecoder())
	d, e := ioutil.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	return d, nil
}

func testLoad(t *testing.T, uri, fname string) Meta {
	m := NewClient(uri, &Config{Retries: 10, Strict: true})
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	fp, err := os.Open(fname)
	if err != nil {
		t.Fatalf("open file: %s", fname)
	}
	defer fp.Close()
	if err = m.LoadMeta(fp); err != nil {
		t.Fatalf("load meta: %s", err)
	}

	ctx := Background
	var entries []*Entry
	if st := m.Readdir(ctx, 1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 11 {
		t.Fatalf("entries: %d", len(entries))
	}
	for _, entry := range entries {
		fname := string(entry.Name)
		if strings.HasPrefix(fname, "GBK") {
			if utf8, err := GbkToUtf8(entry.Name); err != nil || string(utf8) != "GBK果汁数据科技有限公司文件" {
				t.Fatalf("load GBK file error: %s", string(utf8))
			}
		}
		if strings.HasPrefix(fname, "UTF8") && fname != "UTF8果汁数据科技有限公司目录" && fname != "UTF8果汁数据科技有限公司文件" {
			t.Fatalf("load entries error: %s", fname)
		}
	}
	attr := &Attr{}
	if st := m.GetAttr(ctx, 2, attr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if attr.Nlink != 1 || attr.Length != 24 {
		t.Fatalf("nlink: %d, length: %d", attr.Nlink, attr.Length)
	}
	var slices []Slice
	if st := m.Read(ctx, 2, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 1 || slices[0].Id != 4 || slices[0].Size != 24 {
		t.Fatalf("slices: %v", slices)
	}
	if st := m.GetAttr(ctx, 4, attr); st != 0 || attr.Nlink != 2 { // hard link
		t.Fatalf("getattr: %s, %d", st, attr.Nlink)
	}
	if ps := m.GetParents(ctx, 4); len(ps) != 2 || ps[1] != 1 || ps[3] != 1 {
		t.Fatalf("getparents: %+v != {1:1, 3:1}", ps)
	}
	var target []byte

	if st := m.ReadLink(ctx, 5, &target); st == 0 { // symlink
		if utf8, err := GbkToUtf8(target); err != nil || string(utf8) != "GBK果汁数据科技有限公司文件" {
			t.Fatalf("readlink: %s, %s", st, target)
		}
	} else {
		t.Fatalf("readlink: %s, %s", st, target)
	}

	var value []byte
	if st := m.GetXattr(ctx, 2, "k", &value); st != 0 || string(value) != "v" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
	if st := m.GetXattr(ctx, 3, "dk", &value); st != 0 || string(value) != "果汁" {
		t.Fatalf("getxattr: %s %v", st, value)
	}

	return m
}

func testLoadSub(t *testing.T, uri, fname string) {
	m := NewClient(uri, &Config{Retries: 10, Strict: true})
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	fp, err := os.Open(fname)
	if err != nil {
		t.Fatalf("open file: %s", fname)
	}
	defer fp.Close()
	if err = m.LoadMeta(fp); err != nil {
		t.Fatalf("load meta: %s", err)
	}

	var entries []*Entry
	if st := m.Readdir(Background, 1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 4 {
		t.Fatalf("entries: %d", len(entries))
	}
	for _, entry := range entries {
		fname := string(entry.Name)
		if fname != "." && fname != ".." && fname != "big" && fname != "f11" {
			t.Fatalf("invalid entry name: %s", fname)
		}
	}
}

func testDump(t *testing.T, m Meta, root Ino, expect, result string) {
	fp, err := os.OpenFile(result, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("open file %s: %s", result, err)
	}
	defer fp.Close()
	if _, err = m.Load(true); err != nil {
		t.Fatalf("load setting: %s", err)
	}
	if err = m.DumpMeta(fp, root, false); err != nil {
		t.Fatalf("dump meta: %s", err)
	}
	cmd := exec.Command("diff", expect, result)
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("diff %s %s: %s", expect, result, out)
	}
}

func testLoadDump(t *testing.T, name, addr string) {
	t.Run("Metadata Engine: "+name, func(t *testing.T) {
		m := testLoad(t, addr, sampleFile)
		testDump(t, m, 1, sampleFile, "test.dump")
		m.Shutdown()
		m = NewClient(addr, &Config{Retries: 10, Strict: true, Subdir: "d1"})
		testDump(t, m, 1, subSampleFile, "test_subdir.dump")
		testDump(t, m, 0, sampleFile, "test.dump")
		_ = m.Shutdown()
		testLoadSub(t, addr, subSampleFile)
	})
}

func TestLoadDump(t *testing.T) {
	testLoadDump(t, "redis", "redis://127.0.0.1/10")
	testLoadDump(t, "redis cluster", "redis://127.0.0.1:7001/10")
	testLoadDump(t, "sqlite", "sqlite3://"+path.Join(t.TempDir(), "jfs-load-dump-test.db"))
	testLoadDump(t, "mysql", "mysql://root:@/dev")
	testLoadDump(t, "postgres", "postgres://localhost:5432/test?sslmode=disable")
	testLoadDump(t, "badger", "badger://"+path.Join(t.TempDir(), "jfs-load-duimp-testdb"))
	testLoadDump(t, "etcd", "etcd://127.0.0.1:2379/jfs-load-dump")
	testLoadDump(t, "tikv", "tikv://127.0.0.1:2379/jfs-load-dump")
}

func TestLoadDump_MemKV(t *testing.T) {
	t.Run("Metadata Engine: memkv", func(t *testing.T) {
		_ = os.Remove(settingPath)
		m := testLoad(t, "memkv://test/jfs", sampleFile)
		testDump(t, m, 1, sampleFile, "test.dump")
	})
	t.Run("Metadata Engine: memkv; --SubDir d1 ", func(t *testing.T) {
		_ = os.Remove(settingPath)
		m := testLoad(t, "memkv://user:pass@test/jfs", sampleFile)
		if kvm, ok := m.(*kvMeta); ok { // memkv will be empty if created again
			var err error
			if kvm.root, err = lookupSubdir(kvm, "d1"); err != nil {
				t.Fatalf("lookup subdir d1: %s", err)
			}
		}
		testDump(t, m, 1, subSampleFile, "test_subdir.dump")
		testDump(t, m, 0, sampleFile, "test.dump")
		_ = os.Remove(settingPath)
		testLoadSub(t, "memkv://user:pass@test/jfs", subSampleFile)
	})
}
