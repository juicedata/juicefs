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
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	. "github.com/smartystreets/goconvey/convey"
)

func TestInfo(t *testing.T) {
	Convey("TestInfo", t, func() {
		Convey("TestInfo", func() {

			var res string
			tmpFile, err := os.CreateTemp("/tmp", "")
			if err != nil {
				t.Fatalf("creat tmp file failed: %v", err)
			}
			defer os.Remove(tmpFile.Name())
			if err != nil {
				t.Fatalf("create temporary file: %v", err)
			}
			// mock os.Stdout
			patches := gomonkey.ApplyGlobalVar(os.Stdout, *tmpFile)
			defer patches.Reset()

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

			err = os.MkdirAll(fmt.Sprintf("%s/dir1", mountpoint), 0777)
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

			infoArgs := []string{"", "info", fmt.Sprintf("%s/dir1", mountpoint)}
			err = Main(infoArgs)
			if err != nil {
				t.Fatalf("info failed: %v", err)
			}
			content, err := ioutil.ReadFile(tmpFile.Name())
			if err != nil {
				t.Fatalf("readFile failed: %v", err)
			}
			res = string(content)
			var answer = `/tmp/testDir/dir1: inode: 2 files:	10 dirs:	1 length:	40 size:	45056`
			replacer := strings.NewReplacer("\n", "", " ", "")
			res = replacer.Replace(res)
			answer = replacer.Replace(answer)
			So(res, ShouldEqual, answer)
		})
	})
}
