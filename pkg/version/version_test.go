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

package version

import "testing"

func TestVersion(t *testing.T) {
	ver = Semver{
		major: 1,
		minor: 0,
		patch: 0,
		build: "2022-02-22.f4692af9",
	}
	if v := Version(); v != "1.0.0+2022-02-22.f4692af9" {
		t.Fatalf("Version %s != expected 1.0.0+2022-02-22.f4692af9", v)
	}
	if _, err := CompareVersions(&ver, Parse("")); err == nil {
		t.Fatalf("Expect failed to parse empty string")
	}
	if _, err := CompareVersions(&ver, Parse("0.1.2.3")); err == nil {
		t.Fatalf("Expect failed to parse string \"0.1.2.3\"")
	}

	cases := []struct {
		vs     string
		expect int
	}{
		{"0.9+foo.bar", 1},
		{"0.9.10", 1},
		{"1.0-beta+baz", 1},
		{"1", 0},
		{"1.1", -1},
		{"2.0.0-alpha", -1},
	}
	for _, c := range cases {
		if r, _ := CompareVersions(&ver, Parse(c.vs)); r != c.expect {
			t.Fatalf("Failed case: %+v", c)
		}
	}

	ver.preRelease = "beta"
	if v := Version(); v != "1.0.0-beta+2022-02-22.f4692af9" {
		t.Fatalf("Version %s != expected 1.0.0-beta+2022-02-22.f4692af9", v)
	}
	cases[2].expect = 0
	cases[3].expect = -1
	for _, c := range cases {
		if r, _ := CompareVersions(&ver, Parse(c.vs)); r != c.expect {
			t.Fatalf("Failed case: %+v", c)
		}
	}
}
