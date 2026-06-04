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
	"context"
	"os"
	"path/filepath"
	"testing"

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
