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
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"

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
	segTypeChangeLog
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
	segTypeChangeLog: "changeLog",
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
	f.Footer = footer
	return footer, nil
}

// readBackupSource reads the footer after the end-of-segments marker.
func readBackupSource(r io.Reader) (pb.Footer_Engine, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return pb.Footer_UNKNOWN, fmt.Errorf("failed to read footer: %w", err)
	}
	if len(data) < 8 {
		return pb.Footer_UNKNOWN, fmt.Errorf("failed to read footer length: %w", io.ErrUnexpectedEOF)
	}
	length := binary.BigEndian.Uint64(data[len(data)-8:])
	if length != uint64(len(data)-8) {
		return pb.Footer_UNKNOWN, fmt.Errorf("invalid footer length %d, actual %d", length, len(data)-8)
	}
	footer := &BakFooter{Len: length}
	if err := footer.unmarshal(data[:len(data)-8]); err != nil {
		return pb.Footer_UNKNOWN, err
	}
	return footer.Msg.Source, nil
}

// LoadChangelogBaseline reads the volume format, lastChangelog and the changelog
// versions included in a JSON or binary metadata backup. A binary backup that is
// not seekable is copied to a temporary file.
func LoadChangelogBaseline(r io.Reader) (*Format, int64, map[uint64]struct{}, error) {
	reader := bufio.NewReader(r)
	var first byte

	for {
		prefix, err := reader.Peek(1)
		if err != nil {
			return nil, 0, nil, err
		}
		first = prefix[0]
		if first != ' ' && first != '\t' && first != '\r' && first != '\n' {
			break
		}
		_, _ = reader.Discard(1)
	}

	var format Format
	var from int64
	var err error
	seen := make(map[uint64]struct{})
	if first == '{' {
		from, err = readJSONBaseline(reader, &format, seen)
	} else {
		rs, ok := r.(io.ReadSeeker)
		if !ok {
			fp, err := os.CreateTemp("", "juicefs-backup-*")
			if err != nil {
				return nil, 0, nil, err
			}
			defer os.Remove(fp.Name())
			defer fp.Close()
			if _, err := io.Copy(fp, reader); err != nil {
				return nil, 0, nil, fmt.Errorf("copy backup: %w", err)
			}
			rs = fp
		}
		from, err = readBinaryBaseline(rs, &format, seen)
	}
	if err != nil {
		return nil, 0, nil, err
	}
	if format.UUID == "" {
		return nil, 0, nil, fmt.Errorf("backup does not contain a volume UUID")
	}
	if from <= 0 {
		return nil, 0, nil, fmt.Errorf("backup does not contain a positive lastChangelog")
	}
	for id := range seen {
		if id == 0 || id > uint64(from) {
			return nil, 0, nil, fmt.Errorf("backup changelog %d is outside the baseline ending at %d", int64(id), from)
		}
	}
	return &format, from, seen, nil
}

func readJSONBaseline(r io.Reader, format *Format, seen map[uint64]struct{}) (int64, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return 0, err
	}
	var from int64
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return 0, err
		}
		switch name {
		case "Setting":
			err = dec.Decode(format)
		case "Counters":
			var counters DumpedCounters
			err = dec.Decode(&counters)
			from = counters.LastChangelog
		case "ChangeLog":
			var logs []struct{ Version int64 }
			err = dec.Decode(&logs)
			for _, log := range logs {
				seen[uint64(log.Version)] = struct{}{}
			}
		case "FSTree", "Trash":
			// Dump writes the file trees after all other fields.
			return from, nil
		default:
			err = skipJSONValue(dec)
		}
		if err != nil {
			return 0, fmt.Errorf("read backup field %s: %w", name, err)
		}
	}
	_, err := dec.Token()
	return from, err
}

// skipJSONValue skips the next value without retaining it in memory.
func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

func readBinaryBaseline(r io.ReadSeeker, format *Format, seen map[uint64]struct{}) (int64, error) {
	bak := &BakFormat{}
	footer, err := bak.ReadFooter(r)
	if err != nil {
		return 0, err
	}
	var from int64
	for _, name := range []string{"format", "counter", "changeLog"} {
		info := footer.Msg.Infos[name]
		if info == nil {
			continue
		}
		for _, offset := range info.Offset {
			if _, err := r.Seek(int64(offset), io.SeekStart); err != nil {
				return 0, err
			}
			seg, err := bak.ReadSegment(r)
			if err != nil {
				return 0, err
			}
			if seg.Name() != name {
				return 0, fmt.Errorf("expected backup segment %s, got %s", name, seg.Name())
			}
			switch seg.typ {
			case segTypeFormat:
				if err := json.Unmarshal(seg.val.(*pb.Format).Data, format); err != nil {
					return 0, err
				}
			case segTypeCounter:
				for _, counter := range seg.val.(*pb.Batch).Counters {
					if counter.Key == "lastChangelog" {
						from = counter.Value
					}
				}
			case segTypeChangeLog:
				for _, log := range seg.val.(*pb.Batch).Changelogs {
					seen[uint64(log.Version)] = struct{}{}
				}
			}
		}
	}
	return from, nil
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
	end, err := r.Seek(-8, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek footer length: %w", err)
	}
	if end < 0 {
		return fmt.Errorf("failed to read footer length: %w", io.ErrUnexpectedEOF)
	}
	var length [8]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return fmt.Errorf("failed to read footer length: %w", err)
	}
	h.Len = binary.BigEndian.Uint64(length[:])
	if h.Len > uint64(end) || h.Len > uint64(^uint(0)>>1) {
		return fmt.Errorf("invalid footer length %d, available %d", h.Len, end)
	}
	if _, err := r.Seek(end-int64(h.Len), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek footer: %w", err)
	}
	data := make([]byte, int(h.Len))
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("failed to read footer: %w", err)
	}
	return h.unmarshal(data)
}

func (h *BakFooter) unmarshal(data []byte) error {
	h.Msg = &pb.Footer{}
	if err := proto.Unmarshal(data, h.Msg); err != nil {
		return fmt.Errorf("failed to unmarshal footer: %w", err)
	}
	if h.Msg.Magic != BakMagic {
		return fmt.Errorf("invalid magic number %d, expect %d", h.Msg.Magic, BakMagic)
	}
	if h.Msg.Version != BakVersion {
		return fmt.Errorf("unsupported backup version %d", h.Msg.Version)
	}
	switch h.Msg.Source {
	case pb.Footer_UNKNOWN, pb.Footer_REDIS, pb.Footer_SQL, pb.Footer_KV:
	default:
		return fmt.Errorf("unsupported backup source %d", h.Msg.Source)
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
		} else if v.Quotas != nil || v.UserQuotas != nil || v.GroupQuotas != nil {
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
		} else if v.Changelogs != nil {
			s.typ = uint32(segTypeChangeLog)
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
			return uint64(len(b.Quotas) + len(b.UserQuotas) + len(b.GroupQuotas))
		case segTypeParent:
			return uint64(len(b.Parents))
		case segTypeChangeLog:
			return uint64(len(b.Changelogs))
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
	n, err := io.ReadFull(r, data)
	if err != nil {
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

func (c *DumpedCounters) updateFromSegment(seg *BakSegment, counters *[]*pb.Counter) bool {
	recordInode := func(inode uint64) {
		if Ino(inode) < TrashInode {
			c.NextInode = max(c.NextInode, int64(inode)+1)
		} else {
			c.NextTrash = max(c.NextTrash, int64(Ino(inode)-TrashInode))
		}
	}
	switch seg.typ {
	case segTypeCounter:
		batch := seg.val.(*pb.Batch)
		*counters = append(*counters, batch.Counters...)
		for _, counter := range batch.Counters {
			switch counter.Key {
			case "nextInode":
				c.NextInode = max(c.NextInode, counter.Value)
			case "nextChunk":
				c.NextChunk = max(c.NextChunk, counter.Value)
			case "nextSession":
				c.NextSession = max(c.NextSession, counter.Value)
			case "nextTrash":
				c.NextTrash = max(c.NextTrash, counter.Value)
			}
		}
		return true
	case segTypeNode:
		var attr Attr
		for _, node := range seg.val.(*pb.Batch).Nodes {
			recordInode(node.Inode)
			if Ino(node.Inode) != RootInode && Ino(node.Inode) != TrashInode {
				attr.Unmarshal(node.Data)
				c.UsedSpace += align4K(attr.Length)
				c.UsedInodes++
			}
		}
	case segTypeChunk:
		for _, chunk := range seg.val.(*pb.Batch).Chunks {
			recordInode(chunk.Inode)
			for _, s := range readSliceBuf(chunk.Slices) {
				c.NextChunk = max(c.NextChunk, int64(s.id)+1)
			}
		}
	case segTypeSliceRef:
		for _, ref := range seg.val.(*pb.Batch).SliceRefs {
			c.NextChunk = max(c.NextChunk, int64(ref.Id)+1)
		}
	case segTypeSustained:
		for _, sustained := range seg.val.(*pb.Batch).Sustained {
			c.NextSession = max(c.NextSession, int64(sustained.Sid))
			for _, inode := range sustained.Inodes {
				recordInode(inode)
			}
		}
	case segTypeDelFile:
		for _, file := range seg.val.(*pb.Batch).Delfiles {
			recordInode(file.Inode)
		}
	}
	return false
}

func (c *DumpedCounters) toBatch(counters []*pb.Counter) *pb.Batch {
	others := counters[:0]
	for _, counter := range counters {
		switch counter.Key {
		case usedSpace, totalInodes, "nextInode", "nextChunk", "nextSession", "nextTrash":
		default:
			others = append(others, counter)
		}
	}
	return &pb.Batch{Counters: append(others,
		&pb.Counter{Key: usedSpace, Value: c.UsedSpace},
		&pb.Counter{Key: totalInodes, Value: c.UsedInodes},
		&pb.Counter{Key: "nextInode", Value: c.NextInode},
		&pb.Counter{Key: "nextChunk", Value: c.NextChunk},
		&pb.Counter{Key: "nextSession", Value: c.NextSession},
		&pb.Counter{Key: "nextTrash", Value: c.NextTrash},
	)}
}

// transaction

type txSessionKey struct{}
type txMaxRetryKey struct{}
