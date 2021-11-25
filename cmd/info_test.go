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
	"io"
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
			patches := gomonkey.ApplyFuncVar(&writerInfo, func(content string, w io.Writer) error {
				res = content
				return nil
			})
			defer patches.Reset()

			metaUrl := "redis://127.0.0.1:6379/10"
			mountpoint := "/tmp/testDir"
			if err := MountTmp(metaUrl, mountpoint); err != nil {
				t.Fatalf("mount failed: %v", err)
			}

			err := os.MkdirAll(fmt.Sprintf("%s/dir1", mountpoint), 0777)
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
			t.Log(res)

			var answer = `/tmp/testDir/dir1: inode: 2 files:	10 dirs:	1 length:	40 size:	45056`
			defer ResetRedis(metaUrl)
			err = UmountTmp(mountpoint)
			if err != nil {
				t.Fatalf("umount failed: %v", err)
			}
			replacer := strings.NewReplacer("\n", "", " ", "")
			res = replacer.Replace(res)
			answer = replacer.Replace(answer)
			So(res, ShouldEqual, answer)
		})
	})
}
