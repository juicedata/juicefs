/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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
package sync

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
)

func collectAll(c <-chan object.Object) []string {
	r := make([]string, 0)
	for s := range c {
		r = append(r, s.Key())
	}
	return r
}

// nolint:errcheck
func TestIterator(t *testing.T) {
	m, _ := object.CreateStorage("mem", "", "", "")
	m.Put("a", bytes.NewReader([]byte("a")))
	m.Put("b", bytes.NewReader([]byte("a")))
	m.Put("aa", bytes.NewReader([]byte("a")))
	m.Put("c", bytes.NewReader([]byte("a")))

	ch, _ := ListAll(m, "a", "b")
	keys := collectAll(ch)
	if len(keys) != 3 {
		t.Fatalf("length should be 3, but got %d", len(keys))
	}
	if !reflect.DeepEqual(keys, []string{"a", "aa", "b"}) {
		t.Fatalf("result wrong: %s", keys)
	}

	// Single object
	s, _ := object.CreateStorage("mem", "", "", "")
	s.Put("a", bytes.NewReader([]byte("a")))
	ch, _ = ListAll(s, "", "")
	keys = collectAll(ch)
	if !reflect.DeepEqual(keys, []string{"a"}) {
		t.Fatalf("result wrong: %s", keys)
	}
}

func TestIeratorSingleEmptyKey(t *testing.T) {
	// utils.SetLogLevel(logrus.DebugLevel)

	// Construct mem storage
	s, _ := object.CreateStorage("mem", "", "", "")
	err := s.Put("abc", bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("Put error: %q", err)
	}

	// Simulate command line prefix in SRC or DST
	s = object.WithPrefix(s, "abc")
	ch, _ := ListAll(s, "", "")
	keys := collectAll(ch)
	if !reflect.DeepEqual(keys, []string{""}) {
		t.Fatalf("result wrong: %s", keys)
	}
}

// nolint:errcheck
func TestSync(t *testing.T) {
	// utils.SetLogLevel(logrus.DebugLevel)

	config := &Config{
		Start:     "",
		End:       "",
		Threads:   50,
		Update:    true,
		Perms:     true,
		Dry:       false,
		DeleteSrc: false,
		DeleteDst: false,
		Exclude:   []string{"ab.*"},
		Include:   []string{"[a|b].*"},
		Verbose:   false,
		Quiet:     true,
	}

	a, _ := object.CreateStorage("mem", "a", "", "")
	a.Put("a", bytes.NewReader([]byte("a")))
	a.Put("ab", bytes.NewReader([]byte("ab")))
	a.Put("abc", bytes.NewReader([]byte("abc")))

	b, _ := object.CreateStorage("mem", "b", "", "")
	b.Put("ba", bytes.NewReader([]byte("ba")))

	// Copy "a" from mem://a to mem://b
	if err := Sync(a, b, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	if c := copied.Current(); c != 1 {
		t.Fatalf("should copy 1 keys, but got %d", c)
	}

	// Now a: {"a", "ab", "abc"}, b: {"a", "ba"}
	// Copy "ba" from mem://b to mem://a
	total, totalBytes = 0, 0
	if err := Sync(b, a, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	if c := copied.Current(); c != 1 {
		t.Fatalf("should copy 1 keys, but got %d", c)
	}

	// Now a: {"a", "ab", "abc", "ba"}, b: {"a", "ba"}
	akeys, _ := a.List("", "", 4)
	bkeys, _ := b.List("", "", 4)

	if !reflect.DeepEqual(akeys[0], bkeys[0]) {
		t.FailNow()
	}
	if !reflect.DeepEqual(akeys[len(akeys)-1], bkeys[len(bkeys)-1]) {
		t.FailNow()
	}

	total, totalBytes = 0, 0
	if err := Sync(a, b, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
	// No copy occured
	if c := copied.Current(); c != 0 {
		t.Fatalf("should copy 0 keys, but got %d", c)
	}

	// Test --force-update option
	config.ForceUpdate = true
	// Forcibly copy {"a", "ba"} from mem://a to mem://b.
	total, totalBytes = 0, 0
	if err := Sync(a, b, config); err != nil {
		t.Fatalf("sync: %s", err)
	}
}
