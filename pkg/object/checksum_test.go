/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package object

import (
	"bytes"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/juicedata/juicefs/pkg/utils"
)

func TestChecksum(t *testing.T) {
	b := []byte("hello")
	expected := crc32.Update(0, crc32c, b)
	want := strconv.FormatUint(uint64(expected), 10)
	for i := 0; i < 2; i++ {
		actual := generateChecksum(bytes.NewReader(b))
		if actual != want {
			t.Errorf("attempt %d: expect %s but got %s", i, want, actual)
			t.FailNow()
		}
	}
}

func TestParseChecksumRoundTripHighBitCRC(t *testing.T) {
	// The legacy encoding (Itoa(int(uint32(sum)))) wrapped any CRC32
	// value with the high bit set to a negative integer on 32-bit
	// platforms. parseChecksum must accept BOTH the new unsigned form
	// and the historical signed form so existing buckets keep verifying.
	// Use a runtime variable (not a const) so the int32 conversion
	// below doesn't trigger a compile-time overflow check.
	var highBit uint32 = 0x80000001
	// New encoding.
	newForm := strconv.FormatUint(uint64(highBit), 10)
	got, ok := parseChecksum(newForm)
	if !ok || got != highBit {
		t.Fatalf("new-form parse: got=%d ok=%v want=%d", got, ok, highBit)
	}
	// Legacy 32-bit encoding (negative because int32 wraps the high bit).
	legacy := strconv.FormatInt(int64(int32(highBit)), 10)
	got, ok = parseChecksum(legacy)
	if !ok || got != highBit {
		t.Fatalf("legacy-form parse: got=%d ok=%v want=%d", got, ok, highBit)
	}
}

func TestParseChecksumRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "abc", "0xCAFE", "12.5", "99999999999", "-99999999999"} {
		if _, ok := parseChecksum(s); ok {
			t.Errorf("parseChecksum(%q) accepted; want rejection", s)
		}
	}
}

func TestVerifyChecksumUnparseableFailsOnEOF(t *testing.T) {
	// A stored checksum that cannot be parsed must NOT silently pass
	// the bytes through — that would mean any corruption of the
	// checksum metadata itself disables verification for the object.
	body := []byte("hello world")
	reader := verifyChecksum(io.NopCloser(bytes.NewReader(body)), "not-a-number", int64(len(body)))

	// Bytes flow through any prior Reads.
	buf := make([]byte, len(body))
	if n, err := reader.Read(buf); n != len(body) || (err != nil && err != io.EOF) {
		t.Fatalf("first read: n=%d err=%v", n, err)
	}
	// EOF (next read) must surface a verification error, not io.EOF.
	if _, err := reader.Read(buf); err == nil || err == io.EOF {
		t.Fatalf("EOF read should surface verification error; got %v", err)
	} else if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error should mention malformed checksum; got %v", err)
	}
}

func TestGenerateChecksumPreservesPosition(t *testing.T) {
	// generateChecksum must leave the reader positioned exactly where
	// it found it so callers can keep streaming the same body to the
	// HTTP layer. Test with a non-zero starting position to catch any
	// regression that silently rewinds to 0.
	r := bytes.NewReader([]byte("abcdefghij"))
	if _, err := r.Seek(3, io.SeekStart); err != nil {
		t.Fatalf("seed seek: %s", err)
	}
	_ = generateChecksum(r)
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("position check: %s", err)
	}
	if pos != 3 {
		t.Fatalf("position not restored: got %d, want 3", pos)
	}
}

func TestChecksumRead(t *testing.T) {
	length := 10240
	content := make([]byte, length)
	utils.RandRead(content)
	actual := generateChecksum(bytes.NewReader(content))

	// content length equal buff length case
	lens := []int64{-1, int64(length)}
	for _, contentLength := range lens {
		reader := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, contentLength)
		n, err := reader.Read(make([]byte, length))
		if n != length || (err != nil && err != io.EOF) {
			t.Fatalf("verify checksum should success")
		}
	}

	// verify success case
	for _, contentLength := range lens {
		reader := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, contentLength)
		n, err := reader.Read(make([]byte, length+100))
		if n != length || (err != nil && err != io.EOF) {
			t.Fatalf("verify checksum should success")
		}
	}

	// verify failed case
	for _, contentLength := range lens {
		content[0] = 'a'
		reader := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, contentLength)
		n, err := reader.Read(make([]byte, length))
		if contentLength == -1 && (err != nil && err != io.EOF || n != length) {
			t.Fatalf("dont verify checksum when content length is -1")
		}
		if contentLength != -1 && (err == nil || err == io.EOF || !strings.HasPrefix(err.Error(), "verify checksum failed")) {
			t.Fatalf("verify checksum should failed,err %s contentLength %d", err.Error(), contentLength)
		}
	}

	// verify read length less than content length case
	for _, contentLength := range lens {
		reader := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, contentLength)
		n, err := reader.Read(make([]byte, length-100))
		if err != nil || n != length-100 {
			t.Fatalf("error should be nil and read length should be %d", length-100)
		}
	}
}
