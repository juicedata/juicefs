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
	"os"
	"testing"
)

func TestBench(t *testing.T) {
	mountTemp(t, nil, []string{"--trash-days=0"}, nil)
	defer umountTemp(t)

	os.Setenv("SKIP_DROP_CACHES", "true")
	defer os.Unsetenv("SKIP_DROP_CACHES")
	if err := Main([]string{"", "bench", testMountPoint}); err != nil {
		t.Fatalf("test bench failed: %s", err)
	}
}

func TestBenchForObject(t *testing.T) {
	if err := Main([]string{"", "objbench", testMountPoint + "/", "-p", "4"}); err != nil {
		t.Fatalf("test bench failed: %s", err)
	}
}
