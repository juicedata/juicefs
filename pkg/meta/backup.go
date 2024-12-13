/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package meta

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"unsafe"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	BakMagic   = 0x747083
	BakVersion = 1
	BakEOS     = BakMagic // end of segments
)

const (
	segTypeUnknown = iota
	segTypeFormat
	segTypeCounter
	segTypeNode
	segTypeEdge
	segTypeChunk
	segTypeSliceRef
	segTypeSymlink
	segTypeSustained
	segTypeDelFile
	segTypeXattr
	segTypeAcl
	segTypeStat
	segTypeQuota
	segTypeParent // for redis/tkv only
	segTypeMax
)

var errBakEOF = fmt.Errorf("reach backup EOF")

func getMessageFromType(typ int) (proto.Message, error) {
	var name protoreflect.FullName
	if typ == segTypeFormat {
		name = proto.MessageName(&pb.Format{})
	} else if typ < segTypeMax {
		name = proto.MessageName(&pb.Batch{})
	}
	if name == "" {
		return nil, fmt.Errorf("unknown message type %d", typ)
	}
	return createMessageByName(name)
}

func createMessageByName(name protoreflect.FullName) (proto.Message, error) {
	typ, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		return nil, fmt.Errorf("failed to find message %s's type: %v", name, err)
	}
	return typ.New().Interface(), nil
}

// bakFormat: BakSegment... + BakEOS + BakFooter
type bakFormat struct {
	pos    uint64
	footer *bakFooter
}

func newBakFormat() *bakFormat {
	return &bakFormat{
		footer: &bakFooter{
			msg: &pb.Footer{
				Magic:   BakMagic,
				Version: BakVersion,
				Infos:   make(map[string]*pb.Footer_SegInfo),
			},
		},
	}
}

func (f *bakFormat) writeSegment(w io.Writer, seg *bakSegment) error {
	if seg == nil {
		return nil
	}

	n, err := seg.Marshal(w)
	if err != nil {
		return fmt.Errorf("failed to marshal segment %s: %v", seg, err)
	}

	name := seg.String()
	info, ok := f.footer.msg.Infos[name]
	if !ok {
		info = &pb.Footer_SegInfo{Offset: []uint64{}, Num: 0}
		f.footer.msg.Infos[name] = info
	}

	info.Offset = append(info.Offset, f.pos)
	info.Num += seg.num()
	f.pos += uint64(n)
	return nil
}

func (f *bakFormat) readSegment(r io.Reader) (*bakSegment, error) {
	seg := &bakSegment{}
	if err := seg.Unmarshal(r); err != nil {
		return nil, err
	}
	return seg, nil
}

func (f *bakFormat) writeFooter(w io.Writer) error {
	if err := f.writeEOS(w); err != nil {
		return err
	}
	return f.footer.Marshal(w)
}

func (f *bakFormat) writeEOS(w io.Writer) error {
	if n, err := w.Write(binary.BigEndian.AppendUint32(nil, BakEOS)); err != nil && n != 4 {
		return fmt.Errorf("failed to write EOS: err %w, write len %d, expect len 4", err, n)
	}
	return nil
}

func (f *bakFormat) readFooter(r io.ReadSeeker) (*bakFooter, error) { // nolint:unused
	footer := &bakFooter{}
	if err := footer.Unmarshal(r); err != nil {
		return nil, err
	}
	if footer.msg.Magic != BakMagic {
		return nil, fmt.Errorf("invalid magic number %d, expect %d", footer.msg.Magic, BakMagic)
	}
	f.footer = footer
	return footer, nil
}

type bakFooter struct {
	msg *pb.Footer
	len uint64
}

func (h *bakFooter) Marshal(w io.Writer) error {
	data, err := proto.Marshal(h.msg)
	if err != nil {
		return fmt.Errorf("failed to marshal footer: %w", err)
	}

	if n, err := w.Write(data); err != nil && n != len(data) {
		return fmt.Errorf("failed to write footer data: err %w, write len %d, expect len %d", err, n, len(data))
	}

	h.len = uint64(len(data))
	if n, err := w.Write(binary.BigEndian.AppendUint64(nil, h.len)); err != nil && n != 8 {
		return fmt.Errorf("failed to write footer length: err %w, write len %d, expect len 8", err, n)
	}
	return nil
}

func (h *bakFooter) Unmarshal(r io.ReadSeeker) error {
	lenSize := int64(unsafe.Sizeof(h.len))
	_, _ = r.Seek(-lenSize, io.SeekEnd)

	data := make([]byte, lenSize)
	if n, err := r.Read(data); err != nil && n != int(lenSize) {
		return fmt.Errorf("failed to read footer length: err %w, read len %d, expect len %d", err, n, lenSize)
	}

	h.len = binary.BigEndian.Uint64(data)
	_, _ = r.Seek(-int64(h.len)-lenSize, io.SeekEnd)
	data = make([]byte, h.len)
	if n, err := r.Read(data); err != nil && n != int(h.len) {
		return fmt.Errorf("failed to read footer: err %w, read len %d, expect len %d", err, n, h.len)
	}

	h.msg = &pb.Footer{}
	if err := proto.Unmarshal(data, h.msg); err != nil {
		return fmt.Errorf("failed to unmarshal footer: %w", err)
	}
	return nil
}

type bakSegment struct {
	typ uint32
	len uint64
	val proto.Message
}

func (s *bakSegment) String() string {
	return fmt.Sprintf("type-%d", s.typ)
}

func (s *bakSegment) num() uint64 {
	switch s.typ {
	case segTypeFormat:
		return 1
	default:
		b := s.val.(*pb.Batch)
		switch s.typ {
		case segTypeCounter:
			return uint64(len(b.Counters))
		case segTypeNode:
			return uint64(len(b.Nodes))
		case segTypeEdge:
			return uint64(len(b.Edges))
		case segTypeChunk:
			return uint64(len(b.Chunks))
		case segTypeSliceRef:
			return uint64(len(b.SliceRefs))
		case segTypeSymlink:
			return uint64(len(b.Symlinks))
		case segTypeSustained:
			return uint64(len(b.Sustained))
		case segTypeDelFile:
			return uint64(len(b.Delfiles))
		case segTypeXattr:
			return uint64(len(b.Xattrs))
		case segTypeAcl:
			return uint64(len(b.Acls))
		case segTypeStat:
			return uint64(len(b.Dirstats))
		case segTypeQuota:
			return uint64(len(b.Quotas))
		case segTypeParent:
			return uint64(len(b.Parents))
		}
		return 0
	}
}

func (s *bakSegment) Marshal(w io.Writer) (int, error) {
	if s == nil || s.val == nil {
		return 0, fmt.Errorf("segment %s is nil", s)
	}

	switch v := s.val.(type) {
	case *pb.Format:
		s.typ = uint32(segTypeFormat)
	case *pb.Batch:
		if v.Counters != nil {
			s.typ = uint32(segTypeCounter)
		} else if v.Sustained != nil {
			s.typ = uint32(segTypeSustained)
		} else if v.Delfiles != nil {
			s.typ = uint32(segTypeDelFile)
		} else if v.Acls != nil {
			s.typ = uint32(segTypeAcl)
		} else if v.Xattrs != nil {
			s.typ = uint32(segTypeXattr)
		} else if v.Quotas != nil {
			s.typ = uint32(segTypeQuota)
		} else if v.Dirstats != nil {
			s.typ = uint32(segTypeStat)
		} else if v.Nodes != nil {
			s.typ = uint32(segTypeNode)
		} else if v.Chunks != nil {
			s.typ = uint32(segTypeChunk)
		} else if v.SliceRefs != nil {
			s.typ = uint32(segTypeSliceRef)
		} else if v.Edges != nil {
			s.typ = uint32(segTypeEdge)
		} else if v.Symlinks != nil {
			s.typ = uint32(segTypeSymlink)
		} else if v.Parents != nil {
			s.typ = uint32(segTypeParent)
		} else {
			return 0, fmt.Errorf("unknown batch type %s", s)
		}
	}

	if err := binary.Write(w, binary.BigEndian, s.typ); err != nil {
		return 0, fmt.Errorf("failed to write segment type %s : %w", s, err)
	}
	data, err := proto.Marshal(s.val)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal segment message %s : %w", s, err)
	}
	s.len = uint64(len(data))
	if err := binary.Write(w, binary.BigEndian, s.len); err != nil {
		return 0, fmt.Errorf("failed to write segment length %s: %w", s, err)
	}

	if n, err := w.Write(data); err != nil || n != len(data) {
		return 0, fmt.Errorf("failed to write segment data %s: err %w, write len %d, expect len %d", s, err, n, len(data))
	}

	return binary.Size(s.typ) + binary.Size(s.len) + len(data), nil
}

func (s *bakSegment) Unmarshal(r io.Reader) error {
	if err := binary.Read(r, binary.BigEndian, &s.typ); err != nil {
		return fmt.Errorf("failed to read segment type: %v", err)
	}

	if s.typ == BakEOS {
		return errBakEOF
	}

	if err := binary.Read(r, binary.BigEndian, &s.len); err != nil {
		return fmt.Errorf("failed to read segment %s length: %v", s, err)
	}
	data := make([]byte, s.len)
	n, err := r.Read(data)
	if err != nil && n != int(s.len) {
		return fmt.Errorf("failed to read segment value: err %v, read len %d, expect len %d", err, n, s.len)
	}

	msg, err := getMessageFromType(int(s.typ))
	if err != nil {
		return fmt.Errorf("failed to create message by type %d: %w", s.typ, err)
	}
	if err = proto.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("failed to unmarshal segment msg %d: %w", s.typ, err)
	}
	s.val = msg
	return nil
}

type DumpOption struct {
	KeepSecret bool
	Threads    int
}

func (opt *DumpOption) check() *DumpOption {
	if opt == nil {
		opt = &DumpOption{}
	}
	if opt.Threads < 1 {
		opt.Threads = 10
	}
	return opt
}

func (m *baseMeta) dumpFormat(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	f := m.GetFormat()
	if !opt.KeepSecret {
		f.RemoveSecret()
	}
	data, err := json.MarshalIndent(f, "", "")
	if err != nil {
		logger.Errorf("failed to marshal format %s: %v", f.Name, err)
		return nil
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Format{Data: data}})
}

type dumpedResult struct {
	msg     proto.Message
	release func(m proto.Message)
}

func dumpResult(ctx context.Context, ch chan<- *dumpedResult, res *dumpedResult) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- res:
		return nil
	}
}

type LoadOption struct {
	Threads int
}

func (opt *LoadOption) check() {
	if opt.Threads < 1 {
		opt.Threads = 10
	}
}

// transaction

type txSessionKey struct{}
type txMaxRetryKey struct{}
