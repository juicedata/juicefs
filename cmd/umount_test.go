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
	"testing"

	"github.com/juicedata/juicefs/pkg/utils"
)

func UmountTmp(mountpoint string) error {
	umountArgs := []string{"", "umount", mountpoint}
	return Main(umountArgs)
}

func TestUmount(t *testing.T) {

	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}

	err := UmountTmp(mountpoint)
	if err != nil {
		t.Fatalf("umount failed: %v", err)
	}

	inode, err := utils.GetFileInode(mountpoint)
	if err != nil {
		t.Fatalf("get file inode failed: %v", err)
	}
	if inode == 1 {
		t.Fatalf("umount failed: %v", err)
	}

}
