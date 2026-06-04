//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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

package object

import (
	"path/filepath"
	"os"
	"testing"
	"time"
)

func TestLChtimes(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "LChtimesTestAfile1")
	linkPath := filepath.Join(tmpDir, "LChtimesTestLink1")
	_, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create file failed: %s", err)
	}
	err = os.Symlink(filePath, linkPath)
	if err != nil {
		t.Fatalf("symlink file failed: %s", err)
	}
	oldStat, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat file failed: %s", err)
	}

	oldAtime := getAtime(oldStat)
	newMtime := oldStat.ModTime().Add(-time.Hour)
	err = lchtimes(linkPath, time.Time{}, newMtime)
	if err != nil {
		t.Fatalf("lchtimes file failed: %s", err)
	}
	newStat, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat file failed: %s", err)
	}
	if newStat.ModTime() != newMtime {
		t.Fatalf("mtime change failed")
	}
	newAtime := getAtime(newStat)
	if newAtime != oldAtime {
		t.Fatalf("atime change failed")
	}
}
