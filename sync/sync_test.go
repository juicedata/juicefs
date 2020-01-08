// Copyright (C) 2018-present Juicedata Inc.
package sync

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/juicedata/juicesync/config"
	"github.com/juicedata/juicesync/object"
)

func collectAll(c <-chan *object.Object) []string {
	r := make([]string, 0)
	for s := range c {
		r = append(r, s.Key)
	}
	return r
}

func TestIterator(t *testing.T) {
	m := object.CreateStorage("mem", "", "", "")
	m.Put("a", bytes.NewReader([]byte("a")))
	m.Put("b", bytes.NewReader([]byte("a")))
	m.Put("aa", bytes.NewReader([]byte("a")))
	m.Put("c", bytes.NewReader([]byte("a")))

	ch, _ := Iterate(m, "a", "c")
	keys := collectAll(ch)
	if len(keys) != 2 {
		t.Errorf("length should be 2, but got %d", len(keys))
		t.FailNow()
	}
	if !reflect.DeepEqual(keys, []string{"aa", "b"}) {
		t.Errorf("result wrong: %s", keys)
		t.FailNow()
	}

	// Single object
	s := object.CreateStorage("mem", "", "", "")
	s.Put("a", bytes.NewReader([]byte("a")))
	ch, _ = Iterate(s, "", "")
	keys = collectAll(ch)
	if !reflect.DeepEqual(keys, []string{"a"}) {
		t.Errorf("result wrong: %s", keys)
		t.FailNow()
	}
}

func TestSync(t *testing.T) {
	config := &config.Config{
		Start:     "",
		End:       "",
		Threads:   50,
		Update:    false,
		Dry:       false,
		DeleteSrc: false,
		DeleteDst: false,
		Verbose:   false,
		Quiet:     false,
	}

	a := object.CreateStorage("mem", "", "", "")
	a.Put("a", bytes.NewReader([]byte("a")))
	a.Put("b", bytes.NewReader([]byte("a")))

	b := object.CreateStorage("mem", "", "", "")
	b.Put("aa", bytes.NewReader([]byte("a")))

	if err := Sync(a, b, config); err != nil {
		t.FailNow()
	}
	if copied != 2 {
		t.Errorf("should copy 2 keys, but got %d", copied)
		t.FailNow()
	}

	if err := Sync(b, a, config); err != nil {
		t.FailNow()
	}
	if copied != 3 {
		t.Errorf("should copy 3 keys, but got %d", copied)
		t.FailNow()
	}

	akeys, _ := a.List("", "", 4)
	bkeys, _ := b.List("", "", 4)
	if !reflect.DeepEqual(akeys, bkeys) {
		t.FailNow()
	}

	if err := Sync(a, b, config); err != nil {
		t.FailNow()
	}
	if copied != 3 {
		t.FailNow()
	}
}
