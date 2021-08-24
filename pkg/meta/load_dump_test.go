/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

package meta

import (
	"os"
	"os/exec"
	"testing"
)

const sampleFile = "metadata.sample"

func testLoad(t *testing.T, uri, fname string) Meta {
	m := NewClient(uri, &Config{Retries: 10, Strict: true})
	ctx := Background
	switch r := m.(type) {
	case *redisMeta:
		r.rdb.FlushDB(ctx)
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
	if st := m.Readdir(ctx, 1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 6 {
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

func TestLoadDump(t *testing.T) {
	t.Run("Metadata Engine: Redis", func(t *testing.T) {
		m := testLoad(t, "redis://127.0.0.1/10", sampleFile)
		fp, err := os.OpenFile("redis.dump", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			t.Fatalf("open file: %s", "redis.dump")
		}
		defer fp.Close()
		if err = m.DumpMeta(fp); err != nil {
			t.Fatalf("dump meta: %s", err)
		}
	})
	t.Run("Metadata Engine: SQLite", func(t *testing.T) {
		os.Remove("test10.db")
		m := testLoad(t, "sqlite3://test10.db", sampleFile)
		fp, err := os.OpenFile("sqlite3.dump", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			t.Fatalf("open file: %s", "sqlite3.dump")
		}
		defer fp.Close()
		if err = m.DumpMeta(fp); err != nil {
			t.Fatalf("dump meta: %s", err)
		}
	})
	t.Run("Metadata Engine: TKV", func(t *testing.T) {
		os.Remove(settingPath)
		m := testLoad(t, "memkv://test/jfs", sampleFile)
		// m := testLoad(t, "tikv://127.0.0.1:2379/jfs", sampleFile)
		fp, err := os.OpenFile("tkv.dump", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			t.Fatalf("open file: %s", "tkv.dump")
		}
		defer fp.Close()
		if err = m.DumpMeta(fp); err != nil {
			t.Fatalf("dump meta: %s", err)
		}
	})

	cmd := exec.Command("diff", "redis.dump", "sqlite3.dump")
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("diff: %s", out)
	}
	cmd = exec.Command("diff", "redis.dump", "tkv.dump")
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("diff: %s", out)
	}
}
