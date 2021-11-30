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
