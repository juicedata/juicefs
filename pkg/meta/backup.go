package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
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
	SegTypeMix // for tkv only
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
	_, _ = r.Seek(BakFooterSize, io.SeekEnd)
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
	return msg
}

func ConvertFormatFromPB(msg *pb.Format) *Format {
	return &Format{
		Name:             msg.Name,
		UUID:             msg.Uuid,
		Storage:          msg.Storage,
		StorageClass:     msg.StorageClass,
		Bucket:           msg.Bucket,
		AccessKey:        msg.AccessKey,
		SecretKey:        msg.SecretKey,
		SessionToken:     msg.SessionToken,
		BlockSize:        int(msg.BlockSize),
		Compression:      msg.Compression,
		Shards:           int(msg.Shards),
		HashPrefix:       msg.HashPrefix,
		Capacity:         msg.Capacity,
		Inodes:           msg.Inodes,
		EncryptKey:       msg.EncryptKey,
		EncryptAlgo:      msg.EncryptAlgo,
		KeyEncrypted:     msg.KeyEncrypted,
		UploadLimit:      msg.UploadLimit,
		DownloadLimit:    msg.DownloadLimit,
		TrashDays:        int(msg.TrashDays),
		MetaVersion:      int(msg.MetaVersion),
		MinClientVersion: msg.MinClientVersion,
		MaxClientVersion: msg.MaxClientVersion,
		DirStats:         msg.DirStats,
		EnableACL:        msg.EnableAcl,
	}
}

func MarshalAclPB(msg *pb.Acl) []byte {
	w := utils.NewBuffer(uint32(16 + (len(msg.Users)+len(msg.Groups))*6))
	w.Put16(uint16(msg.Owner))
	w.Put16(uint16(msg.Group))
	w.Put16(uint16(msg.Mask))
	w.Put16(uint16(msg.Other))
	w.Put32(uint32(len(msg.Users)))
	for _, user := range msg.Users {
		w.Put32(user.Id)
		w.Put16(uint16(user.Perm))
	}
	w.Put32(uint32(len(msg.Groups)))
	for _, group := range msg.Groups {
		w.Put32(group.Id)
		w.Put16(uint16(group.Perm))
	}
	return w.Bytes()
}

func UnmarshalAclPB(buff []byte) *pb.Acl {
	acl := &pb.Acl{}
	rb := utils.ReadBuffer(buff)
	acl.Owner = uint32(rb.Get16())
	acl.Group = uint32(rb.Get16())
	acl.Mask = uint32(rb.Get16())
	acl.Other = uint32(rb.Get16())

	var entry *pb.AclEntry
	uCnt := rb.Get32()
	acl.Users = make([]*pb.AclEntry, 0, uCnt)
	for i := 0; i < int(uCnt); i++ {
		entry = &pb.AclEntry{}
		entry.Id = rb.Get32()
		entry.Perm = uint32(rb.Get16())
		acl.Users = append(acl.Users, entry)
	}

	gCnt := rb.Get32()
	acl.Groups = make([]*pb.AclEntry, 0, gCnt)
	for i := 0; i < int(gCnt); i++ {
		entry = &pb.AclEntry{}
		entry.Id = rb.Get32()
		entry.Perm = uint32(rb.Get16())
		acl.Groups = append(acl.Groups, entry)
	}
	return acl
}

const (
	BakNodeSizeWithoutAcl = 71
	BakNodeSize           = 79
)

func MarshalNodePB(msg *pb.Node, buff []byte) {
	w := utils.FromBuffer(buff)
	w.Put8(uint8(msg.Flags))
	w.Put16((uint16(msg.Type) << 12) | (uint16(msg.Mode) & 0xfff))
	w.Put32(msg.Uid)
	w.Put32(msg.Gid)
	w.Put64(uint64(msg.Atime))
	w.Put32(uint32(msg.AtimeNsec))
	w.Put64(uint64(msg.Mtime))
	w.Put32(uint32(msg.MtimeNsec))
	w.Put64(uint64(msg.Ctime))
	w.Put32(uint32(msg.CtimeNsec))
	w.Put32(msg.Nlink)
	w.Put64(msg.Length)
	w.Put32(msg.Rdev)
	w.Put64(msg.Parent)
	if msg.AccessAclId|msg.DefaultAclId != aclAPI.None {
		w.Put32(msg.AccessAclId)
		w.Put32(msg.DefaultAclId)
	}
}

func ResetNodePB(msg *pb.Node) {
	if msg == nil {
		return
	}
	// fields that maybe not set in UnmarshalNodePB
	msg.Parent = 0
	msg.AccessAclId = 0
	msg.DefaultAclId = 0
}

func UnmarshalNodePB(buff []byte, node *pb.Node) {
	rb := utils.FromBuffer(buff)
	node.Flags = uint32(rb.Get8())
	node.Mode = uint32(rb.Get16())
	node.Type = node.Mode >> 12
	node.Mode &= 0777
	node.Uid = rb.Get32()
	node.Gid = rb.Get32()
	node.Atime = int64(rb.Get64())
	node.AtimeNsec = int32(rb.Get32())
	node.Mtime = int64(rb.Get64())
	node.MtimeNsec = int32(rb.Get32())
	node.Ctime = int64(rb.Get64())
	node.CtimeNsec = int32(rb.Get32())
	node.Nlink = rb.Get32()
	node.Length = rb.Get64()
	node.Rdev = rb.Get32()
	if rb.Left() >= 8 {
		node.Parent = rb.Get64()
	}
	if rb.Left() >= 8 {
		node.AccessAclId = rb.Get32()
		node.DefaultAclId = rb.Get32()
	}
}

func MarshalSlicePB(msg *pb.Slice, buff []byte) {
	w := utils.FromBuffer(buff)
	w.Put32(msg.Pos)
	w.Put64(msg.Id)
	w.Put32(msg.Size)
	w.Put32(msg.Off)
	w.Put32(msg.Len)
}

func UnmarshalSlicePB(buff []byte, slice *pb.Slice) {
	rb := utils.ReadBuffer(buff)
	slice.Pos = rb.Get32()
	slice.Id = rb.Get64()
	slice.Size = rb.Get32()
	slice.Off = rb.Get32()
	slice.Len = rb.Get32()
}

func MarshalEdgePB(msg *pb.Edge, buff []byte) {
	w := utils.FromBuffer(buff)
	w.Put8(uint8(msg.Type))
	w.Put64(msg.Inode)
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
