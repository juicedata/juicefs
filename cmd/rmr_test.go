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
	"testing"
)

func TestRmr(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	paths := []string{"/dir1", "/dir2", "/dir3/dir2"}
	for _, path := range paths {
		if err := os.MkdirAll(fmt.Sprintf("%s%s/dir2/dir3/dir4/dir5", testMountPoint, path), 0777); err != nil {
			t.Fatalf("mkdirAll err: %s", err)
		}
	}
	for i := 0; i < 5; i++ {
		filename := fmt.Sprintf("%s/dir1/f%d.txt", testMountPoint, i)
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("write file failed: %s", err)
		}
	}

	rmrArgs := []string{"", "rmr", testMountPoint + paths[0], testMountPoint + paths[1], testMountPoint + paths[2]}
	if err := Main(rmrArgs); err != nil {
		t.Fatalf("rmr failed: %s", err)
	}

	for _, path := range paths {
		if dir, err := os.ReadDir(testMountPoint + path); !os.IsNotExist(err) {
			t.Fatalf("test rmr error: %s len(dir): %d", err, len(dir))
		}
	}
}
