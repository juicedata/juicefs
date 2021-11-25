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

func TestInfo(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	MountTmp(metaUrl, mountpoint)

	err := os.Mkdir(fmt.Sprintf("%s/dir1", mountpoint), 0777)
	if err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("%s/dir1/f%d.txt", mountpoint, i)
		err := ioutil.WriteFile(filename, []byte("test"), 0644)
		if err != nil {
			t.Fatalf("mount failed: %v", err)
		}
	}

	infoArgs := []string{"", "info", metaUrl, fmt.Sprintf("%s/dir1", mountpoint)}
	Main(infoArgs)
	defer CleanRedis(metaUrl)
}
