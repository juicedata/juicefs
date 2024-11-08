package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	BakMagic      = 0x747083
	BakVersion    = 1
	BakFooterSize = 4096
)

const (
	SegTypeUnknown = iota
	SegTypeFormat
	SegTypeCounter
	SegTypeSustained
	SegTypeDelFile
	SegTypeSliceRef
	SegTypeAcl
	SegTypeXattr
	SegTypeQuota
	SegTypeStat
	SegTypeNode
	SegTypeChunk
	SegTypeEdge
	SegTypeSymlink
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
		SegTypeSymlink:   proto.MessageName(&pb.SymlinkList{}),
	}

	SegName2Type = make(map[protoreflect.FullName]int)
	for k, v := range SegType2Name {
		SegName2Type[v] = k
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

type BakFormat struct {
	// BakSegment...
	Footer BakFooter
}

func NewBakFormat() *BakFormat {
	return &BakFormat{
		Footer: BakFooter{
			Magic:   BakMagic,
			Version: BakVersion,
		},
	}
}

func (f *BakFormat) WriteSegment(w io.Writer, seg *BakSegment) error {
	if seg == nil {
		return nil
	}

	data, err := seg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal segment %s: %v", seg, err)
	}

	n, err := w.Write(data)
	if err != nil && n != len(data) {
		return fmt.Errorf("failed to write segment %s: err %v, write len %d, expect len %d", seg, err, n, len(data))
	}
	return nil
}

func (f *BakFormat) ReadSegment(r io.Reader) (*BakSegment, error) {
	seg := &BakSegment{}
	if err := seg.Unmarshal(r); err != nil {
		return nil, fmt.Errorf("failed to unmarshal segment: %w", err)
	}
	return seg, nil
}

func (f *BakFormat) WriteFooter(w io.Writer) error {
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

func (f *BakFormat) ReadFooter(r io.ReadSeeker) (*BakFooter, error) {
	footer := &BakFooter{}
	if err := footer.Unmarshal(r); err != nil {
		return nil, err
	}
	if footer.Magic != BakMagic {
		return nil, fmt.Errorf("invalid magic number %d, expect %d", footer.Magic, BakMagic)
	}
	// TODO checksum
	return footer, nil
}

type BakFooter struct {
	Magic    uint32
	Version  uint32
	Checksum uint32
	_        [BakFooterSize - 12]byte
}

func (h *BakFooter) Marshal() ([]byte, error) {
	buff := bytes.NewBuffer(make([]byte, 0, BakFooterSize))
	if err := binary.Write(buff, binary.LittleEndian, h); err != nil {
		return nil, err
	}
	data := buff.Bytes()
	if len(data) != BakFooterSize {
		return nil, fmt.Errorf("footer size is %d, expect %d", len(data), BakFooterSize)
	}
	return data, nil
}

func (h *BakFooter) Unmarshal(r io.ReadSeeker) error {
	r.Seek(BakFooterSize, io.SeekEnd)
	data := make([]byte, BakFooterSize)
	n, err := r.Read(data)
	if err != nil && n != int(BakFooterSize) {
		return fmt.Errorf("failed to read footer: err %v, read len %d, expect len %d", err, n, BakFooterSize)
	}

	buff := bytes.NewBuffer(data)
	return binary.Read(buff, binary.LittleEndian, h)
}

type BakSegment struct {
	Typ uint32
	Len uint32
	Val proto.Message
}

func (s *BakSegment) String() string {
	return string(proto.MessageName(s.Val).Name())
}

func (s *BakSegment) Marshal() ([]byte, error) {
	if s == nil || s.Val == nil {
		return nil, fmt.Errorf("segment %s is nil", s)
	}

	typ, ok := SegName2Type[proto.MessageName(s.Val)]
	if !ok {
		return nil, fmt.Errorf("segment type %d is unknown", typ)
	}
	s.Typ = uint32(typ)

	buf := bytes.NewBuffer(nil)
	if err := binary.Write(buf, binary.LittleEndian, s.Typ); err != nil {
		return nil, fmt.Errorf("failed to write segment %s type: %v", s, err)
	}
	data, err := proto.Marshal(s.Val)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal segment %s message: %v", s, err)
	}
	s.Len = uint32(len(data))
	if err := binary.Write(buf, binary.LittleEndian, s.Len); err != nil {
		return nil, fmt.Errorf("failed to write segment %s length: %v", s, err)
	}
	if n, err := buf.Write(data); err != nil || n != len(data) {
		return nil, fmt.Errorf("failed to write segment %s: err %v, write len %d, expect len %d", s, err, n, len(data))
	}
	return buf.Bytes(), nil
}

func (s *BakSegment) Unmarshal(r io.Reader) error {
	if err := binary.Read(r, binary.LittleEndian, &s.Typ); err != nil {
		return fmt.Errorf("failed to read segment type: %v", err)
	}

	name, ok := SegType2Name[int(s.Typ)]
	if !ok {
		if s.Typ == BakMagic {
			return ErrBakEOF
		}
		return fmt.Errorf("segment type %d is unknown", s.Typ)
	}

	if err := binary.Read(r, binary.LittleEndian, &s.Len); err != nil {
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
		return fmt.Errorf("failed to unmarshal segment msg: %v", err)
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
	query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error
	segReleaser
}

type dumpedSeg struct {
	iDumpedSeg
	typ  int
	meta Meta
}

func (s *dumpedSeg) String() string            { return string(SegType2Name[s.typ]) }
func (s *dumpedSeg) release(msg proto.Message) {}

type formatDS struct {
	dumpedSeg
	f          *Format
	keepSecret bool
}

func (s *formatDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	f := s.f
	msg := &pb.Format{
		Name:             f.Name,
		Uuid:             f.UUID,
		Storage:          f.Storage,
		StorageClass:     f.StorageClass,
		Bucket:           f.Bucket,
		AccessKey:        f.AccessKey,
		SecretKey:        f.SecretKey,
		SessionToken:     f.SessionToken,
		BlockSize:        int32(f.BlockSize),
		Compression:      f.Compression,
		Shards:           int32(f.Shards),
		HashPrefix:       f.HashPrefix,
		Capacity:         f.Capacity,
		Inodes:           f.Inodes,
		EncryptKey:       f.EncryptKey,
		EncryptAlgo:      f.EncryptAlgo,
		KeyEncrypted:     f.KeyEncrypted,
		UploadLimit:      f.UploadLimit,
		DownloadLimit:    f.DownloadLimit,
		TrashDays:        int32(f.TrashDays),
		MetaVersion:      int32(f.MetaVersion),
		MinClientVersion: f.MinClientVersion,
		MaxClientVersion: f.MaxClientVersion,
		DirStats:         f.DirStats,
		EnableAcl:        f.EnableACL,
	}

	if !s.keepSecret {
		removeKey := func(key *string, name string) {
			if *key == "" {
				*key = "remove"
				logger.Warnf("%s is removed for the sake of safety", name)
			}
		}
		removeKey(&msg.SecretKey, "Secret Key")
		removeKey(&msg.SessionToken, "Session Token")
		removeKey(&msg.EncryptKey, "Encrypt Key")
	}

	return dumpResult(ctx, ch, &dumpedResult{s, msg})
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

func (opt *LoadOption) check() *LoadOption {
	if opt == nil {
		opt = &LoadOption{}
	}
	if opt.CoNum < 1 {
		opt.CoNum = 1
	}
	return opt
}

type segDecoder interface {
	decode(proto.Message) []interface{}
	release(rows []interface{})
}

type iLoadedSeg interface {
	String() string
	insert(ctx Context, msg proto.Message) error

	newMsg() proto.Message
	segDecoder
}

type loadedSeg struct {
	iLoadedSeg
	typ  int
	meta Meta
}

func (s *loadedSeg) String() string { return string(SegType2Name[s.typ]) }

func (s *loadedSeg) release(rows []interface{}) {}

type formatLS struct {
	loadedSeg
}

func (s *formatLS) insert(ctx Context, msg proto.Message) error { return nil }
