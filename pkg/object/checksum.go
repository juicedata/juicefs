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
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"strconv"
)

const checksumAlgr = "Crc32c"

var crc32c = crc32.MakeTable(crc32.Castagnoli)

// generateChecksum returns the Castagnoli CRC32 of the full content of
// `in`, restoring the reader's position when it returns so the caller
// can replay the body to the HTTP layer.
//
// The encoding is unsigned decimal (FormatUint of uint64(sum)) so the
// value is never platform-dependent. The legacy code used
// strconv.Itoa(int(uint32(sum))) which produced negative numbers on
// 32-bit platforms (where `int` is int32 and the high CRC bit wraps);
// verifyChecksum0 still accepts that legacy encoding on read for
// backward compatibility, but new writes use the unsigned form.
func generateChecksum(in io.ReadSeeker) string {
	pos, err := in.Seek(0, io.SeekCurrent)
	if err != nil {
		logger.Errorf("checksum seek current: %s", err)
		return ""
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		logger.Errorf("checksum seek start: %s", err)
		return ""
	}
	defer func() { _, _ = in.Seek(pos, io.SeekStart) }()

	var hash uint32
	crcBuffer := bufPool.Get().(*[]byte)
	defer bufPool.Put(crcBuffer)
	for {
		n, err := in.Read(*crcBuffer)
		hash = crc32.Update(hash, crc32c, (*crcBuffer)[:n])
		if err != nil {
			if err != io.EOF {
				logger.Errorf("checksum read: %s", err)
				return ""
			}
			break
		}
	}
	return strconv.FormatUint(uint64(hash), 10)
}

// parseChecksum decodes a stored CRC32 string into a uint32. It accepts
// both the new unsigned encoding (FormatUint) and the legacy signed
// 32-bit encoding (Itoa(int(uint32(sum))) on a 32-bit host, which wraps
// the high bit). Returns false if the string is empty, malformed, or
// outside the [MinInt32, MaxUint32] range that any valid encoding could
// produce.
func parseChecksum(s string) (uint32, bool) {
	if s == "" {
		return 0, false
	}
	// ParseInt with 64-bit width accepts both negative (legacy) and
	// positive (current) encodings; the uint32 cast below truncates
	// either back to the original CRC bits.
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	if v < math.MinInt32 || v > math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}

type checksumReader struct {
	io.ReadCloser
	expected        uint32
	checksum        uint32
	remainingLength int64
	table           *crc32.Table
}

func (c *checksumReader) Read(buf []byte) (n int, err error) {
	n, err = c.ReadCloser.Read(buf)
	c.checksum = crc32.Update(c.checksum, c.table, buf[:n])
	c.remainingLength -= int64(n)
	if (err == io.EOF || c.remainingLength == 0) && c.checksum != c.expected {
		return 0, fmt.Errorf("verify checksum failed: %d != %d", c.checksum, c.expected)
	}
	return
}

// uncheckableReader wraps a body whose checksum header was unparseable.
// Bytes still flow through earlier Reads, but EOF is replaced with an
// explicit error so a downstream caller cannot mistake the unverified
// data for a successful integrity-checked read. Matches the
// fail-closed pattern of checksumReader.
type uncheckableReader struct {
	io.ReadCloser
	raw string
}

func (u *uncheckableReader) Read(buf []byte) (n int, err error) {
	n, err = u.ReadCloser.Read(buf)
	if err == io.EOF {
		return 0, fmt.Errorf("verify checksum: stored CRC32 %q is malformed", u.raw)
	}
	return
}

func verifyChecksum(in io.ReadCloser, checksum string, contentLength int64) io.ReadCloser {
	return verifyChecksum0(in, checksum, contentLength, crc32c)
}

func verifyChecksum0(in io.ReadCloser, checksum string, contentLength int64, table *crc32.Table) io.ReadCloser {
	if checksum == "" {
		return in
	}
	expected, ok := parseChecksum(checksum)
	if !ok {
		logger.Errorf("invalid crc32c %q; verification will fail at EOF", checksum)
		return &uncheckableReader{ReadCloser: in, raw: checksum}
	}
	return &checksumReader{in, expected, 0, contentLength, table}
}
