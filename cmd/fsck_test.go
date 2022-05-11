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
	"testing"
)

func TestFsck(t *testing.T) {
	mountTemp(t, nil, true)
	defer umountTemp(t)

	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("%s/f%d.txt", testMountPoint, i)
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("write file failed: %s", err)
		}
	}
	if err := Main([]string{"", "fsck", testMeta}); err != nil {
		t.Fatalf("fsck failed: %s", err)
	}
}
