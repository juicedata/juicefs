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

package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/redis/go-redis/v9"
)

func TestDumpAndLoad(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/15"
	opt, err := redis.ParseURL(metaUrl)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	rdb := redis.NewClient(opt)
	rdb.FlushDB(context.Background())

	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump_test.json.gz")
	dumpSubdirFile := filepath.Join(tmpDir, "dump_subdir_test.json")

	t.Run("Test Load", func(t *testing.T) {
		loadArgs := []string{"", "load", metaUrl, "./../pkg/meta/metadata.sample"}
		err = Main(loadArgs)
		if err != nil {
			t.Fatalf("load failed: %v", err)
		}
		if rdb.DBSize(context.Background()).Val() == 0 {
			t.Fatalf("load error: %v", err)
		}
	})
	t.Run("Test dump", func(t *testing.T) {
		dumpArgs := []string{"", "dump", metaUrl, dumpFile}
		err := Main(dumpArgs)
		if err != nil {
			t.Fatalf("dump error: %v", err)
		}
		_, err = os.Stat(dumpFile)
		if err != nil {
			t.Fatalf("dump error: %v", err)
		}
	})

	rdb.FlushDB(context.Background())
	t.Run("Test load compressed", func(t *testing.T) {
		loadArgs := []string{"", "load", metaUrl, dumpFile}
		err := Main(loadArgs)
		if err != nil {
			t.Fatalf("load error: %v", err)
		}
		if rdb.DBSize(context.Background()).Val() == 0 {
			t.Fatalf("load error: %v", err)
		}
	})

	t.Run("Test dump with subdir", func(t *testing.T) {
		dumpArgs := []string{"", "dump", metaUrl, dumpSubdirFile, "--subdir", "d1"}
		err := Main(dumpArgs)
		if err != nil {
			t.Fatalf("dump error: %v", err)
		}
		_, err = os.Stat(dumpSubdirFile)
		if err != nil {
			t.Fatalf("dump error: %v", err)
		}
	})
	rdb.FlushDB(context.Background())
}

func TestLoadBinaryPipe(t *testing.T) {
	source := meta.NewClient("sqlite3://"+filepath.Join(t.TempDir(), "source.db"), nil)
	defer source.Shutdown()
	if err := source.Init(&meta.Format{Name: "pipe-test"}, true); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := source.DumpMetaV2(meta.Background(), &data, &meta.DumpOption{Threads: 1}); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	written := make(chan error, 1)
	go func() {
		_, err := io.Copy(w, &data)
		w.Close()
		written <- err
	}()
	stdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = stdin }()
	uri := "sqlite3://" + filepath.Join(t.TempDir(), "target.db")
	if err := Main([]string{"", "load", "--binary", uri}); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	restored := meta.NewClient(uri, nil)
	defer restored.Shutdown()
	format, err := restored.Load(true)
	if err != nil {
		t.Fatal(err)
	}
	if format.Name != "pipe-test" {
		t.Fatalf("restored volume: %q", format.Name)
	}
}
