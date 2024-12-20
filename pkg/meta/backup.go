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
	"google.golang.org/protobuf/encoding/protojson"
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

var SegType2Name = map[int]string{
	segTypeFormat:    "format",
	segTypeCounter:   "counter",
	segTypeNode:      "node",
	segTypeEdge:      "edge",
	segTypeChunk:     "chunk",
	segTypeSliceRef:  "sliceRef",
	segTypeSymlink:   "symlink",
	segTypeSustained: "sustained",
	segTypeDelFile:   "delFile",
	segTypeXattr:     "xattr",
	segTypeAcl:       "acl",
	segTypeStat:      "stat",
	segTypeQuota:     "quota",
	segTypeParent:    "parent",
}

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

// BakFormat: BakSegment... + BakEOS + BakFooter
type BakFormat struct {
	Pos    uint64
	Footer *BakFooter
}

func newBakFormat() *BakFormat {
	return &BakFormat{
		Footer: &BakFooter{
			Msg: &pb.Footer{
				Magic:   BakMagic,
				Version: BakVersion,
				Infos:   make(map[string]*pb.Footer_SegInfo),
			},
		},
	}
}

func (f *BakFormat) writeSegment(w io.Writer, seg *BakSegment) error {
	if seg == nil {
		return nil
	}

	n, err := seg.Marshal(w)
	if err != nil {
		return fmt.Errorf("failed to marshal segment %s: %v", seg, err)
	}

	name := seg.Name()
	info, ok := f.Footer.Msg.Infos[name]
	if !ok {
		info = &pb.Footer_SegInfo{Offset: []uint64{}, Num: 0}
		f.Footer.Msg.Infos[name] = info
	}

	info.Offset = append(info.Offset, f.Pos)
	info.Num += seg.num()
	f.Pos += uint64(n)
	return nil
}

func (f *BakFormat) ReadSegment(r io.Reader) (*BakSegment, error) {
	seg := &BakSegment{}
	if err := seg.Unmarshal(r); err != nil {
		return nil, err
	}
	return seg, nil
}

func (f *BakFormat) writeFooter(w io.Writer) error {
	if err := f.writeEOS(w); err != nil {
		return err
	}
	return f.Footer.Marshal(w)
}

func (f *BakFormat) writeEOS(w io.Writer) error {
	if n, err := w.Write(binary.BigEndian.AppendUint32(nil, BakEOS)); err != nil && n != 4 {
		return fmt.Errorf("failed to write EOS: err %w, write len %d, expect len 4", err, n)
	}
	return nil
}

func (f *BakFormat) ReadFooter(r io.ReadSeeker) (*BakFooter, error) { // nolint:unused
	footer := &BakFooter{}
	if err := footer.Unmarshal(r); err != nil {
		return nil, err
	}
	if footer.Msg.Magic != BakMagic {
		return nil, fmt.Errorf("invalid magic number %d, expect %d", footer.Msg.Magic, BakMagic)
	}
	f.Footer = footer
	return footer, nil
}

type BakFooter struct {
	Msg *pb.Footer
	Len uint64
}

func (h *BakFooter) Marshal(w io.Writer) error {
	data, err := proto.Marshal(h.Msg)
	if err != nil {
		return fmt.Errorf("failed to marshal footer: %w", err)
	}

	if n, err := w.Write(data); err != nil && n != len(data) {
		return fmt.Errorf("failed to write footer data: err %w, write len %d, expect len %d", err, n, len(data))
	}

	h.Len = uint64(len(data))
	if n, err := w.Write(binary.BigEndian.AppendUint64(nil, h.Len)); err != nil && n != 8 {
		return fmt.Errorf("failed to write footer length: err %w, write len %d, expect len 8", err, n)
	}
	return nil
}

func (h *BakFooter) Unmarshal(r io.ReadSeeker) error {
	lenSize := int64(unsafe.Sizeof(h.Len))
	_, _ = r.Seek(-lenSize, io.SeekEnd)

	data := make([]byte, lenSize)
	if n, err := r.Read(data); err != nil && n != int(lenSize) {
		return fmt.Errorf("failed to read footer length: err %w, read len %d, expect len %d", err, n, lenSize)
	}

	h.Len = binary.BigEndian.Uint64(data)
	_, _ = r.Seek(-int64(h.Len)-lenSize, io.SeekEnd)
	data = make([]byte, h.Len)
	if n, err := r.Read(data); err != nil && n != int(h.Len) {
		return fmt.Errorf("failed to read footer: err %w, read len %d, expect len %d", err, n, h.Len)
	}

	h.Msg = &pb.Footer{}
	if err := proto.Unmarshal(data, h.Msg); err != nil {
		return fmt.Errorf("failed to unmarshal footer: %w", err)
	}
	return nil
}

type BakSegment struct {
	typ uint32
	len uint64
	val proto.Message
}

func (s *BakSegment) Name() string {
	if name, ok := SegType2Name[int(s.typ)]; ok {
		return name
	}
	return fmt.Sprintf("type-%d", s.typ)
}

func (s *BakSegment) String() string {
	switch s.val.(type) {
	case *pb.Format:
		return string(s.val.(*pb.Format).Data)
	case *pb.Batch:
		return protojson.Format(s.val)
	}
	return "unknown segment"
}

func newBakSegment(val proto.Message) *BakSegment {
	s := &BakSegment{val: val}
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
			return nil
		}
	}
	return s
}

func (s *BakSegment) num() uint64 {
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

func (s *BakSegment) Marshal(w io.Writer) (int, error) {
	if s == nil || s.val == nil {
		return 0, fmt.Errorf("segment %s is nil", s)
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

func (s *BakSegment) Unmarshal(r io.Reader) error {
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
	Progress   func(name string, cnt int)
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
	Threads  int
	Progress func(name string, cnt int)
}

func (opt *LoadOption) check() {
	if opt.Threads < 1 {
		opt.Threads = 10
	}
}

// transaction

type txSessionKey struct{}
type txMaxRetryKey struct{}
