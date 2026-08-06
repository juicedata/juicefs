/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package chunk

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	CsNone   = "none"
	CsFull   = "full"
	CsShrink = "shrink"
	CsExtend = "extend"

	csBlock = 32 << 10
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

const maxTierID = 3 // maximum valid tier id

// stageFooter is msgpack metadata appended at the end of a stage file:
//
//   - file content: [data] [checksum?] [footer]
//   - footer: [magic uint16] [len uint16] [msgpack data]
//
// We use trailer length parity to indicate
// checksum presence (checksum bytes are always multiple of 4):
//   - with checksum: msgpack length is padded to a multiple of 4
//   - without checksum: msgpack length is padded to a non-multiple of 4
//
// Therefore, trailer length (checksum + msgpack) is multiple of 4 iff checksum
// exists, so openCacheFile can distinguish the two and find the trailing
// msgpack metadata.
type stageFooter struct {
	Tier uint8  `msgpack:"tier"`
	Pad  []byte `msgpack:"pad"` // pad to make msgpack length parity match checksum presence
}

func (f *stageFooter) marshal(align bool) ([]byte, error) {
	var p int
	switch r := stageFooterBaseLen % 4; {
	case align:
		p = (4 - r) % 4 // pad up to the next multiple of 4
	case r == 0:
		p = 1 // break the multiple-of-4 alignment
	}
	f.Pad = stageFooterPad[p]
	data, err := msgpack.Marshal(f)
	if err != nil {
		return nil, err
	}

	buff := make([]byte, 0, 4+len(data))
	buff = binary.BigEndian.AppendUint16(buff, stageFooterMagic)
	buff = binary.BigEndian.AppendUint16(buff, uint16(len(data)))
	buff = append(buff, data...)
	return buff, nil
}

func (f *stageFooter) unmarshal(cf *cacheFile) error {
	if cf.footerOff == 0 {
		f.Tier = 0 // no footer, fall back to the default
		return nil
	}
	var hdr [4]byte
	if _, err := cf.File.ReadAt(hdr[:], cf.footerOff); err != nil {
		return fmt.Errorf("read stage footer header: %w", err)
	}
	if magic := binary.BigEndian.Uint16(hdr[:2]); magic != stageFooterMagic {
		return fmt.Errorf("invalid stage footer magic %#04x", magic)
	}
	size := int64(binary.BigEndian.Uint16(hdr[2:4]))
	if size == 0 {
		return fmt.Errorf("invalid stage footer length %d", size)
	}
	if cf.footerOff+4+size != cf.size {
		return fmt.Errorf("stage footer size mismatch: offset %d, size %d, file size %d", cf.footerOff, size, cf.size)
	}
	buf := make([]byte, size)
	if _, err := cf.File.ReadAt(buf, cf.footerOff+4); err != nil {
		return fmt.Errorf("read stage footer data: %w", err)
	}
	if err := msgpack.Unmarshal(buf, f); err != nil {
		return err
	}
	if f.Tier > maxTierID {
		f.Tier = 0 // ignore unknown/corrupted tier, fall back to the default
	}
	return nil
}

var (
	stageFooterBaseLen int
	stageFooterPad     [4][]byte
	stageFooterMagic   = uint16(0x4653)
)

func init() {
	b, err := msgpack.Marshal(&stageFooter{Pad: make([]byte, 0)})
	if err != nil {
		panic(err)
	}
	stageFooterBaseLen = len(b)

	for i := 0; i < len(stageFooterPad); i++ {
		stageFooterPad[i] = make([]byte, i)
	}
}

type cacheFile struct {
	*os.File
	length    int // length of data
	csLevel   string
	size      int64 // size of file
	footerOff int64 // offset of stageFooter in file (0 if none)
}

// Calculate 32-bits checksum for every 32 KiB data, so 512 Bytes for 4 MiB in total
func checksum(data []byte) []byte {
	length := len(data)
	buf := utils.NewBuffer(uint32((length-1)/csBlock+1) * 4)
	for start, end := 0, 0; start < length; start = end {
		end = start + csBlock
		if end > length {
			end = length
		}
		sum := crc32.Checksum(data[start:end], crc32c)
		buf.Put32(sum)
	}
	return buf.Bytes()
}

func openCacheFile(name string, length int, level string) (*cacheFile, error) {
	fp, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := fp.Stat()
	if err != nil {
		_ = fp.Close()
		return nil, err
	}
	checksumLength := int64(((length-1)/csBlock + 1) * 4)
	extra := fi.Size() - int64(length)
	if extra < 0 {
		_ = fp.Close()
		return nil, fmt.Errorf("invalid file size %d, data length %d", fi.Size(), length)
	}
	footerOff := int64(0)
	switch {
	case extra == 0:
		level = CsNone // data only, no footer
	case extra%4 == 0:
		switch {
		case extra == checksumLength:
		case extra > checksumLength:
			footerOff = int64(length) + checksumLength
		default:
			_ = fp.Close()
			return nil, fmt.Errorf("invalid file size %d, data length %d, checksum length %d", fi.Size(), length, checksumLength)
		}
	default:
		level = CsNone
		footerOff = int64(length)
	}
	cf := &cacheFile{File: fp, length: length, csLevel: level, footerOff: footerOff, size: fi.Size()}
	return cf, nil
}

func (cf *cacheFile) ReadAt(b []byte, off int64) (n int, err error) {
	logger.Tracef("CacheFile length %d level %s, readat off %d buffer size %d", cf.length, cf.csLevel, off, len(b))
	defer func() {
		logger.Tracef("CacheFile readat returns n %d err %s", n, err)
	}()
	if cf.csLevel == CsNone || cf.csLevel == CsFull && (off != 0 || len(b) != cf.length) {
		return cf.File.ReadAt(b, off)
	}
	var rb = b     // read buffer
	var roff = off // read offset
	if cf.csLevel == CsExtend {
		roff = off / csBlock * csBlock
		rend := int(off) + len(b)
		if rend%csBlock != 0 {
			rend = (rend/csBlock + 1) * csBlock
			if rend > cf.length {
				rend = cf.length
			}
		}
		if size := rend - int(roff); size != len(b) {
			p := NewOffPage(size)
			rb = p.Data
			defer func() {
				if err == nil {
					n = copy(b, rb[off-roff:])
				} else {
					n = 0
				}
				p.Release()
			}()
		}
	}
	if n, err = cf.File.ReadAt(rb, roff); err != nil {
		return
	}

	ioff := int(roff) / csBlock // index offset
	if cf.csLevel == CsShrink {
		if roff%csBlock != 0 {
			if o := csBlock - int(roff)%csBlock; len(rb) <= o {
				return
			} else {
				rb = rb[o:]
				ioff += 1
			}
		}
		if end := int(roff) + n; end != cf.length && end%csBlock != 0 {
			if len(rb) <= end%csBlock {
				return
			}
			rb = rb[:len(rb)-end%csBlock]
		}
	}
	// now rb contains the data to check
	length := len(rb)
	buf := utils.NewBuffer(uint32((length-1)/csBlock+1) * 4)
	if _, err = cf.File.ReadAt(buf.Bytes(), int64(cf.length+ioff*4)); err != nil {
		logger.Warnf("Read checksum of data length %d checksum offset %d: %s", length, cf.length+ioff*4, err)
		return
	}
	for start, end := 0, 0; start < length; start = end {
		end = start + csBlock
		if end > length {
			end = length
		}
		sum := crc32.Checksum(rb[start:end], crc32c)
		expect := buf.Get32()
		logger.Debugf("Cache file read data start %d end %d checksum %d, expected %d", start, end, sum, expect)
		if sum != expect {
			err = fmt.Errorf("data checksum %d != expect %d", sum, expect)
			break
		}
	}
	return
}
