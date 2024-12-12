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

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

var (
	kvDumpBatchSize = 10000
)

func (m *kvMeta) dump(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var dumps = []func(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error{
		m.dumpFormat,
		m.dumpCounters,
		m.dumpMix, // node, edge, chunk, symlink, xattr, parent
		m.dumpSustained,
		m.dumpDelFiles,
		m.dumpSliceRef,
		m.dumpACL,
		m.dumpQuota,
		m.dumpDirStat,
	}
	ts := m.client.config("startTS")
	if ts != nil {
		logger.Infof("dump kv with startTS: %d", ts.(uint64))
		ctx.WithValue(txSessionKey{}, ts)
	}

	for _, f := range dumps {
		err := f(ctx, opt, ch)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *kvMeta) load(ctx Context, typ int, opt *LoadOption, val proto.Message) error {
	switch typ {
	case segTypeFormat:
		return m.loadFormat(ctx, val)
	case segTypeCounter:
		return m.loadCounters(ctx, val)
	case segTypeNode:
		return m.loadNodes(ctx, val)
	case segTypeChunk:
		return m.loadChunks(ctx, val)
	case segTypeEdge:
		return m.loadEdges(ctx, val)
	case segTypeSymlink:
		return m.loadSymlinks(ctx, val)
	case segTypeSustained:
		return m.loadSustained(ctx, val)
	case segTypeDelFile:
		return m.loadDelFiles(ctx, val)
	case segTypeSliceRef:
		return m.loadSliceRefs(ctx, val)
	case segTypeAcl:
		return m.loadAcl(ctx, val)
	case segTypeXattr:
		return m.loadXattrs(ctx, val)
	case segTypeQuota:
		return m.loadQuota(ctx, val)
	case segTypeStat:
		return m.loadDirStats(ctx, val)
	case segTypeParent:
		return m.loadParents(ctx, val)
	default:
		logger.Warnf("skip segment type %d", typ)
		return nil
	}
}

func (m *kvMeta) prepareLoad(ctx Context, opt *LoadOption) error {
	opt.check()
	opt.Threads = 1
	logger.Infof("concurrent load is currently not supported , may cause lots of txn conflicts.")

	var exist bool
	err := m.txn(ctx, func(tx *kvTxn) error {
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

func printSums(sums map[int]*atomic.Uint64) string {
	var p string
	for typ, sum := range sums {
		p += fmt.Sprintf("%d num: %d\n", typ, sum.Load())
	}
	return p
}

func (m *kvMeta) dumpCounters(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		counters := make([]*pb.Counter, 0, len(counterNames))
		for _, name := range counterNames {
			val := tx.get(m.counterKey(name))
			counters = append(counters, &pb.Counter{Key: name, Value: parseCounter(val)})
		}
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Counters: counters}})
	})
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

func (m *kvMeta) dumpMix(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	pools := map[int]*sync.Pool{
		segTypeNode:    {New: func() interface{} { return &pb.Node{} }},
		segTypeEdge:    {New: func() interface{} { return &pb.Edge{} }},
		segTypeChunk:   {New: func() interface{} { return &pb.Chunk{} }},
		segTypeSymlink: {New: func() interface{} { return &pb.Symlink{} }},
		segTypeXattr:   {New: func() interface{} { return &pb.Xattr{} }},
		segTypeParent:  {New: func() interface{} { return &pb.Parent{} }},
	}
	release := func(msg proto.Message) {
		batch := msg.(*pb.Batch)
		for _, node := range batch.Nodes {
			pools[segTypeNode].Put(node)
		}
		for _, edge := range batch.Edges {
			pools[segTypeEdge].Put(edge)
		}
		for _, chunk := range batch.Chunks {
			pools[segTypeChunk].Put(chunk)
		}
		for _, symlink := range batch.Symlinks {
			pools[segTypeSymlink].Put(symlink)
		}
		for _, xattr := range batch.Xattrs {
			pools[segTypeXattr].Put(xattr)
		}
		for _, parent := range batch.Parents {
			pools[segTypeParent].Put(parent)
		}
	}

	var sums = map[int]*atomic.Uint64{
		segTypeNode:    {},
		segTypeEdge:    {},
		segTypeChunk:   {},
		segTypeSymlink: {},
		segTypeXattr:   {},
		segTypeParent:  {},
	}
	createMsg := func(typ int) *pb.Batch {
		switch typ {
		case segTypeNode:
			return &pb.Batch{Nodes: make([]*pb.Node, 0, kvDumpBatchSize)}
		case segTypeEdge:
			return &pb.Batch{Edges: make([]*pb.Edge, 0, kvDumpBatchSize)}
		case segTypeChunk:
			return &pb.Batch{Chunks: make([]*pb.Chunk, 0, kvDumpBatchSize)}
		case segTypeSymlink:
			return &pb.Batch{Symlinks: make([]*pb.Symlink, 0, kvDumpBatchSize)}
		case segTypeXattr:
			return &pb.Batch{Xattrs: make([]*pb.Xattr, 0, kvDumpBatchSize)}
		case segTypeParent:
			return &pb.Batch{Parents: make([]*pb.Parent, 0, kvDumpBatchSize)}
		default:
			return nil
		}
	}
	var lists = make(map[int]*pb.Batch)
	for typ := range sums {
		lists[typ] = createMsg(typ)
	}

	var err error // final error
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.Threads)

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
				typ = segTypeNode
				node := pools[typ].Get().(*pb.Node)
				node.Inode = uint64(ino)
				node.Data = e.v
				lists[typ].Nodes = append(lists[typ].Nodes, node)
				n = len(lists[typ].Nodes)
			case 'D':
				typ = segTypeEdge
				edge := pools[typ].Get().(*pb.Edge)
				edge.Parent = uint64(ino)
				edge.Name = e.k[10:]
				nTyp, inode := m.parseEntry(e.v)
				edge.Type, edge.Inode = uint32(nTyp), uint64(inode)
				lists[typ].Edges = append(lists[typ].Edges, edge)
				n = len(lists[typ].Edges)
			case 'C':
				typ = segTypeChunk
				chk := pools[typ].Get().(*pb.Chunk)
				chk.Inode = uint64(ino)
				chk.Index = binary.BigEndian.Uint32(e.k[10:])
				chk.Slices = e.v
				lists[typ].Chunks = append(lists[typ].Chunks, chk)
				n = len(lists[typ].Chunks)
			case 'S':
				typ = segTypeSymlink
				sym := pools[typ].Get().(*pb.Symlink)
				sym.Inode = uint64(ino)
				sym.Target = unescape(string(e.v))
				lists[typ].Symlinks = append(lists[typ].Symlinks, sym)
				n = len(lists[typ].Symlinks)
			case 'X':
				typ = segTypeXattr
				xattr := pools[typ].Get().(*pb.Xattr)
				xattr.Inode = uint64(ino)
				xattr.Name = string(e.k[10:])
				xattr.Value = e.v
				lists[typ].Xattrs = append(lists[typ].Xattrs, xattr)
				n = len(lists[typ].Xattrs)
			case 'P':
				typ = segTypeParent
				parent := pools[typ].Get().(*pb.Parent)
				parent.Inode = uint64(ino)
				parent.Parent = uint64(m.decodeInode(e.k[10:]))
				parent.Cnt = parseCounter(e.v)
				lists[typ].Parents = append(lists[typ].Parents, parent)
				n = len(lists[typ].Parents)
			default:
				typ = segTypeUnknown
			}
			entryPool.Put(e)
			if typ != segTypeUnknown {
				sums[typ].Add(1)
				if n >= kvDumpBatchSize {
					if err = dumpResult(ctx, ch, &dumpedResult{lists[typ], release}); err != nil {
						return
					}
					lists[typ] = createMsg(typ)
				}
			}
		}
		for _, list := range lists {
			_ = dumpResult(ctx, ch, &dumpedResult{list, release})
		}
	}()

	if opt.Threads > 0xFF {
		opt.Threads = 0xFF
	}
	rs := splitInodeRange(byte(opt.Threads))
	for i, r := range rs {
		start, end := []byte{'A', r[0]}, []byte{'A', r[1]}
		if i == len(rs)-1 {
			end = []byte{'B'}
		}
		logger.Debugf("range: %v-%v", start, end)
		eg.Go(func() error {
			return m.txn(egCtx, func(tx *kvTxn) error {
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
	return err
}

func (m *kvMeta) dumpSustained(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
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

		sustained := make([]*pb.Sustained, 0, cnt)
		for sid, inodes := range sids {
			sustained = append(sustained, &pb.Sustained{
				Sid:    sid,
				Inodes: inodes,
			})
		}
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Sustained: sustained}})
	})
}

func (m *kvMeta) dumpDelFiles(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		delFiles := make([]*pb.DelFile, 0, kvDumpBatchSize)
		tx.scan(m.fmtKey("D"), nextKey(m.fmtKey("D")), false, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[1:])) // "D"
			if b.Len() != 16 {
				logger.Warnf("invalid delfileKey: %s", k)
				return true
			}
			inode := m.decodeInode(b.Get(8))
			delFiles = append(delFiles, &pb.DelFile{Inode: uint64(inode), Length: b.Get64(), Expire: m.parseInt64(v)})
			return true
		})
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Delfiles: delFiles}})
	})
}

func (m *kvMeta) dumpSliceRef(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		sliceRefs := make([]*pb.SliceRef, 0, kvDumpBatchSize)
		tx.scan(m.fmtKey("K"), nextKey(m.fmtKey("K")), false, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[1:])) // "K"
			if b.Len() != 12 {
				logger.Warnf("invalid sliceRefKey: %s", k)
				return true
			}
			id := b.Get64()
			size := b.Get32()
			sliceRefs = append(sliceRefs, &pb.SliceRef{Id: id, Size: size, Refs: parseCounter(v) + 1})
			return true
		})
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{SliceRefs: sliceRefs}})
	})
}

func (m *kvMeta) dumpACL(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		acls := make([]*pb.Acl, 0, 128)
		tx.scan(m.fmtKey("R"), nextKey(m.fmtKey("R")), false, func(k, v []byte) bool {
			b := utils.FromBuffer([]byte(k[1:])) // "R"
			if b.Len() != 4 {
				logger.Warnf("invalid aclKey: %s", k)
				return true
			}
			acls = append(acls, &pb.Acl{Id: b.Get32(), Data: v})
			return true
		})
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Acls: acls}})
	})
}

func (m *kvMeta) dumpQuota(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		quotas := make([]*pb.Quota, 0, 128)
		tx.scan(m.fmtKey("QD"), nextKey(m.fmtKey("QD")), false, func(k, v []byte) bool {
			q := &pb.Quota{}
			q.Inode = uint64(m.decodeInode([]byte(k)[2:]))
			b := utils.FromBuffer(v)
			q.MaxSpace = int64(b.Get64())
			q.MaxInodes = int64(b.Get64())
			q.UsedSpace = int64(b.Get64())
			q.UsedInodes = int64(b.Get64())
			quotas = append(quotas, q)
			return true
		})
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Quotas: quotas}})
	})
}

func (m *kvMeta) dumpDirStat(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		stats := make([]*pb.Stat, 0, kvDumpBatchSize)
		tx.scan(m.fmtKey("U"), nextKey(m.fmtKey("U")), false, func(k, v []byte) bool {
			s := &pb.Stat{}
			s.Inode = uint64(m.decodeInode([]byte(k)[1:]))
			b := utils.FromBuffer(v)
			s.DataLength = int64(b.Get64())
			s.UsedSpace = int64(b.Get64())
			s.UsedInodes = int64(b.Get64())
			stats = append(stats, s)
			return true
		})
		return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Dirstats: stats}})
	})
}

func (m *kvMeta) loadFormat(ctx Context, msg proto.Message) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		tx.set(m.fmtKey("setting"), msg.(*pb.Format).Data)
		return nil
	})
}

func (m *kvMeta) loadCounters(ctx Context, msg proto.Message) error {
	return m.txn(ctx, func(tx *kvTxn) error {
		for _, counter := range msg.(*pb.Batch).Counters {
			tx.set(m.counterKey(counter.Key), packCounter(counter.Value))
		}
		return nil
	})
}

func (m *kvMeta) insertKVs(ctx context.Context, keys [][]byte, values [][]byte) error {
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
			if err := m.txn(ctx, func(tx *kvTxn) error {
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

func (m *kvMeta) loadNodes(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Nodes)), make([][]byte, 0, len(batch.Nodes))
	for _, pn := range batch.Nodes {
		keys = append(keys, m.inodeKey(Ino(pn.Inode)))
		vals = append(vals, pn.Data)
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadChunks(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Chunks)), make([][]byte, 0, len(batch.Chunks))
	for _, chk := range batch.Chunks {
		keys = append(keys, m.chunkKey(Ino(chk.Inode), chk.Index))
		vals = append(vals, chk.Slices)
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadEdges(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Edges)), make([][]byte, 0, len(batch.Edges))
	for _, edge := range batch.Edges {
		buff := utils.NewBuffer(9)
		buff.Put8(uint8(edge.Type))
		buff.Put64(edge.Inode)
		keys = append(keys, m.entryKey(Ino(edge.Parent), string(edge.Name)))
		vals = append(vals, buff.Bytes())
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadSymlinks(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Symlinks)), make([][]byte, 0, len(batch.Symlinks))
	for _, symlink := range batch.Symlinks {
		keys = append(keys, m.symKey(Ino(symlink.Inode)))
		vals = append(vals, []byte(symlink.Target))
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadSustained(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	var keys, vals [][]byte
	for _, sustained := range batch.Sustained {
		for _, inode := range sustained.Inodes {
			keys = append(keys, m.sustainedKey(sustained.Sid, Ino(inode)))
			vals = append(vals, []byte{1})
		}
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadDelFiles(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Delfiles)), make([][]byte, 0, len(batch.Delfiles))
	for _, f := range batch.Delfiles {
		keys = append(keys, m.delfileKey(Ino(f.Inode), f.Length))
		vals = append(vals, m.packInt64(f.Expire))
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadSliceRefs(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.SliceRefs)), make([][]byte, 0, len(batch.SliceRefs))
	for _, r := range batch.SliceRefs {
		keys = append(keys, m.sliceKey(r.Id, r.Size))
		vals = append(vals, packCounter(r.Refs-1))
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadAcl(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Acls)), make([][]byte, 0, len(batch.Acls))
	var maxId uint32 = 0
	for _, acl := range batch.Acls {
		if acl.Id > maxId {
			maxId = acl.Id
		}
		keys = append(keys, m.aclKey(acl.Id))
		vals = append(vals, acl.Data)
	}

	if err := m.insertKVs(ctx, keys, vals); err != nil {
		return err
	}

	return m.txn(ctx, func(tx *kvTxn) error {
		tx.set(m.counterKey(aclCounter), packCounter(int64(maxId)))
		return nil
	})
}

func (m *kvMeta) loadXattrs(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Xattrs)), make([][]byte, 0, len(batch.Xattrs))
	for _, xattr := range batch.Xattrs {
		keys = append(keys, m.xattrKey(Ino(xattr.Inode), xattr.Name))
		vals = append(vals, xattr.Value)
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadQuota(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Quotas)), make([][]byte, 0, len(batch.Quotas))
	for _, q := range batch.Quotas {
		b := utils.NewBuffer(32)
		b.Put64(uint64(q.MaxSpace))
		b.Put64(uint64(q.MaxInodes))
		b.Put64(uint64(q.UsedSpace))
		b.Put64(uint64(q.UsedInodes))
		keys = append(keys, m.dirQuotaKey(Ino(q.Inode)))
		vals = append(vals, b.Bytes())
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadDirStats(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Dirstats)), make([][]byte, 0, len(batch.Dirstats))
	for _, s := range batch.Dirstats {
		b := utils.NewBuffer(24)
		b.Put64(uint64(s.DataLength))
		b.Put64(uint64(s.UsedSpace))
		b.Put64(uint64(s.UsedInodes))
		keys = append(keys, m.dirStatKey(Ino(s.Inode)))
		vals = append(vals, b.Bytes())
	}
	return m.insertKVs(ctx, keys, vals)
}

func (m *kvMeta) loadParents(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	keys, vals := make([][]byte, 0, len(batch.Parents)), make([][]byte, 0, len(batch.Parents))
	for _, parent := range batch.Parents {
		keys = append(keys, m.parentKey(Ino(parent.Inode), Ino(parent.Parent)))
		vals = append(vals, packCounter(parent.Cnt))
	}
	return m.insertKVs(ctx, keys, vals)
}
