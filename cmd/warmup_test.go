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
	"runtime"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

func TestWarmup(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	defer ResetRedis(metaUrl)
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	defer func() {
		err := UmountTmp(mountpoint)
		if err != nil {
			t.Fatalf("umount failed: %v", err)
		}
	}()

	err := ioutil.WriteFile(fmt.Sprintf("%s/f1.txt", mountpoint), []byte("test"), 0644)
	if err != nil {
		t.Fatalf("test mount failed: %v", err)
	}
	m := meta.NewClient(metaUrl, &meta.Config{Retries: 10, Strict: true})
	format, err := m.Load()
	if err != nil {
		t.Fatalf("load setting err: %s", err)
	}
	uuid := format.UUID
	var cacheDir string
	var filePath string
	switch runtime.GOOS {
	case "darwin", "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("%v", err)
		}
		cacheDir = fmt.Sprintf("%s/.juicefs/cache", homeDir)
	default:
		cacheDir = "/var/jfsCache"
	}

	os.RemoveAll(fmt.Sprintf("%s/%s", cacheDir, uuid))
	defer os.RemoveAll(fmt.Sprintf("%s/%s", cacheDir, uuid))

	warmupArgs := []string{"", "warmup", mountpoint}
	err = Main(warmupArgs)
	if err != nil {
		t.Fatalf("warmup error: %v", err)
	}

	time.Sleep(2 * time.Second)
	filePath = fmt.Sprintf("%s/%s/raw/chunks/0/0/1_0_4", cacheDir, uuid)
	content, err := ioutil.ReadFile(filePath)
	if err != nil || string(content) != "test" {
		t.Fatalf("warmup error:%v", err)
	}
}
