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
