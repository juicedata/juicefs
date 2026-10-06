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

import "testing"

func TestKerberosRuleSubstitution(t *testing.T) {
	cases := []struct {
		name      string
		rule      string
		principal string
		want      string
	}{
		{"first_capture", "RULE:[1:$1]s/(alice)/local_$1/", "alice-alice@EXAMPLE.COM", "local_alice-alice"},
		{"global_capture", "RULE:[1:$1]s/(alice)/local_$1/g", "alice-alice@EXAMPLE.COM", "local_alice-local_alice"},
		{"multiple_captures", "RULE:[1:$1]s/(alice)-(bob)/$2-$1/", "alice-bob@EXAMPLE.COM", "bob-alice"},
		{"capture_with_suffix", "RULE:[1:$1]s/(alice)/$1/", "alice-bob@EXAMPLE.COM", "alice-bob"},
		{"first_literal", "RULE:[1:$1]s/alice/local/", "alice-alice@EXAMPLE.COM", "local-alice"},
		{"global_literal", "RULE:[1:$1]s/alice/local/g", "alice-alice@EXAMPLE.COM", "local-local"},
		{"no_match", "RULE:[1:$1]s/alice/local_$1/", "bob@EXAMPLE.COM", "bob"},
		{"zero_width", "RULE:[1:$1]s/^/local_/", "alice@EXAMPLE.COM", "local_alice"},
		{"lowercase", "RULE:[1:$1]s/(ALICE)/local_$1/L", "ALICE@EXAMPLE.COM", "local_alice"},
		{"two_components", "RULE:[2:$1-$2]s/(alice)-(host)/$2-$1/", "alice/host@EXAMPLE.COM", "host-alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vol := &volParams{}
			vol.parse("rule", "", tc.rule)
			if len(vol.rules.rules) != 1 {
				t.Fatalf("rule was not parsed: %q", tc.rule)
			}
			if got := vol.rules.getShortName(tc.principal); got != tc.want {
				t.Fatalf("getShortName(%q) = %q, want %q", tc.principal, got, tc.want)
			}
		})
	}
}
