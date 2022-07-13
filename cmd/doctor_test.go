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
	"os"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDoctor(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)
	Convey("TestDoctor", t, func() {
		cases := []struct {
			name string
			args []string
		}{
			{"Simple cases", []string{"", "doctor"}},
			{"Enable collecting syslog", []string{"", "doctor", "--collect-log"}},
			{"Max 5 log entries", []string{"", "doctor", "--collect-log", "--limit", "5"}},
		}

		for _, c := range cases {
			Convey(c.name, func() {
				So(Main(append(c.args, testMountPoint)), ShouldBeNil)
			})
		}

		Convey("Mount point does not exist", func() {
			mp := "/jfs/test/mp"
			So(Main([]string{"", "doctor", mp}), ShouldNotBeNil)
		})

		Convey("Directory is not a mount point", func() {
			mp := "./"
			So(Main([]string{"", "doctor", mp}), ShouldNotBeNil)
		})

	})

	Convey("TestDoctor_OutDir", t, func() {

		Convey("Use default out dir", func() {
			So(Main([]string{"", "doctor", testMountPoint}), ShouldBeNil)
		})

		outDir := "./doctor/ok"
		Convey("Specify existing out dir", func() {
			if err := os.MkdirAll(outDir, 0755); err != nil {
				t.Fatalf("doctor error: %v", err)
			}
			So(Main([]string{"", "doctor", "--out-dir", outDir, testMountPoint}), ShouldBeNil)
			if err := os.RemoveAll(outDir); err != nil {
				t.Fatalf("doctor error: %v", err)
			}
		})

		Convey("Specify a non-existing out dir", func() {
			So(Main([]string{"", "doctor", "--out-dir", "./doctor/out1", testMountPoint}), ShouldBeNil)
		})

		Convey("Specify a file as out dir", func() {
			So(Main([]string{"", "doctor", "--out-dir", "./doctor_test.go", testMountPoint}), ShouldNotBeNil)
		})
	})
}
