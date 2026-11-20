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
	"crypto/rand"
	"crypto/rsa"
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

func TestIsFileSystem(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %s", err)
	}
	enc, err := NewDataEncryptor(NewRSAEncryptor(key), AES256GCM_RSA)
	if err != nil {
		t.Fatalf("create encryptor: %s", err)
	}
	mem, _ := CreateStorage("mem", "bucket", "", "", "")
	file, _ := CreateStorage("file", t.TempDir()+"/", "", "", "")
	dav, _ := CreateStorage("webdav", "http://127.0.0.1:1/", "", "", "")
	for _, tc := range []struct {
		name          string
		storage       ObjectStorage
		fs, pathBased bool
	}{
		{"mem", mem, false, false},
		{"mem_prefix", WithPrefix(mem, "p/"), false, false},
		{"mem_prefix_chunked_encrypted", NewChunkedEncrypted(WithPrefix(mem, "p/"), enc), false, false},
		{"mem_encrypted", NewEncrypted(mem, enc), false, false},
		{"file", file, true, true},
		{"file_prefix", WithPrefix(file, "p/"), true, true},
		{"file_prefix_chunked_encrypted", NewChunkedEncrypted(WithPrefix(file, "p/"), enc), true, true},
		{"file_encrypted", NewEncrypted(file, enc), true, true},
		{"webdav", dav, false, true},
		{"webdav_prefix_chunked_encrypted", NewChunkedEncrypted(WithPrefix(dav, "p/"), enc), false, true},
	} {
		if got := IsFileSystem(tc.storage); got != tc.fs {
			t.Errorf("%s: IsFileSystem = %v, want %v", tc.name, got, tc.fs)
		}
		if got := IsPathBased(tc.storage); got != tc.pathBased {
			t.Errorf("%s: IsPathBased = %v, want %v", tc.name, got, tc.pathBased)
		}
	}
}
