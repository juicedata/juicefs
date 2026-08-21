/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"math"
	"strconv"
	"testing"
)

func TestGuidMask(t *testing.T) {
	cases := []struct {
		name     string
		mask     string
		metaName string
		want     uint32
		wantErr  bool
	}{
		{"explicit uint32 on mysql", "4294967295", "mysql", 0xFFFFFFFF, false},
		{"explicit uint32 on redis", "4294967295", "redis", 0xFFFFFFFF, false},
		{"explicit hex uint32 on mysql", "0xffffffff", "mysql", 0xFFFFFFFF, false},
		{"explicit uppercase hex uint32", "0XFFFFFFFF", "redis", 0xFFFFFFFF, false},
		{"explicit int31 on redis", "2147483647", "redis", 0x7FFFFFFF, false},
		{"explicit hex int31 on redis", "0x7fffffff", "redis", 0x7FFFFFFF, false},
		{"explicit int31 on sqlite", "2147483647", "sqlite3", 0x7FFFFFFF, false},
		{"octal mask with leading zero", "010", "redis", 8, false},
		{"explicit binary mask", "0b1111", "redis", 15, false},
		{"explicit octal mask", "0o17", "redis", 15, false},
		{"mask with separators", "0x00ff_ffff", "redis", 0x00FFFFFF, false},
		{"explicit arbitrary mask", "16777215", "redis", 0x00FFFFFF, false},
		{"explicit arbitrary hex mask", "0x00ffffff", "redis", 0x00FFFFFF, false},
		{"default mysql", "", "mysql", 0x7FFFFFFF, false},
		{"default postgres", "", "postgres", 0x7FFFFFFF, false},
		{"default sqlite3", "", "sqlite3", 0x7FFFFFFF, false},
		{"default redis", "", "redis", 0, false},
		{"default tikv", "", "tikv", 0, false},
		{"zero decimal mask", "0", "mysql", 0, true},
		{"zero hex mask", "0x0", "redis", 0, true},
		{"negative mask", "-1", "redis", 0, true},
		{"overflow mask", "0x100000000", "redis", 0, true},
		{"invalid octal mask", "08", "redis", 0, true},
		{"invalid mask", "invalid", "redis", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := guidMask(c.mask, c.metaName)
			if (err != nil) != c.wantErr {
				t.Fatalf("guidMask(%q, %q) error = %v, wantErr %v", c.mask, c.metaName, err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("guidMask(%q, %q) = %#x, want %#x", c.mask, c.metaName, got, c.want)
			}
		})
	}
}

// TestGenGuidRespectsMask makes sure the same volume+name yields a stable id and
// that the mask decides whether the full uint32 range is used. It also captures
// the cross-engine bug from issue #7338: a hash whose high bit is set differs
// between the uint32 and int31 masks.
func TestGenGuidRespectsMask(t *testing.T) {
	m := &mapping{salt: "uidcheck"}

	// Find a name whose raw uint32 hash exceeds MaxInt32 so int31 masking changes it.
	var name string
	var raw uint32
	for i := 0; i < 100000; i++ {
		n := "user" + strconv.Itoa(i)
		if id := m.genGuid(n); id > math.MaxInt32 {
			name, raw = n, id
			break
		}
	}
	if name == "" {
		t.Skip("no sample name with high-bit hash found")
	}

	m.mask = math.MaxUint32
	if got := m.genGuid(name); got != raw {
		t.Fatalf("uint32 mask is not deterministic: %d != %d", got, raw)
	}
	m.mask = 0x7FFFFFFF
	int31 := m.genGuid(name)
	if int31 > math.MaxInt32 {
		t.Fatalf("int31 mode produced out-of-range id: %d", int31)
	}
	if int31 == raw {
		t.Fatalf("expected int31 id to differ from uint32 id for high-bit hash")
	}
	if int31 != raw&0x7FFFFFFF {
		t.Fatalf("int31 id = %#x, want %#x", int31, raw&0x7FFFFFFF)
	}
}
