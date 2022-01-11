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
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"

	. "github.com/smartystreets/goconvey/convey"
)

func TestStatus(t *testing.T) {
	Convey("TestInfo", t, func() {
		Convey("TestInfo", func() {
			tmpFile, err := os.CreateTemp("/tmp", "")
			if err != nil {
				t.Fatalf("creat tmp file failed: %v", err)
			}
			defer tmpFile.Close()
			defer os.Remove(tmpFile.Name())
			if err != nil {
				t.Fatalf("create temporary file: %v", err)
			}
			// mock os.Stdout
			patches := gomonkey.ApplyGlobalVar(os.Stdout, *tmpFile)
			defer patches.Reset()
			metaUrl := "redis://localhost:6379/10"
			mountpoint := "/tmp/testDir"
			statusArgs := []string{"", "status", metaUrl}

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

			if err := Main(statusArgs); err != nil {
				t.Fatalf("test status failed: %v", err)
			}

			content, err := ioutil.ReadFile(tmpFile.Name())
			if err != nil {
				t.Fatalf("readFile failed: %v", err)
			}

			s := sections{}
			if err = json.Unmarshal(content, &s); err != nil {
				t.Fatalf("test status failed: %v", err)
			}
			if s.Setting.Name != "test" || s.Setting.Storage != "file" {
				t.Fatalf("test status failed: %v", err)
			}
		})
	})
}
