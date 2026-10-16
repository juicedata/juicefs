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
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	checksumAlgr       = "Crc32c"
	chunksChecksumAlgr = "Crc32c-chunks"
	ChunkChecksumSize  = 32 << 10 // CRC32C segment size in bytes.
	maxChunkChecksums  = 352      // ~11 MiB; keep whole user metadata within 2 KiB limit.
)

type checksumMode uint8

const (
	checksumFull checksumMode = iota
	checksumNone
	checksumExtend
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

var ErrChecksum = errors.New("checksum mismatch")

func parseChecksumMode(query url.Values) (checksumMode, error) {
	level := query.Get("verify-checksum")
	if strings.EqualFold(query.Get("disable-checksum"), "true") { // backward compatibility for old versions
		if level != "none" {
			logger.Warnf("disable-checksum=true overrides verify-checksum=%q, will disable checksum", level)
		}
		level = "none"
	}
	if level == "" {
		level = "full" // default mode
	}
	switch level {
	case "none":
		logger.Infof("Default CRC checksum is disabled")
		return checksumNone, nil
	case "extend":
		logger.Infof("Enable CRC checksum protection for object full and range reads")
		return checksumExtend, nil
	case "full":
		return checksumFull, nil
	default:
		return 0, fmt.Errorf("verify-checksum must be none, full or extend, got %q", level)
	}
}

type checksumReader struct {
	io.ReadCloser
	key             string
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
		err = fmt.Errorf("verify checksum failed: %d != %d", c.checksum, c.expected)
		logger.Warnf("Read %q (full object): %s", c.key, err)
		return 0, err
	}
	return
}

func verifyChecksum(in io.ReadCloser, checksum string, contentLength int64, key string) io.ReadCloser {
	return verifyChecksum0(in, checksum, contentLength, crc32c, key)
}

func verifyChecksum0(in io.ReadCloser, checksum string, contentLength int64, table *crc32.Table, key string) io.ReadCloser {
	if checksum == "" {
		return in
	}
	expected, err := strconv.Atoi(checksum)
	if err != nil {
		logger.Errorf("invalid crc32c: %s", checksum)
		return in
	}
	return &checksumReader{ReadCloser: in, key: key, expected: uint32(expected), remainingLength: contentLength, table: table}
}

// chunkCount returns number of crc chunks covering size bytes.
func chunkCount(size int64) int64 {
	if size == 0 {
		return 0
	}
	return 1 + (size-1)/ChunkChecksumSize
}

// chunkChecksums returns one big-endian CRC32C per ChunkChecksumSize bytes.
func chunkChecksums(data []byte) []byte {
	sums := make([]byte, 0, chunkCount(int64(len(data)))*crc32.Size)
	for len(data) > 0 {
		n := len(data)
		if n > ChunkChecksumSize {
			n = ChunkChecksumSize
		}
		sums = binary.BigEndian.AppendUint32(sums, crc32.Checksum(data[:n], crc32c))
		data = data[n:]
	}
	return sums
}

// generateChecksums emits one checksum format, or none when disabled.
func generateChecksums(in io.ReadSeeker, mode checksumMode) (metadata map[string]string, err error) {
	if mode == checksumNone {
		return nil, nil
	}
	if b, ok := in.(*bytes.Reader); ok {
		data := reflect.ValueOf(b).Elem().Field(0).Bytes()
		data = data[len(data)-b.Len():]
		if mode == checksumExtend && len(data) > 0 && len(data) <= maxChunkChecksums*ChunkChecksumSize {
			return map[string]string{chunksChecksumAlgr: base64.StdEncoding.EncodeToString(chunkChecksums(data))}, nil
		}
		return map[string]string{checksumAlgr: strconv.FormatUint(uint64(crc32.Checksum(data, crc32c)), 10)}, nil
	}
	pos, err := in.Seek(0, io.SeekCurrent) // save current position
	if err != nil {
		return nil, err
	}
	defer func() {
		if _, seekErr := in.Seek(pos, io.SeekStart); err == nil {
			err = seekErr
		}
	}()
	end, err := in.Seek(0, io.SeekEnd) // seek to EOF to determine how much data remains
	if err != nil {
		return nil, err
	}
	if end < pos {
		return nil, fmt.Errorf("reader offset %d exceeds size %d", pos, end)
	}
	if _, err = in.Seek(pos, io.SeekStart); err != nil { // restore from EOF
		return nil, err
	}
	remaining := end - pos
	chunkedCRC := mode == checksumExtend && remaining > 0 && remaining <= int64(maxChunkChecksums)*ChunkChecksumSize
	var sums []byte
	if chunkedCRC {
		sums = make([]byte, 0, chunkCount(remaining)*crc32.Size)
	}
	buf := bufPool.Get().(*[]byte) // 1 MiB reads preserve 32 KiB chunk boundaries.
	defer bufPool.Put(buf)
	var crc uint32
	for {
		n, readErr := io.ReadFull(in, *buf)
		if int64(n) > remaining {
			return nil, fmt.Errorf("reader grew while calculating checksum")
		}
		remaining -= int64(n)
		data := (*buf)[:n]
		if chunkedCRC {
			sums = append(sums, chunkChecksums(data)...)
		} else {
			crc = crc32.Update(crc, crc32c, data)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			if remaining != 0 {
				return nil, io.ErrUnexpectedEOF
			}
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if chunkedCRC {
		return map[string]string{chunksChecksumAlgr: base64.StdEncoding.EncodeToString(sums)}, nil
	}
	return map[string]string{checksumAlgr: strconv.FormatUint(uint64(crc), 10)}, nil
}

// alignChunkRange expands [off, off+limit) to chunk boundaries; limit <= 0 reads to EOF.
func alignChunkRange(off, limit int64) (start, end int64, err error) {
	if off < 0 || limit > 0 && limit > math.MaxInt64-off-(ChunkChecksumSize-1) {
		return 0, 0, fmt.Errorf("invalid checksum range: offset %d, length %d", off, limit)
	}
	start = off / ChunkChecksumSize * ChunkChecksumSize
	if limit <= 0 {
		return start, -1, nil
	}
	return start, chunkCount(off+limit) * ChunkChecksumSize, nil
}

// checksumRangeReader validates object metadata and selects CRCs for this response.
func checksumRangeReader(body io.ReadCloser, key, crcChunks, contentRange string, contentLength, off, limit int64) (io.ReadCloser, error) {
	start, end, err := alignChunkRange(off, limit)
	if err != nil {
		return nil, err
	}
	total := contentLength
	if off > 0 || limit > 0 {
		_, objectSize, _ := strings.Cut(contentRange, "/")
		total, err = strconv.ParseInt(objectSize, 10, 64)
		if err != nil || off >= total {
			return nil, fmt.Errorf("invalid content range %q for offset %d", contentRange, off)
		}
		if end < 0 || end > total {
			end = total
		}
		if contentRange != fmt.Sprintf("bytes %d-%d/%d", start, end-1, total) || contentLength != end-start {
			return nil, fmt.Errorf("content range %q, length %d does not match requested range %d-%d", contentRange, contentLength, start, end-1)
		}
	} else if contentRange != "" {
		return nil, fmt.Errorf("unexpected content range %q for full get", contentRange)
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("invalid content length %d", contentLength)
	}
	var sums []byte
	if crcChunks != "" {
		sums, err = base64.StdEncoding.DecodeString(crcChunks)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", chunksChecksumAlgr, err)
		}
		count := chunkCount(total)
		if int64(len(sums)) != count*crc32.Size {
			return nil, fmt.Errorf("invalid %s length %d for object size %d", chunksChecksumAlgr, len(sums), total)
		}
		last := chunkCount(start + contentLength)
		sums = sums[start/ChunkChecksumSize*crc32.Size : last*crc32.Size]
	}
	head := off - start
	length := contentLength - head
	if limit > 0 && limit < length {
		length = limit
	}
	return NewChunkChecksumReader(body, key, sums, contentLength, head, length), nil
}

// NewChunkChecksumReader verifies an aligned response and returns [head, head+length).
// Caller must validate 0 <= head <= wireLen and 0 <= length <= wireLen-head.
func NewChunkChecksumReader(body io.ReadCloser, key string, checksums []byte, wireLen, head, length int64) io.ReadCloser {
	return &chunkChecksumReader{ReadCloser: body, key: key, expected: checksums, wireLen: wireLen, head: head, remaining: length}
}

type chunkChecksumReader struct {
	io.ReadCloser
	key       string
	wireLen   int64
	head      int64
	remaining int64
	expected  []byte
	consumed  int64
	crc       uint32
	err       error
}

func (r *chunkChecksumReader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	defer func() { r.err = err }()
	if r.head > 0 {
		if err = r.skip(r.head); err != nil {
			return 0, err
		}
		r.head = 0
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err = r.ReadCloser.Read(p)
	if verr := r.verify(p[:n]); verr != nil {
		return 0, verr
	}
	r.remaining -= int64(n)
	if err == io.EOF && r.consumed < r.wireLen {
		return 0, io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF {
		return 0, err
	}
	if r.remaining == 0 {
		// The final Read consumes tail padding so io.ReadFull cannot miss a checksum error.
		if err = r.skip(r.wireLen - r.consumed); err != nil {
			return 0, err
		}
	}
	return n, err
}

func (r *chunkChecksumReader) skip(n int64) error {
	if n == 0 {
		return nil
	}
	buf := utils.Alloc0(ChunkChecksumSize)
	defer utils.Free0(buf)
	for n > 0 {
		m := int64(len(buf))
		if n < m {
			m = n
		}
		if _, err := io.ReadFull(r.ReadCloser, buf[:m]); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		if err := r.verify(buf[:m]); err != nil {
			return err
		}
		n -= m
	}
	return nil
}

func (r *chunkChecksumReader) verify(data []byte) error {
	if r.expected == nil {
		r.consumed += int64(len(data))
		return nil
	}
	for len(data) > 0 {
		n := ChunkChecksumSize - r.consumed%ChunkChecksumSize
		if int64(len(data)) < n {
			n = int64(len(data))
		}
		r.crc = crc32.Update(r.crc, crc32c, data[:n])
		r.consumed += n
		data = data[n:]
		if r.consumed%ChunkChecksumSize == 0 || r.consumed == r.wireLen {
			i := (r.consumed - 1) / ChunkChecksumSize
			if expect := binary.BigEndian.Uint32(r.expected[i*crc32.Size:]); r.crc != expect {
				err := fmt.Errorf("%w: response offset %d crc %d != expect %d", ErrChecksum, i*ChunkChecksumSize, r.crc, expect)
				logger.Warnf("Read %q: %s", r.key, err)
				return err
			}
			r.crc = 0
		}
	}
	return nil
}
