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
