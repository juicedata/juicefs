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
	"crypto/rand"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"sync"
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

func TestChecksumRead(t *testing.T) {
	content := make([]byte, 10240)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("Generate random content: %s", err)
	}
	actual := generateChecksum(bytes.NewReader(content))

	// content length equal buff length case
	reader := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, int64(len(content)))
	n, err := reader.Read(make([]byte, 10240))
	if n != 10240 || (err != nil && err != io.EOF) {
		t.Fatalf("verify checksum shuold success")
	}

	// verify success case
	err = nil
	reader1 := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, int64(len(content)))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := make([]byte, 50)
			for i := 0; i < 100; i++ {
				_, err = reader1.Read(body)
				if err == io.EOF {
					err = nil
				}
			}
		}()
	}
	wg.Wait()
	if err != nil {
		t.Fatalf("verify checksum shuold success %s", err)
	}

	// verify failed case
	content[0] = 'a'
	reader2 := verifyChecksum(io.NopCloser(bytes.NewReader(content)), actual, int64(len(content)))
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := make([]byte, 50)
			for i := 0; i < 100; i++ {
				_, err = reader2.Read(body)
				if err == io.EOF {
					err = nil
				}
			}
		}()
	}
	wg.Wait()

	if err == nil || !strings.HasPrefix(err.Error(), "verify checksum failed") {
		t.Fatalf("verify checksum should failed")
	}
}
