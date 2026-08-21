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
	var filePath string
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

	if err = Main([]string{"", "warmup", "--evict", testMountPoint}); err != nil {
		t.Fatalf("evict: %s", err)
	}

	if err = Main([]string{"", "warmup", testMountPoint}); err != nil {
		t.Fatalf("warmup: %s", err)
	}

	filePath = fmt.Sprintf("%s/%s/raw/chunks/0/0/1_0_4", cacheDir, uuid)
	deadline := time.Now().Add(10 * time.Second)
	var content []byte
	for {
		content, err = os.ReadFile(filePath)
		if err == nil && len(content) >= 4 && string(content[:4]) == "test" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("warmup: %s; got content %s", err, content)
		}
		time.Sleep(100 * time.Millisecond)
	}
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
	if err := os.WriteFile(fileList2, []byte(fmt.Sprintf("%s/f1.txt 0-5\n", testMountPoint)), 0644); err != nil {
		t.Fatalf("write filelist2: %s", err)
	}

	// Warmup with file list containing ranges - should not panic
	if err := Main([]string{"", "warmup", "-f", fileList2, testMountPoint}); err != nil {
		t.Fatalf("warmup with range file list: %s", err)
	}
}

func TestSplitPathRanges(t *testing.T) {
	tests := []struct {
		target     string
		wantPath   string
		wantRanges string
	}{
		{"/data/file.lance", "/data/file.lance", ""},
		{"/data/file.lance\t0-100", "/data/file.lance", "\t0-100"},
		{"/data/a\tb\t0-100;200-300", "/data/a\tb", "\t0-100;200-300"},
	}
	for _, tt := range tests {
		gotPath, gotRanges := splitPathRanges(tt.target)
		if gotPath != tt.wantPath || gotRanges != tt.wantRanges {
			t.Errorf("splitPathRanges(%q) = (%q, %q), want (%q, %q)", tt.target, gotPath, gotRanges, tt.wantPath, tt.wantRanges)
		}
	}
}

func TestRangeLineRegex(t *testing.T) {
	tests := []struct {
		line       string
		wantMatch  bool
		wantPath   string
		wantRanges string
	}{
		{"/data/file.lance", false, "", ""},
		{"/data/my file.lance", false, "", ""},
		{"/data/10-20", false, "", ""},
		{"/data/file.lance 0-100", true, "/data/file.lance", "0-100"},
		{"/data/my file.lance 0-100;200-300", true, "/data/my file.lance", "0-100;200-300"},
		{"relative/file 5-10", true, "relative/file", "5-10"},
	}
	for _, tt := range tests {
		m := rangeLineRe.FindStringSubmatch(tt.line)
		if !tt.wantMatch {
			if m != nil {
				t.Errorf("%q unexpectedly matched: %v", tt.line, m)
			}
			continue
		}
		if m == nil {
			t.Errorf("%q did not match", tt.line)
			continue
		}
		if gotPath, gotRanges := m[1], m[2]; gotPath != tt.wantPath || gotRanges != tt.wantRanges {
			t.Errorf("%q => path %q ranges %q, want path %q ranges %q", tt.line, gotPath, gotRanges, tt.wantPath, tt.wantRanges)
		}
	}
}
