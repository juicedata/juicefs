package main

import (
	"bytes"
	"osync/object"
	"reflect"
	"testing"
)

func collectAll(c <-chan string) []string {
	r := make([]string, 0)
	for s := range c {
		r = append(r, s)
	}
	return r
}

func TestIterator(t *testing.T) {
	m := object.CreateStorage("mem", "", "", "")
	m.Put("a", bytes.NewReader([]byte("a")))
	m.Put("b", bytes.NewReader([]byte("a")))
	m.Put("aa", bytes.NewReader([]byte("a")))

	ch, _ := Iterate(m, "")
	cha, chb := Duplicate(ch)
	akeys := collectAll(cha)
	bkeys := collectAll(chb)
	if len(akeys) != 3 || len(bkeys) != 3 {
		t.Errorf("length %d %d", len(akeys), len(bkeys))
		t.FailNow()
	}
	if !reflect.DeepEqual(akeys, []string{"a", "aa", "b"}) {
		t.Errorf("result wrong")
		t.FailNow()
	}
}

func TestSync(t *testing.T) {
	a := object.CreateStorage("mem", "", "", "")
	a.Put("a", bytes.NewReader([]byte("a")))
	a.Put("b", bytes.NewReader([]byte("a")))
	b := object.CreateStorage("mem", "", "", "")
	b.Put("aa", bytes.NewReader([]byte("a")))

	err := SyncAll(a, b, "")
	if err != nil {
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

	err = SyncAll(a, b, "")
	if err != nil {
		t.FailNow()
	}
	if copied != 3 {
		t.FailNow()
	}
}
