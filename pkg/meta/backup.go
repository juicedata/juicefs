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
	"sync"
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

var (
	SegType2Name map[int]protoreflect.FullName
	SegName2Type map[protoreflect.FullName]int
)

func init() {
	SegType2Name = map[int]protoreflect.FullName{
		SegTypeFormat:    proto.MessageName(&pb.Format{}),
		SegTypeCounter:   proto.MessageName(&pb.Counters{}),
		SegTypeSustained: proto.MessageName(&pb.SustainedList{}),
		SegTypeDelFile:   proto.MessageName(&pb.DelFileList{}),
		SegTypeSliceRef:  proto.MessageName(&pb.SliceRefList{}),
		SegTypeAcl:       proto.MessageName(&pb.AclList{}),
		SegTypeXattr:     proto.MessageName(&pb.XattrList{}),
		SegTypeQuota:     proto.MessageName(&pb.QuotaList{}),
		SegTypeStat:      proto.MessageName(&pb.StatList{}),
		SegTypeNode:      proto.MessageName(&pb.NodeList{}),
		SegTypeChunk:     proto.MessageName(&pb.ChunkList{}),
		SegTypeEdge:      proto.MessageName(&pb.EdgeList{}),
		SegTypeParent:    proto.MessageName(&pb.ParentList{}),
		SegTypeSymlink:   proto.MessageName(&pb.SymlinkList{}),
	}

	SegName2Type = make(map[protoreflect.FullName]int)
	for k, v := range SegType2Name {
		SegName2Type[v] = k
	}

	SegType2Name[SegTypeMix] = "kv.Mix"
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

	typ, ok := SegName2Type[proto.MessageName(s.Val)]
	if !ok {
		return 0, fmt.Errorf("segment type %d is unknown", typ)
	}
	s.Typ = uint32(typ)

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

	name, ok := SegType2Name[int(s.Typ)]
	if !ok {
		if s.Typ == BakMagic {
			return ErrBakEOF
		}
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
	CoNum      int
}

func (opt *DumpOption) check() *DumpOption {
	if opt == nil {
		opt = &DumpOption{}
	}
	if opt.CoNum < 1 {
		opt.CoNum = 1
	}
	return opt
}

type segReleaser interface {
	release(msg proto.Message)
}

type iDumpedSeg interface {
	String() string
	dump(ctx Context, ch chan *dumpedResult) error
	segReleaser
}

type dumpedSeg struct {
	iDumpedSeg
	typ  int
	meta Meta
	opt  *DumpOption
	txn  *eTxn
}

func (s *dumpedSeg) String() string            { return string(SegType2Name[s.typ]) }
func (s *dumpedSeg) release(msg proto.Message) {}

type formatDS struct {
	dumpedSeg
}

func (s *formatDS) dump(ctx Context, ch chan *dumpedResult) error {
	f := s.meta.GetFormat()
	return dumpResult(ctx, ch, &dumpedResult{s, ConvertFormatToPB(&f, s.opt.KeepSecret)})
}

type dumpedBatchSeg struct {
	dumpedSeg
	pools []*sync.Pool
}

type dumpedResult struct {
	seg segReleaser
	msg proto.Message
}

func dumpResult(ctx context.Context, ch chan *dumpedResult, res *dumpedResult) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- res:
	}
	return nil
}

// Load Segment...

type LoadOption struct {
	CoNum int
}

func (opt *LoadOption) check() {
	if opt.CoNum < 1 {
		opt.CoNum = 1
	}
}

type iLoadedSeg interface {
	String() string
	load(ctx Context, msg proto.Message) error
}

type loadedSeg struct {
	iLoadedSeg
	typ  int
	meta Meta
}

func (s *loadedSeg) String() string { return string(SegType2Name[s.typ]) }

// Message Marshal/Unmarshal

func ConvertFormatToPB(f *Format, keepSecret bool) *pb.Format {
	if !keepSecret {
		f.RemoveSecret()
	}
	data, err := json.MarshalIndent(f, "", "")
	if err != nil {
		logger.Errorf("failed to marshal format %s: %v", f.Name, err)
		return nil
	}
	return &pb.Format{
		Data: data,
	}
}

// transaction

type txMaxRetryKey struct{}

type bTxnOption struct {
	coNum        int
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
