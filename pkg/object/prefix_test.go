/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"testing"
)

func TestDirStorage(t *testing.T) {
	base, _ := CreateStorage("mem", "bucket", "", "", "")

	tests := []struct {
		name    string
		storage func() ObjectStorage
		wantStr string
	}{
		{
			name:    "withPrefix_dir",
			storage: func() ObjectStorage { return WithPrefix(base, "subdir/") },
			wantStr: base.String() + "subdir/",
		},
		{
			name:    "withPrefix_file",
			storage: func() ObjectStorage { return WithPrefix(base, "subdir/file") },
			wantStr: base.String() + "subdir/",
		},
		{
			name:    "withPrefix_toplevel_file",
			storage: func() ObjectStorage { return WithPrefix(base, "file") },
			wantStr: base.String(),
		},
		{
			name:    "withPrefix_empty",
			storage: func() ObjectStorage { return WithPrefix(base, "") },
			wantStr: base.String(),
		},
		{
			name:    "withPrefix_nested_file",
			storage: func() ObjectStorage { return WithPrefix(base, "a/b/c") },
			wantStr: base.String() + "a/b/",
		},
		{
			name: "filestore_dir",
			storage: func() ObjectStorage {
				fs, _ := CreateStorage("file", "/tmp/", "", "", "")
				return fs
			},
			wantStr: "file:///tmp/",
		},
		{
			name: "filestore_file",
			storage: func() ObjectStorage {
				return &filestore{root: "/tmp/target"}
			},
			wantStr: "file:///tmp/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.storage()
			got := DirStorage(s)
			if got.String() != tc.wantStr {
				t.Errorf("DirStorage(%q).String() = %q, want %q", s.String(), got.String(), tc.wantStr)
			}
		})
	}
}
