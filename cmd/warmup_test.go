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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

func TestWarmup(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	if err := os.WriteFile(fmt.Sprintf("%s/f1.txt", testMountPoint), []byte("test"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}
	m := meta.NewClient(testMeta, nil)
	format, err := m.Load(true)
	if err != nil {
		t.Fatalf("load setting err: %s", err)
	}
	uuid := format.UUID
	var cacheDir = "/var/jfsCache"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin", "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("%v", err)
		}
		cacheDir = fmt.Sprintf("%s/.juicefs/cache", homeDir)
	}

	defer os.RemoveAll(fmt.Sprintf("%s/%s", cacheDir, uuid))

	filePath := fmt.Sprintf("%s/%s/raw/chunks/0/0/1_0_4", cacheDir, uuid)
	// The cached block may carry a checksum footer, so only compare the block data.
	cached := func() bool {
		content, err := os.ReadFile(filePath)
		return err == nil && len(content) >= 4 && string(content[:4]) == "test"
	}
	// The block is cached asynchronously while being uploaded. Wait until it has
	// been flushed into the disk cache, otherwise evicting it only cancels the
	// pending flush and the following warmup races with it. See #7464.
	if !waitFor(cached) {
		t.Fatalf("block was not cached after write")
	}

	if err = Main([]string{"", "warmup", "--evict", testMountPoint}); err != nil {
		t.Fatalf("evict: %s", err)
	}
	if !waitFor(func() bool { _, err := os.Stat(filePath); return os.IsNotExist(err) }) {
		t.Fatalf("evict: cache block still exists")
	}

	if err = Main([]string{"", "warmup", testMountPoint}); err != nil {
		t.Fatalf("warmup: %s", err)
	}

	if !waitFor(cached) {
		content, err := os.ReadFile(filePath)
		t.Fatalf("warmup: %s; got content %s", err, content)
	}
}

func waitFor(cond func() bool) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestWarmupWithRangeFile(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	// Create a test file
	if err := os.WriteFile(fmt.Sprintf("%s/f1.txt", testMountPoint), []byte("hello world"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}

	// Create a file list with path only (backward compatible)
	fileList := filepath.Join(t.TempDir(), "filelist.txt")
	if err := os.WriteFile(fileList, []byte(fmt.Sprintf("%s/f1.txt\n", testMountPoint)), 0644); err != nil {
		t.Fatalf("write filelist: %s", err)
	}

	// Warmup with file list (no ranges) - should work as before
	if err := Main([]string{"", "warmup", "-f", fileList, testMountPoint}); err != nil {
		t.Fatalf("warmup with file list: %s", err)
	}

	// Create a file list with ranges
	fileList2 := filepath.Join(t.TempDir(), "filelist2.txt")
	if err := os.WriteFile(fileList2, []byte(fmt.Sprintf("%s/f1.txt [0-5]\n", testMountPoint)), 0644); err != nil {
		t.Fatalf("write filelist2: %s", err)
	}

	// Warmup with file list containing ranges - should not panic
	if err := Main([]string{"", "warmup", "-f", fileList2, testMountPoint}); err != nil {
		t.Fatalf("warmup with range file list: %s", err)
	}
}
