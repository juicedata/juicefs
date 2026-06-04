//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package chunk

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInRootVolume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.SkipNow()
	}
	if !inRootVolume("/") {
		t.Fatal("`/` is in root volume")
	}
	if inRootVolume(".") {
		testDir := filepath.Join(t.TempDir(), "__test__")
		err := os.MkdirAll(testDir, 0755)
		if err != nil {
			t.Fatal(err)
		}
		if !inRootVolume(testDir) {
			t.Fatalf("%q is in root volume", testDir)
		}
	}
	if !inRootVolume("/tmp") {
		tmpTestDir := filepath.Join("/tmp", "__jfs_test__"+fmt.Sprintf("%d", os.Getpid()))
		err := os.MkdirAll(tmpTestDir, 0755)
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpTestDir)
		if inRootVolume(tmpTestDir) {
			t.Fatalf("%q is not in root volume", tmpTestDir)
		}
	}
}
