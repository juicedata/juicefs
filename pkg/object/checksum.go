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
	"fmt"
	"hash/crc32"
	"io"
	"reflect"
	"strconv"
)

const checksumAlgr = "Crc32c"

var crc32c = crc32.MakeTable(crc32.Castagnoli)

func generateChecksum(in io.ReadSeeker) string {
	if b, ok := in.(*bytes.Reader); ok {
		v := reflect.ValueOf(b)
		data := v.Elem().Field(0).Bytes()
		return strconv.Itoa(int(crc32.Update(0, crc32c, data)))
	}
	var hash uint32
	crcBuffer := bufPool.Get().(*[]byte)
	defer bufPool.Put(crcBuffer)
	defer func() { _, _ = in.Seek(0, io.SeekStart) }()
	for {
		n, err := in.Read(*crcBuffer)
		hash = crc32.Update(hash, crc32c, (*crcBuffer)[:n])
		if err != nil {
			if err != io.EOF {
				return ""
			}
			break
		}
	}
	return strconv.Itoa(int(hash))
}

type checksumReader struct {
	io.ReadCloser
	expected        uint32
	checksum        uint32
	remainingLength int64
}

func (c *checksumReader) Read(buf []byte) (n int, err error) {
	n, err = c.ReadCloser.Read(buf)
	c.checksum = crc32.Update(c.checksum, crc32c, buf[:n])
	c.remainingLength -= int64(n)
	if (err == io.EOF || c.remainingLength == 0) && c.checksum != c.expected {
		return 0, fmt.Errorf("verify checksum failed: %d != %d", c.checksum, c.expected)
	}
	return
}

func verifyChecksum(in io.ReadCloser, checksum string, contentLength int64) io.ReadCloser {
	if checksum == "" {
		return in
	}
	expected, err := strconv.Atoi(checksum)
	if err != nil {
		logger.Errorf("invalid crc32c: %s", checksum)
		return in
	}
	return &checksumReader{in, uint32(expected), 0, contentLength}
}
