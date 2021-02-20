package object

import (
	"bytes"
	"hash/crc32"
	"strconv"
	"testing"
)

func TestChecksum(t *testing.T) {
	b := []byte("hello")
	expected := crc32.Update(0, crc32c, b)
	actual := generateChecksum(bytes.NewReader(b))
	if actual != strconv.Itoa(int(expected)) {
		t.Errorf("expect %d but got %s", expected, actual)
		t.FailNow()
	}

	actual = generateChecksum(bytes.NewReader(b))
	if actual != strconv.Itoa(int(expected)) {
		t.Errorf("expect %d but got %s", expected, actual)
		t.FailNow()
	}
}
