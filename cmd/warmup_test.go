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
	"runtime"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

func TestWarmup(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	if err := os.WriteFile(fmt.Sprintf("%s/f1.txt", testMountPoint), []byte("test"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}
	m := meta.NewClient(testMeta, nil)
	format, err := m.Load(true)
	if err != nil {
		t.Fatalf("load setting err: %s", err)
	}
	uuid := format.UUID
	var cacheDir = "/var/jfsCache"
	var filePath string
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin", "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("%v", err)
		}
		cacheDir = fmt.Sprintf("%s/.juicefs/cache", homeDir)
	}

	os.RemoveAll(fmt.Sprintf("%s/%s", cacheDir, uuid))
	defer os.RemoveAll(fmt.Sprintf("%s/%s", cacheDir, uuid))

	if err = Main([]string{"", "warmup", testMountPoint}); err != nil {
		t.Fatalf("warmup: %s", err)
	}

	time.Sleep(2 * time.Second)
	filePath = fmt.Sprintf("%s/%s/raw/chunks/0/0/1_0_4", cacheDir, uuid)
	content, err := os.ReadFile(filePath)
	if err != nil || len(content) < 4 || string(content[:4]) != "test" {
		t.Fatalf("warmup: %s; got content %s", err, content)
	}
}
