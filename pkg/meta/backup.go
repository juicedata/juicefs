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
	SegTypeUnknown = iota
	SegTypeFormat
	SegTypeCounter
	SegTypeSustained
	SegTypeDelFile
	SegTypeAcl
	SegTypeXattr
	SegTypeQuota
	SegTypeStat
	SegTypeNode
	SegTypeChunk
	SegTypeSliceRef
	SegTypeEdge
	SegTypeParent // for redis/tkv only
	SegTypeSymlink
	SegTypeMix // for redis/tkv only
	SegTypeMax
)

func getMessageNameFromType(typ int) protoreflect.FullName {
	if typ == SegTypeFormat {
		return proto.MessageName(&pb.Format{})
	} else if typ < SegTypeMax {
		return proto.MessageName(&pb.Batch{})
	} else {
		return ""
	}
}

func CreateMessageByName(name protoreflect.FullName) (proto.Message, error) {
	typ, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		return nil, fmt.Errorf("failed to find message %s's type: %v", name, err)
	}
	return typ.New().Interface(), nil
}

var ErrBakEOF = fmt.Errorf("reach backup EOF")

// BakFormat: BakSegment... + BakEOF + BakFooter
type BakFormat struct {
	Offset uint64
	Footer *BakFooter
}

func NewBakFormat() *BakFormat {
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

func (f *BakFormat) WriteSegment(w io.Writer, seg *BakSegment) error {
	if seg == nil {
		return nil
	}

	n, err := seg.Marshal(w)
	if err != nil {
		return fmt.Errorf("failed to marshal segment %s: %v", seg, err)
	}

	name := seg.String()
	info, ok := f.Footer.Msg.Infos[name]
	if !ok {
		info = &pb.Footer_SegInfo{Offset: []uint64{}}
		f.Footer.Msg.Infos[name] = info
	}

	info.Offset = append(info.Offset, f.Offset)
	f.Offset += uint64(n)
	return nil
}

func (f *BakFormat) ReadSegment(r io.Reader) (*BakSegment, error) {
	seg := &BakSegment{}
	if err := seg.Unmarshal(r); err != nil {
		return nil, err
	}
	return seg, nil
}

func (f *BakFormat) WriteFooter(w io.Writer) error {
	if err := f.writeEOS(w); err != nil {
		return err
	}

	data, err := f.Footer.Marshal()
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err != nil && n != len(data) {
		return fmt.Errorf("failed to write footer: err %v, write len %d, expect len %d", err, n, len(data))
	}
	return nil
}

func (f *BakFormat) writeEOS(w io.Writer) error {
	if n, err := w.Write(binary.BigEndian.AppendUint32(nil, BakEOS)); err != nil && n != 4 {
		return fmt.Errorf("failed to write EOS: err %w, write len %d, expect len 4", err, n)
	}
	return nil
}

func (f *BakFormat) ReadFooter(r io.ReadSeeker) (*BakFooter, error) {
	footer := &BakFooter{}
	if err := footer.Unmarshal(r); err != nil {
		return nil, err
	}
	if footer.Msg.Magic != BakMagic {
		return nil, fmt.Errorf("invalid magic number %d, expect %d", footer.Msg.Magic, BakMagic)
	}
	return footer, nil
}

type BakFooter struct {
	Msg *pb.Footer
	Len uint64
}

func (h *BakFooter) Marshal() ([]byte, error) {
	data, err := proto.Marshal(h.Msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal footer: %w", err)
	}

	h.Len = uint64(len(data))
	data = binary.BigEndian.AppendUint64(data, h.Len)
	return data, nil
}

func (h *BakFooter) Unmarshal(r io.ReadSeeker) error {
	lenSize := int64(unsafe.Sizeof(h.Len))
	_, _ = r.Seek(lenSize, io.SeekEnd)

	data := make([]byte, lenSize)
	if n, err := r.Read(data); err != nil && n != int(lenSize) {
		return fmt.Errorf("failed to read footer length: err %w, read len %d, expect len %d", err, n, lenSize)
	}

	h.Len = binary.BigEndian.Uint64(data)
	_, _ = r.Seek(int64(h.Len)+lenSize, io.SeekEnd)
	data = make([]byte, h.Len)
	if n, err := r.Read(data); err != nil && n != int(h.Len) {
		return fmt.Errorf("failed to read footer: err %w, read len %d, expect len %d", err, n, h.Len)
	}

	if err := proto.Unmarshal(data, h.Msg); err != nil {
		return fmt.Errorf("failed to unmarshal footer: %w", err)
	}
	return nil
}

type BakSegment struct {
	Typ uint32
	Len uint64
	Val proto.Message
}

func (s *BakSegment) String() string {
	return string(proto.MessageName(s.Val).Name())
}

func (s *BakSegment) Marshal(w io.Writer) (int, error) {
	if s == nil || s.Val == nil {
		return 0, fmt.Errorf("segment %s is nil", s)
	}

	switch v := s.Val.(type) {
	case *pb.Format:
		s.Typ = uint32(SegTypeFormat)
	case *pb.Batch:
		if v.Counters != nil {
			s.Typ = uint32(SegTypeCounter)
		} else if v.Sustained != nil {
			s.Typ = uint32(SegTypeSustained)
		} else if v.Delfiles != nil {
			s.Typ = uint32(SegTypeDelFile)
		} else if v.Acls != nil {
			s.Typ = uint32(SegTypeAcl)
		} else if v.Xattrs != nil {
			s.Typ = uint32(SegTypeXattr)
		} else if v.Quotas != nil {
			s.Typ = uint32(SegTypeQuota)
		} else if v.Dirstats != nil {
			s.Typ = uint32(SegTypeStat)
		} else if v.Nodes != nil {
			s.Typ = uint32(SegTypeNode)
		} else if v.Chunks != nil {
			s.Typ = uint32(SegTypeChunk)
		} else if v.SliceRefs != nil {
			s.Typ = uint32(SegTypeSliceRef)
		} else if v.Edges != nil {
			s.Typ = uint32(SegTypeEdge)
		} else if v.Symlinks != nil {
			s.Typ = uint32(SegTypeSymlink)
		} else if v.Parents != nil {
			s.Typ = uint32(SegTypeParent)
		} else {
			return 0, fmt.Errorf("unknown batch type %s", s)
		}
	}

	if err := binary.Write(w, binary.BigEndian, s.Typ); err != nil {
		return 0, fmt.Errorf("failed to write segment type %s : %w", s, err)
	}
	data, err := proto.Marshal(s.Val)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal segment message %s : %w", s, err)
	}
	s.Len = uint64(len(data))
	if err := binary.Write(w, binary.BigEndian, s.Len); err != nil {
		return 0, fmt.Errorf("failed to write segment length %s: %w", s, err)
	}

	if n, err := w.Write(data); err != nil || n != len(data) {
		return 0, fmt.Errorf("failed to write segment data %s: err %w, write len %d, expect len %d", s, err, n, len(data))
	}

	return binary.Size(s.Typ) + binary.Size(s.Len) + len(data), nil
}

func (s *BakSegment) Unmarshal(r io.Reader) error {
	if err := binary.Read(r, binary.BigEndian, &s.Typ); err != nil {
		return fmt.Errorf("failed to read segment type: %v", err)
	}

	if s.Typ == BakMagic {
		return ErrBakEOF
	}
	name := getMessageNameFromType(int(s.Typ))
	if name == "" {
		return fmt.Errorf("segment type %d is unknown", s.Typ)
	}

	if err := binary.Read(r, binary.BigEndian, &s.Len); err != nil {
		return fmt.Errorf("failed to read segment %s length: %v", s, err)
	}
	data := make([]byte, s.Len)
	n, err := r.Read(data)
	if err != nil && n != int(s.Len) {
		return fmt.Errorf("failed to read segment value: err %v, read len %d, expect len %d", err, n, s.Len)
	}
	msg, err := CreateMessageByName(name)
	if err != nil {
		return fmt.Errorf("failed to create message %s: %v", name, err)
	}
	if err = proto.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("failed to unmarshal segment msg %s: %v", name, err)
	}
	s.Val = msg
	return nil
}

// Dump Segment

type DumpOption struct {
	KeepSecret bool
	Threads    int
}

func (opt *DumpOption) check() *DumpOption {
	if opt == nil {
		opt = &DumpOption{}
	}
	if opt.Threads < 1 {
		opt.Threads = 1
	}
	return opt
}

func (m *baseMeta) dumpFormat(ctx Context, opt *DumpOption, txn *eTxn, ch chan *dumpedResult) error {
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

func dumpResult(ctx context.Context, ch chan *dumpedResult, res *dumpedResult) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- res:
		return nil
	}
}

// Load Segment...

type LoadOption struct {
	threads int
}

func (opt *LoadOption) check() {
	if opt.threads < 1 {
		opt.threads = 1
	}
}

// transaction

type txMaxRetryKey struct{}

type bTxnOption struct {
	threads      int
	notUsed      bool
	readOnly     bool
	maxRetry     int
	maxStmtRetry int
}

type eTxn struct {
	en  engine
	opt *bTxnOption
	obj interface{} // real transaction object for different engine
}
