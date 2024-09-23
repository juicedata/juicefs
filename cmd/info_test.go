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
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
)

func TestInfo(t *testing.T) {
	tmpFile, err := os.CreateTemp("/tmp", "")
	if err != nil {
		t.Fatalf("create temporary file: %s", err)
	}
	defer tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)
	// mock os.Stdout
	patches := gomonkey.ApplyGlobalVar(os.Stdout, *tmpFile)
	defer patches.Reset()

	if err = os.MkdirAll(fmt.Sprintf("%s/dir1", testMountPoint), 0777); err != nil {
		t.Fatalf("mkdirAll failed: %s", err)
	}
	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("%s/dir1/f%d.txt", testMountPoint, i)
		if err = os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("write file failed: %s", err)
		}
	}

	if err = Main([]string{"", "info", fmt.Sprintf("%s/dir1", testMountPoint), "--strict"}); err != nil {
		t.Fatalf("info failed: %s", err)
	}
	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("read file failed: %s", err)
	}
	replacer := strings.NewReplacer("\n", "", " ", "")
	res := replacer.Replace(string(content))
	answer := fmt.Sprintf("%s/dir1: inode: 2 files: 10 dirs: 1 length: 40 Bytes size: 44.00 KiB (45056 Bytes) path: /dir1", testMountPoint)
	answer = replacer.Replace(answer)
	require.Equal(t, answer, res)
}
