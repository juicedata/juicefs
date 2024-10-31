package meta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"google.golang.org/protobuf/proto"
)

const (
	BakMagic      = 0x747083
	BakVersion    = 1
	BakHeaderSize = 4096
)

type BakFormat struct {
	Header BakHeader
	// Format     pb.Format
	// Counters   pb.Counters
	// Sustaineds pb.Sustaineds
	// DelFiles pb.DelFiles
	// Acls pb.Acls
	// repeated Header.NodeBatchNum times: size(pb.NodeBatch), pb.NodeBatch, ...
	// repeated Header.ChunkBatchNum times: size(pb.ChunkBatch), pb.ChunkBatch, ...
	// repeated Header.EdgeBatchNum times: size(pb.EdgeBatch), pb.EdgeBatch, ...
	// repeated Header.SliceRefBatchNum times: size(pb.SliceRefBatch), pb.SliceRefBatch, ...
	// repeated Header.SymlinkBatchNum times: size(pb.SymlinkBatch), pb.SymlinkBatch, ...
	// repeated Header.XattrBatchNum times: size(pb.XattrBatch), pb.XattrBatch, ...
	// repeated Header.QuotaBatchNum times: size(pb.QuotaBatch), pb.QuotaBatch, ...
	// repeated Header.StatBatchNum times: size(pb.StatBatch), pb.StatBatch, ...
}

func newBakFormat() *BakFormat {
	return &BakFormat{
		Header: BakHeader{
			Magic:   BakMagic,
			Version: BakVersion,
		},
	}
}

func (f *BakFormat) seekForWrite(w io.Seeker) {
	w.Seek(BakHeaderSize, io.SeekStart)
}

func (f *BakFormat) writeData(w io.Writer, name string, data []byte) (int, error) {
	n, err := w.Write(data)
	if err != nil && n != len(data) {
		return n, fmt.Errorf("write %s failed: err %v, write len %d, expect len %d", name, err, n, len(data))
	}
	return n, nil
}

func (f *BakFormat) readData(r io.Reader, name string, size int) ([]byte, error) {
	data := make([]byte, size)
	n, err := r.Read(data)
	if err != nil && n != size {
		return nil, fmt.Errorf("read %s failed: err %v, read len %d, expect len %d", name, err, n, size)
	}
	return data, nil
}

func (f *BakFormat) writeSeg(w io.Writer, m proto.Message) error {
	if m == nil {
		return nil
	}
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	n, err := f.writeData(w, string(proto.MessageName(m)), data)
	if err != nil {
		return err
	}
	f.Header.setSize(string(proto.MessageName(m).Name()), uint32(n))
	return nil
}

func (f *BakFormat) readSeg(r io.Reader, name string, size int, msg proto.Message) error {
	if size == 0 {
		return nil
	}
	data, err := f.readData(r, name, size)
	if err != nil {
		return err
	}
	// skip nil message
	if msg == nil {
		return nil
	}
	return proto.Unmarshal(data, msg)
}

func (f *BakFormat) writeBatchSeg(w io.Writer, m proto.Message) error {
	if m == nil {
		return nil
	}

	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}

	name := string(proto.MessageName(m).Name())
	// batch size
	n1, err := f.writeData(w, name+" size", binary.LittleEndian.AppendUint32(nil, uint32(len(data))))
	if err != nil {
		return nil
	}
	// batch msg
	n2, err := f.writeData(w, name, data)
	if err != nil {
		return err
	}
	f.Header.addBatch(name, uint32(n1+n2))
	return nil
}

func (f *BakFormat) readBatchSeg(r io.Reader, name string, msg proto.Message) error {
	sd, err := f.readData(r, name+" size", 4)
	if err != nil {
		return err
	}

	size := int(binary.LittleEndian.Uint32(sd))
	data, err := f.readData(r, name, size)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, msg)
}

func (f *BakFormat) writeHeader(w io.WriteSeeker) error {
	w.Seek(0, io.SeekStart)
	data, err := f.Header.marshal()
	if err != nil {
		return err
	}
	_, err = f.writeData(w, "Header", data)
	return err
}

func (f *BakFormat) readHeader(r io.Reader) error {
	data, err := f.readData(r, "Header", BakHeaderSize)
	if err != nil {
		return err
	}
	if err = f.Header.unmarshal(data); err != nil {
		return err
	}

	// check TODO
	if f.Header.Magic != BakMagic {
		return fmt.Errorf("this binary file may not be a juicefs backup file")
	}
	return nil
}

type BakHeader struct {
	Magic            uint32
	Version          uint32
	Checksum         uint32
	FormatSize       uint32
	CounterSize      uint32
	SustainedSize    uint32
	DelFileSize      uint32
	AclSize          uint32
	NodeSize         uint32
	NodeBatchNum     uint32
	ChunkSize        uint32
	ChunkBatchNum    uint32
	EdgeSize         uint32
	EdgeBatchNum     uint32
	SliceRefSize     uint32
	SliceRefBatchNum uint32
	SymlinkSize      uint32
	SymlinkBatchNum  uint32
	XattrSize        uint32
	XattrBatchNum    uint32
	QuotaSize        uint32
	QuotaBatchNum    uint32
	StatSize         uint32
	StatBatchNum     uint32
	_                [BakHeaderSize - 96]byte
}

func (h *BakHeader) addBatch(name string, size uint32) {
	switch name {
	case "NodeBatch":
		h.NodeSize += size
		h.NodeBatchNum++
	case "ChunkBatch":
		h.ChunkSize += size
		h.ChunkBatchNum++
	case "EdgeBatch":
		h.EdgeSize += size
		h.EdgeBatchNum++
	case "SliceRefBatch":
		h.SliceRefSize += size
		h.SliceRefBatchNum++
	case "SymlinkBatch":
		h.SymlinkSize += size
		h.SymlinkBatchNum++
	case "XattrBatch":
		h.XattrSize += size
		h.XattrBatchNum++
	case "QuotaBatch":
		h.QuotaSize += size
		h.QuotaBatchNum++
	case "StatBatch":
		h.StatSize += size
		h.StatBatchNum++
	}
}

func (h *BakHeader) setSize(name string, n uint32) {
	switch name {
	case "Format":
		h.FormatSize = n
	case "Counters":
		h.CounterSize = n
	case "Sustaineds":
		h.SustainedSize = n
	case "DelFiles":
		h.DelFileSize = n
	case "Acls":
		h.AclSize = n
	}
}

func (h *BakHeader) marshal() ([]byte, error) {
	buff := bytes.NewBuffer(make([]byte, 0, BakHeaderSize))
	if err := binary.Write(buff, binary.LittleEndian, h); err != nil {
		return nil, err
	}
	data := buff.Bytes()
	if len(data) != BakHeaderSize {
		return nil, fmt.Errorf("header size is %d, expect %d", len(data), BakHeaderSize)
	}
	return data, nil
}

func (h *BakHeader) unmarshal(data []byte) error {
	buff := bytes.NewBuffer(data)
	return binary.Read(buff, binary.LittleEndian, h)
}

func newPBFormat(f *Format, keepSecret bool) *pb.Format {
	pf := &pb.Format{}
	pf.Name = f.Name
	pf.Uuid = f.UUID
	pf.Storage = f.Storage
	pf.StorageClass = f.StorageClass
	pf.Bucket = f.Bucket
	pf.AccessKey = f.AccessKey
	pf.SecretKey = f.SecretKey
	pf.SessionToken = f.SessionToken
	pf.BlockSize = int32(f.BlockSize)
	pf.Compression = f.Compression
	pf.Shards = int32(f.Shards)
	pf.HashPrefix = f.HashPrefix
	pf.Capacity = f.Capacity
	pf.Inodes = f.Inodes
	pf.EncryptKey = f.EncryptKey
	pf.EncryptAlgo = f.EncryptAlgo
	pf.KeyEncrypted = f.KeyEncrypted
	pf.UploadLimit = f.UploadLimit
	pf.DownloadLimit = f.DownloadLimit
	pf.TrashDays = int32(f.TrashDays)
	pf.MetaVersion = int32(f.MetaVersion)
	pf.MinClientVersion = f.MinClientVersion
	pf.MaxClientVersion = f.MaxClientVersion
	pf.DirStats = f.DirStats
	pf.EnableAcl = f.EnableACL

	if !keepSecret {
		removeKey := func(key *string, name string) {
			if *key == "" {
				*key = "remove"
				logger.Warnf("%s is removed for the sake of safety", name)
			}
		}
		removeKey(&pf.SecretKey, "Secret Key")
		removeKey(&pf.SessionToken, "Session Token")
		removeKey(&pf.EncryptKey, "Encrypt Key")
	}
	return pf
}

func newPBPool(msg proto.Message) *sync.Pool {
	pr := msg.ProtoReflect()
	return &sync.Pool{
		New: func() interface{} {
			return pr.New().Interface()
		},
	}
}

// TODO
func getCounterFields(c *pb.Counters) map[string]*int64 {
	return map[string]*int64{
		"usedSpace":   &c.UsedSpace,
		"totalInodes": &c.UsedInodes,
		"nextInode":   &c.NextInode,
		"nextChunk":   &c.NextChunk,
		"nextSession": &c.NextSession,
		"nextTrash":   &c.NextTrash,
	}
}
