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

	"github.com/stretchr/testify/require"
)

func TestDebug(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	require.NotNil(t, Main([]string{"", "debug", "/jfs/test/mp"}), "mount point does not exist")
	require.NotNil(t, Main([]string{"", "debug", "./"}), "directory is not a mount point")
	require.NotNil(t, Main([]string{"", "debug", "--out-dir", "./debug_test.go", testMountPoint}), "specify a file as out dir")

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
		require.True(t, logArg.FindStringSubmatch(c.arg)[2] == c.val, fmt.Sprintf("valid log arg %d", i))
	}
}
