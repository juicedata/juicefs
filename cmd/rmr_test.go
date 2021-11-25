/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}

	defer ResetRedis(metaUrl)
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
	err := UmountTmp(mountpoint)
	if err != nil {
		t.Fatalf("umount failed: %v", err)
	}
}
