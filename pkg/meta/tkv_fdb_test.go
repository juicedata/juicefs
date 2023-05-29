//go:build fdb
// +build fdb

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

//nolint:errcheck
package meta

import (
	"testing"
)

func TestFdbClient(t *testing.T) { //skip mutate
	m, err := newKVMeta("fdb", "/etc/foundationdb/fdb.cluster?prefix=test2", testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestFdb(t *testing.T) { //skip mutate
	c, err := newFdbClient("/etc/foundationdb/fdb.cluster?prefix=test1")
	if err != nil {
		t.Fatal(err)
	}
	testTKV(t, c)
}
