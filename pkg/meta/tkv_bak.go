/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

func (m *kvMeta) getBatchNum() int {
	batch := 10240
	if m.Name() == "etcd" {
		batch = 128
	}
	return batch
}

func (m *kvMeta) buildDumpedSeg(typ int, opt *DumpOption) iDumpedSeg {
	ds := dumpedSeg{typ: typ, meta: m, opt: opt}
	switch typ {
	case SegTypeFormat:
		return &formatDS{ds}
	case SegTypeCounter:
		return &kvCounterDS{ds}
	case SegTypeSustained:
		return &kvSustainedDS{ds}
	case SegTypeDelFile:
		return &kvDelFileDS{ds}
	case SegTypeSliceRef:
		return &kvSliceRefDS{ds}
	case SegTypeAcl:
		return &kvAclDS{ds}
	case SegTypeMix:
		return &kvMixDS{
			ds,
			[]*sync.Pool{
				{New: func() interface{} { return &pb.Node{} }},
				{New: func() interface{} { return &pb.Edge{} }},
				{New: func() interface{} { return &pb.Chunk{} }},
				{New: func() interface{} { return &pb.Slice{} }},
				{New: func() interface{} { return &pb.Symlink{} }},
				{New: func() interface{} { return &pb.Xattr{} }},
				{New: func() interface{} { return &pb.Parent{} }},
			},
		}
	case SegTypeQuota:
		return &kvQuotaDS{ds}
	case SegTypeStat:
		return &kvStatDS{ds}
	}
	return nil
}

var kvLoadedPoolOnce sync.Once
var kvLoadedPools = make(map[int][]*sync.Pool)

func (m *kvMeta) buildLoadedPools(typ int) []*sync.Pool {
	kvLoadedPoolOnce.Do(func() {
		kvLoadedPools = map[int][]*sync.Pool{
			SegTypeNode:  {{New: func() interface{} { return make([]byte, BakNodeSizeWithoutAcl) }}, {New: func() interface{} { return make([]byte, BakNodeSize) }}},
			SegTypeChunk: {{New: func() interface{} { return make([]byte, sliceBytes*10) }}},
			SegTypeEdge:  {{New: func() interface{} { return make([]byte, 9) }}},
		}
	})
	return kvLoadedPools[typ]
}

func (m *kvMeta) buildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	switch typ {
	case SegTypeFormat:
		return &kvFormatLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeCounter:
		return &kvCounterLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSustained:
		return &kvSustainedLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeDelFile:
		return &kvDelFileLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSliceRef:
		return &kvSliceRefLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeAcl:
		return &kvAclLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeXattr:
		return &kvXattrLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeQuota:
		return &kvQuotaLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeStat:
		return &kvStatLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeNode:
		return &kvNodeLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeChunk:
		return &kvChunkLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeEdge:
		return &kvEdgeLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeParent:
		return &kvParentLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSymlink:
		return &kvSymlinkLS{loadedSeg{typ: typ, meta: m}}
	}
	return nil
}

type kvCounterDS struct {
	dumpedSeg
}

func getKVCounterFields(c *pb.Counters) map[string]*int64 {
	return map[string]*int64{
		usedSpace:     &c.UsedSpace,
		totalInodes:   &c.UsedInodes,
		"nextInode":   &c.NextInode,
		"nextChunk":   &c.NextChunk,
		"nextSession": &c.NextSession,
		"nextTrash":   &c.NextTrash,
	}
}

func (s *kvCounterDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	msg := &pb.Counters{}
	if err := m.txn(func(tx *kvTxn) error {
		fields := getKVCounterFields(msg)

		names := make([]string, 0, len(fields))
		keys := make([][]byte, 0, len(fields))

		for name := range fields {
			names = append(names, name)
			keys = append(keys, m.counterKey(name))
		}
		vals := tx.gets(keys...)
		for i, r := range vals {
			if r != nil {
				*(fields[names[i]]) = parseCounter(r)
			}
		}
		ctx.WithValue("nextInode", msg.NextInode)
		return nil
	}); err != nil {
		return nil
	}
	logger.Debugf("dump counters %+v", msg)
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: msg})
}

type kvSustainedDS struct {
	dumpedSeg
}

func (s *kvSustainedDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	keys, err := m.scanKeys(m.fmtKey("SS"))
	if err != nil {
		return err
	}
	sids := make(map[uint64][]uint64)
	for _, key := range keys {
		b := utils.FromBuffer([]byte(key[2:])) // "SS"
		if b.Len() != 16 {
			return fmt.Errorf("invalid sustainedKey: %s", key)
		}
		sid := b.Get64()
		inode := uint64(m.decodeInode(b.Get(8)))
		sids[sid] = append(sids[sid], inode)
	}
	msg := &pb.SustainedList{
		List: make([]*pb.Sustained, 0, len(keys)),
	}
	for sid, inodes := range sids {
		msg.List = append(msg.List, &pb.Sustained{
			Sid:    sid,
			Inodes: inodes,
		})
	}
	logger.Debugf("dump %s num: %d", s, len(msg.List))
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: msg})
}

type kvDelFileDS struct {
	dumpedSeg
}

func (s *kvDelFileDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	vals, err := m.scanValues(m.fmtKey("D"), -1, nil)
	if err != nil {
		return err
	}
	list := &pb.DelFileList{List: make([]*pb.DelFile, 0, len(vals))}
	for k, v := range vals {
		b := utils.FromBuffer([]byte(k[1:])) // "D"
		if b.Len() != 16 {
			logger.Warnf("invalid delfileKey: %s", k)
			continue
		}
		inode := m.decodeInode(b.Get(8))
		list.List = append(list.List, &pb.DelFile{Inode: uint64(inode), Length: b.Get64(), Expire: m.parseInt64(v)})
	}
	logger.Debugf("dump %s num: %d", s, len(list.List))
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list})
}

type kvSliceRefDS struct {
	dumpedSeg
}

func (s *kvSliceRefDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	vals, err := m.scanValues(m.fmtKey("K"), -1, nil)
	if err != nil {
		return err
	}
	list := &pb.SliceRefList{List: make([]*pb.SliceRef, 0, len(vals))}
	for k, v := range vals {
		b := utils.FromBuffer([]byte(k[1:])) // "K"
		if b.Len() != 12 {
			logger.Warnf("invalid sliceRefKey: %s", k)
			continue
		}
		id := b.Get64()
		size := b.Get32()
		list.List = append(list.List, &pb.SliceRef{Id: id, Size: size, Refs: parseCounter(v) + 1})
	}
	logger.Debugf("dump %s num: %d", s, len(list.List))
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list})
}

type kvAclDS struct {
	dumpedSeg
}

func (s *kvAclDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	vals, err := m.scanValues(m.fmtKey("R"), -1, nil)
	if err != nil {
		return err
	}
	list := &pb.AclList{List: make([]*pb.Acl, 0, len(vals))}
	for k, v := range vals {
		b := utils.FromBuffer([]byte(k[1:])) // "R"
		if b.Len() != 4 {
			logger.Warnf("invalid aclKey: %s", k)
			continue
		}
		acl := UnmarshalAclPB(v)
		acl.Id = b.Get32()
		list.List = append(list.List, acl)
	}
	logger.Debugf("dump %s num: %d", s, len(list.List))
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list})
}

type kvMixDS struct {
	dumpedSeg
	pools []*sync.Pool
}

func (s *kvMixDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	kvBatchSize := m.getBatchNum()
	var lists = map[int]proto.Message{
		SegTypeNode:    &pb.NodeList{List: make([]*pb.Node, 0, kvBatchSize)},
		SegTypeEdge:    &pb.EdgeList{List: make([]*pb.Edge, 0, kvBatchSize)},
		SegTypeChunk:   &pb.ChunkList{List: make([]*pb.Chunk, 0, kvBatchSize)},
		SegTypeSymlink: &pb.SymlinkList{List: make([]*pb.Symlink, 0, kvBatchSize)},
		SegTypeXattr:   &pb.XattrList{List: make([]*pb.Xattr, 0, kvBatchSize)},
		SegTypeParent:  &pb.ParentList{List: make([]*pb.Parent, 0, kvBatchSize)},
	}
	var sums = map[int]*atomic.Uint64{
		SegTypeNode:    {},
		SegTypeEdge:    {},
		SegTypeChunk:   {},
		SegTypeSymlink: {},
		SegTypeXattr:   {},
		SegTypeParent:  {},
	}
	printSums := func(sums map[int]*atomic.Uint64) string {
		var p string
		for typ, sum := range sums {
			p += fmt.Sprintf("dump %s num: %d\n", SegType2Name[typ], sum.Load())
		}
		return p
	}

	type entry struct {
		typ  int
		elem proto.Message
	}
	entryCh := make(chan *entry, kvBatchSize*s.opt.CoNum)
	entryPool := &sync.Pool{
		New: func() interface{} {
			return &entry{}
		},
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(s.opt.CoNum)

	var wg sync.WaitGroup
	var err error // final error

	wg.Add(1)
	go func() {
		defer wg.Done()
		finished := false
		var n int
		for !finished {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-entryCh:
				if !ok {
					finished = true
					break
				}
				switch e.typ {
				case SegTypeNode:
					lists[SegTypeNode].(*pb.NodeList).List = append(lists[SegTypeNode].(*pb.NodeList).List, e.elem.(*pb.Node))
					n = len(lists[SegTypeNode].(*pb.NodeList).List)
				case SegTypeEdge:
					lists[SegTypeEdge].(*pb.EdgeList).List = append(lists[SegTypeEdge].(*pb.EdgeList).List, e.elem.(*pb.Edge))
					n = len(lists[SegTypeEdge].(*pb.EdgeList).List)
				case SegTypeChunk:
					lists[SegTypeChunk].(*pb.ChunkList).List = append(lists[SegTypeChunk].(*pb.ChunkList).List, e.elem.(*pb.Chunk))
					n = len(lists[SegTypeChunk].(*pb.ChunkList).List)
				case SegTypeSymlink:
					lists[SegTypeSymlink].(*pb.SymlinkList).List = append(lists[SegTypeSymlink].(*pb.SymlinkList).List, e.elem.(*pb.Symlink))
					n = len(lists[SegTypeSymlink].(*pb.SymlinkList).List)
				case SegTypeXattr:
					lists[SegTypeXattr].(*pb.XattrList).List = append(lists[SegTypeXattr].(*pb.XattrList).List, e.elem.(*pb.Xattr))
					n = len(lists[SegTypeXattr].(*pb.XattrList).List)
				case SegTypeParent:
					lists[SegTypeParent].(*pb.ParentList).List = append(lists[SegTypeParent].(*pb.ParentList).List, e.elem.(*pb.Parent))
					n = len(lists[SegTypeParent].(*pb.ParentList).List)
				}
				if n >= kvBatchSize {
					if err = dumpResult(ctx, ch, &dumpedResult{seg: s, msg: lists[e.typ]}); err != nil {
						return
					}
					lists[e.typ] = lists[e.typ].ProtoReflect().New().Interface()
				}
			}
		}
		for _, list := range lists {
			if err = dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list}); err != nil {
				return
			}
		}
	}()

	nextInode := uint64(ctx.Value("nextInode").(int64))
	offset := nextInode/uint64(s.opt.CoNum) + 1
	for left := uint64(1); left < nextInode; left += offset {
		right := left + offset
		right = utils.Min64(right, nextInode)

		start, end := make([]byte, 9), make([]byte, 9)
		start[0], end[0] = 'A', 'A'
		m.encodeInode(Ino(left), start[1:])
		m.encodeInode(Ino(right), end[1:])
		eg.Go(func() error {
			return m.txn(func(tx *kvTxn) error {
				var ent *entry
				tx.scan(start, end, false, func(k, v []byte) bool {
					if egCtx.Err() != nil {
						return false
					}
					if len(k) <= 9 || k[0] != 'A' {
						return true
					}
					ino := m.decodeInode(k[1:9])
					switch k[9] {
					case 'I':
						node := s.pools[0].Get().(*pb.Node)
						node.Inode = uint64(ino)
						UnmarshalNodePB(v, node)
						sums[SegTypeNode].Add(1)
						ent = entryPool.Get().(*entry)
						ent.typ, ent.elem = SegTypeNode, node
						entryCh <- ent
					case 'D':
						edge := s.pools[1].Get().(*pb.Edge)
						edge.Parent = uint64(ino)
						edge.Name = k[10:]
						typ, inode := m.parseEntry(v)
						edge.Type, edge.Inode = uint32(typ), uint64(inode)
						sums[SegTypeEdge].Add(1)
						ent = entryPool.Get().(*entry)
						ent.typ, ent.elem = SegTypeEdge, edge
						entryCh <- ent
					case 'C':
						n := len(v) / sliceBytes
						chk := s.pools[2].Get().(*pb.Chunk)
						chk.Inode = uint64(ino)
						chk.Index = binary.BigEndian.Uint32(k[10:])
						chk.Slices = make([]*pb.Slice, 0, n)
						var ps *pb.Slice
						for i := 0; i < n; i++ {
							ps = s.pools[3].Get().(*pb.Slice)
							UnmarshalSlicePB(v[i*sliceBytes:], ps)
							chk.Slices = append(chk.Slices, ps)
						}
						sums[SegTypeChunk].Add(1)
						ent = entryPool.Get().(*entry)
						ent.typ, ent.elem = SegTypeChunk, chk
						entryCh <- ent
					case 'S':
						sym := s.pools[4].Get().(*pb.Symlink)
						sym.Inode = uint64(ino)
						sym.Target = unescape(string(v))
						sums[SegTypeSymlink].Add(1)
						ent = entryPool.Get().(*entry)
						ent.typ, ent.elem = SegTypeSymlink, sym
						entryCh <- ent
					case 'X':
						xattr := s.pools[5].Get().(*pb.Xattr)
						xattr.Inode = uint64(ino)
						xattr.Name = string(k[10:])
						xattr.Value = v
						sums[SegTypeXattr].Add(1)
						ent = entryPool.Get().(*entry)
						ent.typ, ent.elem = SegTypeXattr, xattr
						entryCh <- ent
					case 'P':
						parent := s.pools[6].Get().(*pb.Parent)
						parent.Inode = uint64(ino)
						parent.Parent = uint64(m.decodeInode(k[10:]))
						parent.Cnt = parseCounter(v)
						sums[SegTypeParent].Add(1)
						ent = entryPool.Get().(*entry)
						ent.typ, ent.elem = SegTypeParent, parent
						entryCh <- ent
					}
					return true
				})
				return nil
			})
		})
	}

	if iErr := eg.Wait(); iErr != nil {
		ctx.Cancel()
		wg.Wait()
		return iErr
	}

	close(entryCh)
	wg.Wait()

	logger.Debugf("dump %s num: %s", s, printSums(sums))
	return err
}

func (s *kvMixDS) release(msg proto.Message) {
	switch list := msg.(type) {
	case *pb.NodeList:
		for _, node := range list.List {
			s.pools[0].Put(node)
		}
	case *pb.EdgeList:
		for _, edge := range list.List {
			s.pools[1].Put(edge)
		}
	case *pb.ChunkList:
		for _, chunk := range list.List {
			for _, slice := range chunk.Slices {
				s.pools[2].Put(slice)
			}
			s.pools[3].Put(chunk)
		}
	case *pb.SymlinkList:
		for _, symlink := range list.List {
			s.pools[4].Put(symlink)
		}
	case *pb.XattrList:
		for _, xattr := range list.List {
			s.pools[5].Put(xattr)
		}
	case *pb.ParentList:
		for _, parent := range list.List {
			s.pools[6].Put(parent)
		}
	}
}

type kvQuotaDS struct {
	dumpedSeg
}

func (s *kvQuotaDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	vals, err := m.scanValues(m.fmtKey("QD"), -1, nil)
	if err != nil {
		return err
	}

	ql := &pb.QuotaList{}
	for k, v := range vals {
		q := &pb.Quota{}
		q.Inode = uint64(m.decodeInode([]byte(k)[2:]))
		b := utils.FromBuffer(v)
		q.MaxSpace = int64(b.Get64())
		q.MaxInodes = int64(b.Get64())
		q.UsedSpace = int64(b.Get64())
		q.UsedInodes = int64(b.Get64())
		ql.List = append(ql.List, q)
	}
	logger.Debugf("dump %s num: %d", s, len(ql.List))
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: ql})
}

type kvStatDS struct {
	dumpedSeg
}

func (s *kvStatDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	vals, err := m.scanValues(m.fmtKey("U"), -1, nil)
	if err != nil {
		return err
	}

	sl := &pb.StatList{}
	for k, v := range vals {
		s := &pb.Stat{}
		s.Inode = uint64(m.decodeInode([]byte(k)[1:]))
		b := utils.FromBuffer(v)
		s.DataLength = int64(b.Get64())
		s.UsedSpace = int64(b.Get64())
		s.UsedInodes = int64(b.Get64())
		sl.List = append(sl.List, s)
	}
	logger.Debugf("dump %s num: %d", s, len(sl.List))
	return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: sl})
}

type kvFormatLS struct {
	loadedSeg
}

func (s *kvFormatLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	format := ConvertFormatFromPB(msg.(*pb.Format))
	fData, _ := json.MarshalIndent(*format, "", "")
	return m.txn(func(tx *kvTxn) error {
		tx.set(m.fmtKey("setting"), fData)
		return nil
	})
}

type kvCounterLS struct {
	loadedSeg
}

func (s *kvCounterLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		fields := getKVCounterFields(msg.(*pb.Counters))
		for k, v := range fields {
			tx.set(m.counterKey(k), packCounter(*v))
		}
		return nil
	})
}

type kvSustainedLS struct {
	loadedSeg
}

func (s *kvSustainedLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.SustainedList)
		for _, sustained := range list.List {
			for _, inode := range sustained.Inodes {
				tx.set(m.sustainedKey(sustained.Sid, Ino(inode)), []byte{1})
			}
		}
		return nil
	})
}

type kvDelFileLS struct {
	loadedSeg
}

func (s *kvDelFileLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.DelFileList)
		for _, f := range list.List {
			tx.set(m.delfileKey(Ino(f.Inode), f.Length), m.packInt64(f.Expire))
		}
		return nil
	})
}

type kvSliceRefLS struct {
	loadedSeg
}

func (s *kvSliceRefLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.SliceRefList)
		for _, r := range list.List {
			tx.set(m.sliceKey(r.Id, r.Size), packCounter(r.Refs-1))
		}
		return nil
	})
}

type kvAclLS struct {
	loadedSeg
}

func (s *kvAclLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.AclList)
		for _, acl := range list.List {
			tx.set(m.aclKey(acl.Id), MarshalAclPB(acl))
		}
		return nil
	})
}

type kvXattrLS struct {
	loadedSeg
}

func (s *kvXattrLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.XattrList)
		for _, xattr := range list.List {
			tx.set(m.xattrKey(Ino(xattr.Inode), xattr.Name), xattr.Value)
		}
		return nil
	})
}

type kvQuotaLS struct {
	loadedSeg
}

func (s *kvQuotaLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.QuotaList)
		for _, q := range list.List {
			b := utils.NewBuffer(32)
			b.Put64(uint64(q.MaxSpace))
			b.Put64(uint64(q.MaxInodes))
			b.Put64(uint64(q.UsedSpace))
			b.Put64(uint64(q.UsedInodes))
			tx.set(m.dirQuotaKey(Ino(q.Inode)), b.Bytes())
		}
		return nil
	})
}

type kvStatLS struct {
	loadedSeg
}

func (s *kvStatLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.StatList)
		for _, s := range list.List {
			b := utils.NewBuffer(24)
			b.Put64(uint64(s.DataLength))
			b.Put64(uint64(s.UsedSpace))
			b.Put64(uint64(s.UsedInodes))
			tx.set(m.dirStatKey(Ino(s.Inode)), b.Bytes())
		}
		return nil
	})
}

type kvNodeLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *kvNodeLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.NodeList)
		for _, pn := range list.List {
			var buff []byte
			if pn.AccessAclId|pn.DefaultAclId != aclAPI.None {
				buff = s.pools[1].Get().([]byte)
			} else {
				buff = s.pools[0].Get().([]byte)
			}
			MarshalNodePB(pn, buff)
			tx.set(m.inodeKey(Ino(pn.Inode)), buff)

			if pn.AccessAclId|pn.DefaultAclId != aclAPI.None {
				s.pools[1].Put(buff) // nolint:staticcheck
			} else {
				s.pools[0].Put(buff) // nolint:staticcheck
			}
		}
		return nil
	})
}

type kvChunkLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *kvChunkLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.ChunkList)
		for _, chk := range list.List {
			size := len(chk.Slices) * sliceBytes
			buff := s.pools[0].Get().([]byte)
			if len(buff) < size {
				buff = make([]byte, size)
			}
			for i, slice := range chk.Slices {
				MarshalSlicePB(slice, buff[i*sliceBytes:])
			}
			tx.set(m.chunkKey(Ino(chk.Inode), chk.Index), buff[:size])
			s.pools[0].Put(buff) // nolint:staticcheck
		}
		return nil
	})
}

type kvEdgeLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *kvEdgeLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.EdgeList)
		for _, edge := range list.List {
			buff := s.pools[0].Get().([]byte)
			MarshalEdgePB(edge, buff)
			tx.set(m.entryKey(Ino(edge.Parent), string(edge.Name)), buff)
			s.pools[0].Put(buff) // nolint:staticcheck
		}
		return nil
	})
}

type kvParentLS struct {
	loadedSeg
}

func (s *kvParentLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.ParentList)
		for _, parent := range list.List {
			tx.set(m.parentKey(Ino(parent.Inode), Ino(parent.Parent)), packCounter(parent.Cnt))
		}
		return nil
	})
}

type kvSymlinkLS struct {
	loadedSeg
}

func (s *kvSymlinkLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		list := msg.(*pb.SymlinkList)
		for _, symlink := range list.List {
			tx.set(m.symKey(Ino(symlink.Inode)), symlink.Target)
		}
		return nil
	})
}

func (m *kvMeta) prepareLoad(ctx Context) error {
	var exist bool
	err := m.txn(func(tx *kvTxn) error {
		exist = tx.exist(m.fmtKey())
		return nil
	})
	if err != nil {
		return err
	}
	if exist {
		return fmt.Errorf("database %s://%s is not empty", m.Name(), m.addr)
	}
	return nil
}
