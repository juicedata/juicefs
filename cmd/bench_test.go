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
	"os"
	"testing"
)

func TestBench(t *testing.T) {
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
	benchArgs := []string{"", "bench", mountpoint}
	os.Setenv("SKIP_DROP_CACHES", "true")
	defer os.Unsetenv("SKIP_DROP_CACHES")
	err := Main(benchArgs)
	if err != nil {
		t.Fatalf("test bench failed: %v", err)
	}
}
