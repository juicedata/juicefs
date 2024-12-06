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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

var (
	kvDumpBatchSize = 10000
)

func (m *kvMeta) buildDumpedSeg(typ int, opt *DumpOption, txn *eTxn) iDumpedSeg {
	ds := dumpedSeg{typ: typ, meta: m, opt: opt, txn: txn}
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
		return &kvMixDBS{dumpedBatchSeg{ds, []*sync.Pool{
			{New: func() interface{} { return &pb.Node{} }},
			{New: func() interface{} { return &pb.Edge{} }},
			{New: func() interface{} { return &pb.Chunk{} }},
			{New: func() interface{} { return &pb.Symlink{} }},
			{New: func() interface{} { return &pb.Xattr{} }},
			{New: func() interface{} { return &pb.Parent{} }},
		}}}
	case SegTypeQuota:
		return &kvQuotaDS{ds}
	case SegTypeStat:
		return &kvStatDS{ds}
	}
	return nil
}

func (m *kvMeta) buildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	ls := loadedSeg{typ: typ, meta: m}
	switch typ {
	case SegTypeFormat:
		return &kvFormatLS{ls}
	case SegTypeCounter:
		return &kvCounterLS{ls}
	case SegTypeSustained:
		return &kvSustainedLS{ls}
	case SegTypeDelFile:
		return &kvDelFileLS{ls}
	case SegTypeSliceRef:
		return &kvSliceRefLS{ls}
	case SegTypeAcl:
		return &kvAclLS{ls}
	case SegTypeXattr:
		return &kvXattrLS{ls}
	case SegTypeQuota:
		return &kvQuotaLS{ls}
	case SegTypeStat:
		return &kvStatLS{ls}
	case SegTypeNode:
		return &kvNodeLS{ls}
	case SegTypeChunk:
		return &kvChunkLS{ls}
	case SegTypeEdge:
		return &kvEdgeLS{ls}
	case SegTypeParent:
		return &kvParentLS{ls}
	case SegTypeSymlink:
		return &kvSymlinkLS{ls}
	}
	return nil
}

func (m *kvMeta) execETxn(ctx Context, txn *eTxn, f func(Context, *eTxn) error) error {
	ctx.WithValue(txMaxRetryKey{}, txn.opt.maxRetry)
	return m.roTxn(ctx, func(tx *kvTxn) error {
		txn.obj = tx
		return f(ctx, txn)
	})
}

func (m *kvMeta) execStmt(ctx context.Context, txn *eTxn, f func(*kvTxn) error) error {
	if txn.opt.notUsed {
		return m.roTxn(ctx, func(tx *kvTxn) error {
			return f(tx)
		})
	}

	var err error
	cnt := 0
	for cnt < txn.opt.maxStmtRetry {
		err = f(txn.obj.(*kvTxn))
		if err == nil || !m.shouldRetry(err) {
			break
		}
		cnt++
		time.Sleep(time.Duration(cnt) * time.Microsecond)
	}
	return err
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

type kvCounterDS struct {
	dumpedSeg
}

func (s *kvCounterDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		msg := &pb.Counters{}
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

		logger.Debugf("dump counters %+v", msg)
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: msg})
	})
}

type kvSustainedDS struct {
	dumpedSeg
}

func (s *kvSustainedDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		sids := make(map[uint64][]uint64)
		cnt := 0
		tx.scan(m.fmtKey("SS"), nextKey(m.fmtKey("SS")), true, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[2:])) // "SS"
			if b.Len() != 16 {
				logger.Warnf("invalid sustainedKey: %s", k)
				return true
			}
			sid := b.Get64()
			inode := uint64(m.decodeInode(b.Get(8)))
			sids[sid] = append(sids[sid], inode)
			cnt++
			return true
		})

		msg := &pb.SustainedList{
			List: make([]*pb.Sustained, 0, cnt),
		}
		for sid, inodes := range sids {
			msg.List = append(msg.List, &pb.Sustained{
				Sid:    sid,
				Inodes: inodes,
			})
		}
		logger.Debugf("dump %s num: %d", s, len(msg.List))
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: msg})
	})
}

type kvDelFileDS struct {
	dumpedSeg
}

func (s *kvDelFileDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		list := &pb.DelFileList{List: make([]*pb.DelFile, 0, 16)}
		tx.scan(m.fmtKey("D"), nextKey(m.fmtKey("D")), false, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[1:])) // "D"
			if b.Len() != 16 {
				logger.Warnf("invalid delfileKey: %s", k)
				return true
			}
			inode := m.decodeInode(b.Get(8))
			list.List = append(list.List, &pb.DelFile{Inode: uint64(inode), Length: b.Get64(), Expire: m.parseInt64(v)})
			return true
		})

		logger.Debugf("dump %s num: %d", s, len(list.List))
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list})
	})
}

type kvSliceRefDS struct {
	dumpedSeg
}

func (s *kvSliceRefDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		list := &pb.SliceRefList{List: make([]*pb.SliceRef, 0, 1024)}
		tx.scan(m.fmtKey("K"), nextKey(m.fmtKey("K")), false, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[1:])) // "K"
			if b.Len() != 12 {
				logger.Warnf("invalid sliceRefKey: %s", k)
				return true
			}
			id := b.Get64()
			size := b.Get32()
			list.List = append(list.List, &pb.SliceRef{Id: id, Size: size, Refs: parseCounter(v) + 1})
			return true
		})
		logger.Debugf("dump %s num: %d", s, len(list.List))
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list})
	})
}

type kvAclDS struct {
	dumpedSeg
}

func (s *kvAclDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		acls := &pb.AclList{List: make([]*pb.Acl, 0, 16)}
		tx.scan(m.fmtKey("R"), nextKey(m.fmtKey("R")), false, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[1:])) // "R"
			if b.Len() != 4 {
				logger.Warnf("invalid aclKey: %s", k)
				return true
			}
			acls.List = append(acls.List, &pb.Acl{Id: b.Get32(), Data: v})
			return true
		})
		logger.Debugf("dump %s num: %d", s, len(acls.List))
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: acls})
	})
}

type kvMixDBS struct {
	dumpedBatchSeg
}

func splitInodeRange(n byte) [][2]byte {
	if n == 0 {
		return nil
	}

	step := 0xFF / n
	intervals := make([][2]byte, 0, n)

	for i := byte(0); i < n; i++ {
		start, end := i*step, (i+1)*step
		if i == n-1 {
			end = 0xFF
		}
		intervals = append(intervals, [2]byte{start, end})
	}
	return intervals
}

func printSums(sums map[int]*atomic.Uint64) string {
	var p string
	for typ, sum := range sums {
		p += fmt.Sprintf("%s num: %d\n", SegType2Name[typ], sum.Load())
	}
	return p
}

func (s *kvMixDBS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	var lists = map[int]proto.Message{
		SegTypeNode:    &pb.NodeList{List: make([]*pb.Node, 0, kvDumpBatchSize)},
		SegTypeEdge:    &pb.EdgeList{List: make([]*pb.Edge, 0, kvDumpBatchSize)},
		SegTypeChunk:   &pb.ChunkList{List: make([]*pb.Chunk, 0, kvDumpBatchSize)},
		SegTypeSymlink: &pb.SymlinkList{List: make([]*pb.Symlink, 0, kvDumpBatchSize)},
		SegTypeXattr:   &pb.XattrList{List: make([]*pb.Xattr, 0, kvDumpBatchSize)},
		SegTypeParent:  &pb.ParentList{List: make([]*pb.Parent, 0, kvDumpBatchSize)},
	}
	var sums = map[int]*atomic.Uint64{
		SegTypeNode:    {},
		SegTypeEdge:    {},
		SegTypeChunk:   {},
		SegTypeSymlink: {},
		SegTypeXattr:   {},
		SegTypeParent:  {},
	}

	var err error // final error
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(s.opt.CoNum)

	type entry struct {
		k []byte
		v []byte
	}
	entryPool := &sync.Pool{
		New: func() interface{} {
			return &entry{}
		},
	}
	entryCh := make(chan *entry, kvDumpBatchSize)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var e *entry
		var typ int
		var n int
		for {
			select {
			case <-ctx.Done():
				return
			case e = <-entryCh:
			}
			if e == nil {
				break
			}
			ino := m.decodeInode(e.k[1:9])
			switch e.k[9] {
			case 'I':
				typ = SegTypeNode
				node := s.pools[0].Get().(*pb.Node)
				node.Inode = uint64(ino)
				node.Data = e.v
				lists[SegTypeNode].(*pb.NodeList).List = append(lists[SegTypeNode].(*pb.NodeList).List, node)
				n = len(lists[SegTypeNode].(*pb.NodeList).List)
			case 'D':
				typ = SegTypeEdge
				edge := s.pools[1].Get().(*pb.Edge)
				edge.Parent = uint64(ino)
				edge.Name = e.k[10:]
				typ, inode := m.parseEntry(e.v)
				edge.Type, edge.Inode = uint32(typ), uint64(inode)
				lists[SegTypeEdge].(*pb.EdgeList).List = append(lists[SegTypeEdge].(*pb.EdgeList).List, edge)
				n = len(lists[SegTypeEdge].(*pb.EdgeList).List)
			case 'C':
				typ = SegTypeChunk
				chk := s.pools[2].Get().(*pb.Chunk)
				chk.Inode = uint64(ino)
				chk.Index = binary.BigEndian.Uint32(e.k[10:])
				chk.Slices = e.v
				lists[SegTypeChunk].(*pb.ChunkList).List = append(lists[SegTypeChunk].(*pb.ChunkList).List, chk)
				n = len(lists[SegTypeChunk].(*pb.ChunkList).List)
			case 'S':
				typ = SegTypeSymlink
				sym := s.pools[3].Get().(*pb.Symlink)
				sym.Inode = uint64(ino)
				sym.Target = unescape(string(e.v))
				lists[SegTypeSymlink].(*pb.SymlinkList).List = append(lists[SegTypeSymlink].(*pb.SymlinkList).List, sym)
				n = len(lists[SegTypeSymlink].(*pb.SymlinkList).List)
			case 'X':
				typ = SegTypeXattr
				xattr := s.pools[4].Get().(*pb.Xattr)
				xattr.Inode = uint64(ino)
				xattr.Name = string(e.k[10:])
				xattr.Value = e.v
				lists[SegTypeXattr].(*pb.XattrList).List = append(lists[SegTypeXattr].(*pb.XattrList).List, xattr)
				n = len(lists[SegTypeXattr].(*pb.XattrList).List)
			case 'P':
				typ = SegTypeParent
				parent := s.pools[5].Get().(*pb.Parent)
				parent.Inode = uint64(ino)
				parent.Parent = uint64(m.decodeInode(e.k[10:]))
				parent.Cnt = parseCounter(e.v)
				lists[SegTypeParent].(*pb.ParentList).List = append(lists[SegTypeParent].(*pb.ParentList).List, parent)
				n = len(lists[SegTypeParent].(*pb.ParentList).List)
			default:
				typ = SegTypeUnknown
			}
			entryPool.Put(e)
			if typ != SegTypeUnknown {
				sums[typ].Add(1)
				if n >= kvDumpBatchSize {
					if err = dumpResult(ctx, ch, &dumpedResult{seg: s, msg: lists[typ]}); err != nil {
						return
					}
					lists[typ] = lists[typ].ProtoReflect().New().Interface()
				}
			}
		}
		for _, list := range lists {
			_ = dumpResult(ctx, ch, &dumpedResult{seg: s, msg: list})
		}
	}()

	if s.opt.CoNum > 0xFF {
		s.opt.CoNum = 0xFF
	}
	rs := splitInodeRange(byte(s.opt.CoNum))

	for i, r := range rs {
		start, end := []byte{'A', r[0]}, []byte{'A', r[1]}
		if i == len(rs)-1 {
			end = []byte{'B'}
		}
		logger.Debugf("range: %v-%v", start, end)
		eg.Go(func() error {
			return m.execStmt(egCtx, s.txn, func(tx *kvTxn) error {
				var ent *entry
				tx.scan(start, end, false, func(k, v []byte) bool {
					if egCtx.Err() != nil {
						return false
					}
					if len(k) <= 9 || k[0] != 'A' {
						return true
					}
					ent = entryPool.Get().(*entry)
					ent.k, ent.v = k, v
					entryCh <- ent
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

	logger.Infof("dump %s num: %s", s, printSums(sums))
	return err
}

func (s *kvMixDBS) release(msg proto.Message) {
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
			s.pools[2].Put(chunk)
		}
	case *pb.SymlinkList:
		for _, symlink := range list.List {
			s.pools[3].Put(symlink)
		}
	case *pb.XattrList:
		for _, xattr := range list.List {
			s.pools[4].Put(xattr)
		}
	case *pb.ParentList:
		for _, parent := range list.List {
			s.pools[5].Put(parent)
		}
	}
}

type kvQuotaDS struct {
	dumpedSeg
}

func (s *kvQuotaDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		ql := &pb.QuotaList{List: make([]*pb.Quota, 0, 16)}
		tx.scan(m.fmtKey("QD"), nextKey(m.fmtKey("QD")), false, func(k, v []byte) bool {
			q := &pb.Quota{}
			q.Inode = uint64(m.decodeInode([]byte(k)[2:]))
			b := utils.FromBuffer(v)
			q.MaxSpace = int64(b.Get64())
			q.MaxInodes = int64(b.Get64())
			q.UsedSpace = int64(b.Get64())
			q.UsedInodes = int64(b.Get64())
			ql.List = append(ql.List, q)
			return true
		})
		logger.Debugf("dump %s num: %d", s, len(ql.List))
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: ql})
	})
}

type kvStatDS struct {
	dumpedSeg
}

func (s *kvStatDS) dump(ctx Context, ch chan *dumpedResult) error {
	m := s.meta.(*kvMeta)
	return m.execStmt(ctx, s.txn, func(tx *kvTxn) error {
		sl := &pb.StatList{List: make([]*pb.Stat, 0, 16)}
		tx.scan(m.fmtKey("U"), nextKey(m.fmtKey("U")), false, func(k, v []byte) bool {
			s := &pb.Stat{}
			s.Inode = uint64(m.decodeInode([]byte(k)[1:]))
			b := utils.FromBuffer(v)
			s.DataLength = int64(b.Get64())
			s.UsedSpace = int64(b.Get64())
			s.UsedInodes = int64(b.Get64())
			sl.List = append(sl.List, s)
			return true
		})
		logger.Debugf("dump %s num: %d", s, len(sl.List))
		return dumpResult(ctx, ch, &dumpedResult{seg: s, msg: sl})
	})
}

type kvFormatLS struct {
	loadedSeg
}

func (s *kvFormatLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	return m.txn(func(tx *kvTxn) error {
		tx.set(m.fmtKey("setting"), msg.(*pb.Format).Data)
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

func (m *kvMeta) insertKVs(keys [][]byte, values [][]byte) error {
	maxSize, maxNum := 5<<20, 10240
	if m.Name() == "etcd" {
		maxNum = 128
	}
	n := len(keys)
	last, num, size := 0, 0, 0
	for i := 0; i < n; i++ {
		num++
		size += len(keys[i]) + len(values[i])
		if num >= maxNum || size >= maxSize || i >= n-1 {
			if err := m.txn(func(tx *kvTxn) error {
				for j := last; j <= i; j++ {
					tx.set(keys[j], values[j])
				}
				return nil
			}); err != nil {
				return err
			}
			num, size, last = 0, 0, i+1
		}
	}
	return nil
}

type kvSustainedLS struct {
	loadedSeg
}

func (s *kvSustainedLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.SustainedList)
	var keys, vals [][]byte
	for _, sustained := range list.List {
		for _, inode := range sustained.Inodes {
			keys = append(keys, m.sustainedKey(sustained.Sid, Ino(inode)))
			vals = append(vals, []byte{1})
		}
	}
	return m.insertKVs(keys, vals)
}

type kvDelFileLS struct {
	loadedSeg
}

func (s *kvDelFileLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.DelFileList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, f := range list.List {
		keys = append(keys, m.delfileKey(Ino(f.Inode), f.Length))
		vals = append(vals, m.packInt64(f.Expire))
	}
	return m.insertKVs(keys, vals)
}

type kvSliceRefLS struct {
	loadedSeg
}

func (s *kvSliceRefLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.SliceRefList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, r := range list.List {
		keys = append(keys, m.sliceKey(r.Id, r.Size))
		vals = append(vals, packCounter(r.Refs-1))
	}
	return m.insertKVs(keys, vals)
}

type kvAclLS struct {
	loadedSeg
}

func (s *kvAclLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.AclList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	var maxId uint32 = 0
	for _, acl := range list.List {
		if acl.Id > maxId {
			maxId = acl.Id
		}
		keys = append(keys, m.aclKey(acl.Id))
		vals = append(vals, acl.Data)
	}

	if err := m.insertKVs(keys, vals); err != nil {
		return err
	}

	return m.txn(func(tx *kvTxn) error {
		tx.set(m.counterKey(aclCounter), packCounter(int64(maxId)))
		return nil
	})
}

type kvXattrLS struct {
	loadedSeg
}

func (s *kvXattrLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.XattrList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, xattr := range list.List {
		keys = append(keys, m.xattrKey(Ino(xattr.Inode), xattr.Name))
		vals = append(vals, xattr.Value)
	}
	return m.insertKVs(keys, vals)
}

type kvQuotaLS struct {
	loadedSeg
}

func (s *kvQuotaLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.QuotaList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, q := range list.List {
		b := utils.NewBuffer(32)
		b.Put64(uint64(q.MaxSpace))
		b.Put64(uint64(q.MaxInodes))
		b.Put64(uint64(q.UsedSpace))
		b.Put64(uint64(q.UsedInodes))
		keys = append(keys, m.dirQuotaKey(Ino(q.Inode)))
		vals = append(vals, b.Bytes())
	}
	return m.insertKVs(keys, vals)
}

type kvStatLS struct {
	loadedSeg
}

func (s *kvStatLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.StatList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, s := range list.List {
		b := utils.NewBuffer(24)
		b.Put64(uint64(s.DataLength))
		b.Put64(uint64(s.UsedSpace))
		b.Put64(uint64(s.UsedInodes))
		keys = append(keys, m.dirStatKey(Ino(s.Inode)))
		vals = append(vals, b.Bytes())
	}
	return m.insertKVs(keys, vals)
}

type kvNodeLS struct {
	loadedSeg
}

func (s *kvNodeLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.NodeList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, pn := range list.List {
		keys = append(keys, m.inodeKey(Ino(pn.Inode)))
		vals = append(vals, pn.Data)
	}
	return m.insertKVs(keys, vals)
}

type kvChunkLS struct {
	loadedSeg
}

func (s *kvChunkLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.ChunkList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, chk := range list.List {
		keys = append(keys, m.chunkKey(Ino(chk.Inode), chk.Index))
		vals = append(vals, chk.Slices)
	}
	return m.insertKVs(keys, vals)
}

type kvEdgeLS struct {
	loadedSeg
}

func (s *kvEdgeLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)

	list := msg.(*pb.EdgeList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, edge := range list.List {
		buff := utils.NewBuffer(9)
		buff.Put8(uint8(edge.Type))
		buff.Put64(edge.Inode)
		keys = append(keys, m.entryKey(Ino(edge.Parent), string(edge.Name)))
		vals = append(vals, buff.Bytes())
	}
	return m.insertKVs(keys, vals)
}

type kvParentLS struct {
	loadedSeg
}

func (s *kvParentLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.ParentList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, parent := range list.List {
		keys = append(keys, m.parentKey(Ino(parent.Inode), Ino(parent.Parent)))
		vals = append(vals, packCounter(parent.Cnt))
	}
	return m.insertKVs(keys, vals)
}

type kvSymlinkLS struct {
	loadedSeg
}

func (s *kvSymlinkLS) load(ctx Context, msg proto.Message) error {
	m := s.meta.(*kvMeta)
	list := msg.(*pb.SymlinkList)
	keys, vals := make([][]byte, 0, len(list.List)), make([][]byte, 0, len(list.List))
	for _, symlink := range list.List {
		keys = append(keys, m.symKey(Ino(symlink.Inode)))
		vals = append(vals, []byte(symlink.Target))
	}
	return m.insertKVs(keys, vals)
}

func (m *kvMeta) prepareLoad(ctx Context, opt *LoadOption) error {
	opt.check()
	// concurrent load is not supported , may cause lots of txn conflicts.
	opt.CoNum = 1

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
