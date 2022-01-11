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

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"
)

func TestRmr(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	defer ResetRedis(metaUrl)
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	defer func(mountpoint string) {
		err := UmountTmp(mountpoint)
		if err != nil {
			t.Fatalf("umount failed: %v", err)
		}
	}(mountpoint)

	paths := []string{"/dir1", "/dir2", "/dir3/dir2"}
	for _, path := range paths {
		if err := os.MkdirAll(fmt.Sprintf("%s%s/dir2/dir3/dir4/dir5", mountpoint, path), 0777); err != nil {
			t.Fatalf("Test mount err %v", err)
		}
	}
	for i := 0; i < 5; i++ {
		filename := fmt.Sprintf("%s/dir1/f%d.txt", mountpoint, i)
		err := ioutil.WriteFile(filename, []byte("test"), 0644)
		if err != nil {
			t.Fatalf("Test mount failed : %v", err)
		}
	}

	rmrArgs := []string{"", "rmr", mountpoint + paths[0], mountpoint + paths[1], mountpoint + paths[2]}
	if err := Main(rmrArgs); err != nil {
		t.Fatalf("rmr failed : %v", err)
	}

	for _, path := range paths {
		dir, err := os.ReadDir(mountpoint + path)
		if len(dir) != 0 {
			t.Fatalf("test rmr error: %v", err)
		}
	}
}
