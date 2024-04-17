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
	"os"
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
		err := os.MkdirAll("./__test__", 0755)
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll("./__test__")
		if !inRootVolume("./__test__") {
			t.Fatal("`./__test__` is in root volume")
		}
	}
	if !inRootVolume("/tmp") {
		err := os.MkdirAll("/tmp/__jfs_test__", 0755)
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll("/tmp/__jfs_test__")
		if inRootVolume("/tmp/__jfs_test__") {
			t.Fatal("`/tmp/__jfs_test__` is not in root volume")
		}
	}
}
