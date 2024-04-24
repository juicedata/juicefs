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
			t.Fatalf("verify checksum should failed")
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
