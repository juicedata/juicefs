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
	"os"
	"os/exec"
	"path"
	"testing"
)

const sampleFile = "metadata.sample"
const subSampleFile = "metadata-sub.sample"

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
	} else if len(entries) != 8 {
		t.Fatalf("entries: %d", len(entries))
	}
	attr := &Attr{}
	if st := m.GetAttr(ctx, 2, attr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if attr.Nlink != 1 || attr.Length != 24 {
		t.Fatalf("nlink: %d, length: %d", attr.Nlink, attr.Length)
	}
	var chunks []Slice
	if st := m.Read(ctx, 2, 0, &chunks); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(chunks) != 1 || chunks[0].Chunkid != 4 || chunks[0].Size != 24 {
		t.Fatalf("chunks: %v", chunks)
	}
	if st := m.GetAttr(ctx, 4, attr); st != 0 || attr.Nlink != 2 { // hard link
		t.Fatalf("getattr: %s, %d", st, attr.Nlink)
	}
	var target []byte
	if st := m.ReadLink(ctx, 5, &target); st != 0 || string(target) != "d1/f11" { // symlink
		t.Fatalf("readlink: %s, %s", st, target)
	}
	var value []byte
	if st := m.GetXattr(ctx, 2, "k", &value); st != 0 || string(value) != "v" {
		t.Fatalf("getxattr: %s %v", st, value)
	}

	return m
}

func testDump(t *testing.T, m Meta, root Ino, expect, result string) {
	fp, err := os.OpenFile(result, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("open file %s: %s", result, err)
	}
	defer fp.Close()
	if err = m.DumpMeta(fp, root); err != nil {
		t.Fatalf("dump meta: %s", err)
	}
	cmd := exec.Command("diff", expect, result)
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("diff %s %s: %s", expect, result, out)
	}
}

func TestLoadDump(t *testing.T) {
	t.Run("Metadata Engine: Redis", func(t *testing.T) {
		m := testLoad(t, "redis://127.0.0.1/10", sampleFile)
		testDump(t, m, 0, sampleFile, "redis.dump")
	})
	t.Run("Metadata Engine: Redis; --SubDir d1 ", func(t *testing.T) {
		_ = testLoad(t, "redis://127.0.0.1/10", sampleFile)
		m := NewClient("redis://127.0.0.1/10", &Config{Retries: 10, Strict: true, Subdir: "d1"})
		testDump(t, m, 0, subSampleFile, "redis_subdir.dump")
		testDump(t, m, 1, sampleFile, "redis.dump")
	})

	sqluri := "sqlite3://" + path.Join(t.TempDir(), "jfs-load-dump-test.db")
	t.Run("Metadata Engine: SQLite", func(t *testing.T) {
		m := testLoad(t, sqluri, sampleFile)
		testDump(t, m, 0, sampleFile, "sqlite3.dump")
	})
	t.Run("Metadata Engine: SQLite --SubDir d1", func(t *testing.T) {
		_ = testLoad(t, sqluri, sampleFile)
		m := NewClient(sqluri, &Config{Retries: 10, Strict: true, Subdir: "d1"})
		testDump(t, m, 0, subSampleFile, "sqlite3_subdir.dump")
		testDump(t, m, 1, sampleFile, "sqlite3.dump")
	})

	t.Run("Metadata Engine: TKV", func(t *testing.T) {
		_ = os.Remove(settingPath)
		m := testLoad(t, "memkv://test/jfs", sampleFile)
		testDump(t, m, 0, sampleFile, "tkv.dump")
	})
	t.Run("Metadata Engine: TKV --SubDir d1 ", func(t *testing.T) {
		_ = os.Remove(settingPath)
		m := testLoad(t, "memkv://user:passwd@test/jfs", sampleFile)
		if kvm, ok := m.(*kvMeta); ok { // memkv will be empty if created again
			var err error
			if kvm.root, err = lookupSubdir(kvm, "d1"); err != nil {
				t.Fatalf("lookup subdir d1: %s", err)
			}
		}
		testDump(t, m, 0, subSampleFile, "tkv_subdir.dump")
		testDump(t, m, 1, sampleFile, "tkv.dump")
	})
}
