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
	"encoding/json"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
)

func TestStatus(t *testing.T) {
	tmpFile, err := os.CreateTemp("/tmp", "")
	if err != nil {
		t.Fatalf("create temporary file: %s", err)
	}
	defer tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	// mock os.Stdout
	patches := gomonkey.ApplyGlobalVar(os.Stdout, *tmpFile)
	defer patches.Reset()

	if err = Main([]string{"", "status", testMeta}); err != nil {
		t.Fatalf("status failed: %s", err)
	}
	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("read file failed: %s", err)
	}
	s := sections{}
	if err = json.Unmarshal(content, &s); err != nil {
		t.Fatalf("json unmarshal failed: %s", err)
	}
	if s.Setting.Name != testVolume || s.Setting.Storage != "file" {
		t.Fatalf("setting is not as expected: %+v", s.Setting)
	}
}
