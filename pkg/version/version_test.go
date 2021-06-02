/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package version

import "testing"

func TestParseVersion(t *testing.T) {
	t.Run("Should return error for invalid Version", func(t *testing.T) {
		invalidVers := []string{"2.sadf.1", "3", "t.3.4"}
		for _, v := range invalidVers {
			_, err := ParseVersion(v)
			if err == nil {
				t.Fail()
			}
		}
	})
	t.Run("Should parse empty Version", func(t *testing.T) {
		ver, err := ParseVersion("")
		if err != nil {
			t.Fatalf("Failed to parse an empty Version: %s", err)
		}
		if !(ver.Major == 0 && ver.Minor == 0 && ver.Patch == 0) {
			t.Fatalf("Expect %s, got %s", "0.0.0", ver)
		}
	})
	t.Run("Should parse Version", func(t *testing.T) {
		ver, err := ParseVersion("0.13.1")
		if err != nil {
			t.Fatalf("Failed to parse a valid Version: %s", err)
		}
		if !(ver.Major == 0 && ver.Minor == 13 && ver.Patch == 1) {
			t.Fatalf("Expect %s, got %s", "0.13.1", ver)
		}
		if ver.String() != "0.13.1" {
			t.Fatalf("Expect %s, got %s", "0.13.1", ver)
		}
	})
	t.Run("Should parse dev Version", func(t *testing.T) {
		ver, err := ParseVersion("0.14-dev")
		if err != nil {
			t.Fatalf("Failed to parse a valid Version: %s", err)
		}
		if !(ver.Major == 0 && ver.Minor == 14 && ver.Patch == -1) {
			t.Fatalf("Expect %s, got %s", "0.14-dev (0.14.-1)", ver)
		}
		if ver.String() != "0.14-dev" {
			t.Fatalf("Expect %s, got %s", "0.14-dev", ver)
		}
	})
}

func TestOlderThan(t *testing.T) {
	v := Version{0, 13, 1}
	if !v.OlderThan(Version{1, 0, 0}) {
		t.Fatal("Expect true, got false.")
	}
	if !v.OlderThan(Version{0, 14, -1}) {
		t.Fatal("Expect true, got false.")
	}
	if v.OlderThan(Version{0, 12, 3}) {
		t.Fatal("Expect false, got true.")
	}
	if v.OlderThan(Version{0, 13, 0}) {
		t.Fatal("Expect false, got true.")
	}
	if v.OlderThan(v) {
		t.Fatal("Expect false, got true.")
	}
	if v.OlderThan(Version{}) {
		t.Fatal("Expect false, got true.")
	}
}
