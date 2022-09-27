/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDebug(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)
	Convey("TestDebug", t, func() {
		Convey("Mount point does not exist", func() {
			mp := "/jfs/test/mp"
			So(Main([]string{"", "debug", mp}), ShouldNotBeNil)
		})

		Convey("Directory is not a mount point", func() {
			mp := "./"
			So(Main([]string{"", "debug", mp}), ShouldNotBeNil)
		})

	})

	Convey("TestDebug_OutDir", t, func() {
		Convey("Specify a file as out dir", func() {
			So(Main([]string{"", "debug", "--out-dir", "./debug_test.go", testMountPoint}), ShouldNotBeNil)
		})
	})

	Convey("TestDebug_LogArg", t, func() {
		cases := []struct {
			arg string
			val string
		}{
			{"--log /var/log/jfs.log", "/var/log/jfs.log"},
			{"--log=/var/log/jfs.log", "/var/log/jfs.log"},
			{"--log   =   /var/log/jfs.log", "/var/log/jfs.log"},
			{"--log=    /var/log/jfs.log", "/var/log/jfs.log"},
			{"--log    =/var/log/jfs.log", "/var/log/jfs.log"},
			{"--log      /var/log/jfs.log", "/var/log/jfs.log"},
		}
		for i, c := range cases {
			Convey(fmt.Sprintf("valid log arg %d", i), func() {
				So(logArg.FindStringSubmatch(c.arg)[2] == c.val, ShouldBeTrue)
			})
		}
	})
}
