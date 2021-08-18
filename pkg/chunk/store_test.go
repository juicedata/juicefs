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
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/sirupsen/logrus"
)

func testStore(t *testing.T, store ChunkStore) {
	utils.SetLogLevel(logrus.DebugLevel)
	writer := store.NewWriter(1)
	data := []byte("hello world")
	if n, err := writer.WriteAt(data, 0); n != 11 || err != nil {
		t.Fatalf("write fail: %d %s", n, err)
	}
	offset := defaultConf.BlockSize - 3
	if n, err := writer.WriteAt(data, int64(offset)); err != nil || n != 11 {
		t.Fatalf("write fail: %d %s", n, err)
	}
	if err := writer.FlushTo(defaultConf.BlockSize + 3); err != nil {
		t.Fatalf("flush fail: %s", err)
	}
	size := offset + len(data)
	if err := writer.Finish(size); err != nil {
		t.Fatalf("finish fail: %s", err)
	}
	// nolint:errcheck
	defer store.Remove(1, size)

	reader := store.NewReader(1, size)
	p := NewPage(make([]byte, 5))
	if n, err := reader.ReadAt(context.Background(), p, 6); n != 5 || err != nil {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "world" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}
	p = NewPage(make([]byte, 20))
	if n, err := reader.ReadAt(context.Background(), p, offset); n != 11 || err != nil && err != io.EOF {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "hello world" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}
}

func TestDiskStore(t *testing.T) {
	testStore(t, NewDiskStore("/tmp/diskStore"))
}

var defaultConf = Config{
	BlockSize:  1024,
	CacheDir:   "/tmp/diskCache",
	CacheSize:  10,
	MaxUpload:  1,
	PutTimeout: time.Second,
	GetTimeout: time.Second * 2,
}

func TestCachedStore(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "")
	store := NewCachedStore(mem, defaultConf)
	testStore(t, store)
}

func TestUncompressedStore(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "")
	conf := defaultConf
	conf.Compress = ""
	conf.CacheSize = 0
	store := NewCachedStore(mem, conf)
	testStore(t, store)
}

// nolint:errcheck
func TestAsyncStore(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "")
	conf := defaultConf
	conf.CacheDir = "/tmp/testdirAsync"
	p := filepath.Join(conf.CacheDir, stagingDir, "chunks/0/0/123_0_4")
	os.MkdirAll(filepath.Dir(p), 0744)
	f, _ := os.Create(p)
	f.WriteString("good")
	f.Close()
	conf.Writeback = true
	_ = NewCachedStore(mem, conf)
	time.Sleep(time.Millisecond * 50) // wait for scan to finish
	if _, err := mem.Get("chunks/0/0/123_0_4", 0, -1); err != nil {
		t.Fatalf("staging object should be upload")
	}
}
