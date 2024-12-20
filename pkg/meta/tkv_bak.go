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
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
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
	if ts == nil && m.Name() == "tikv" {
		return errors.New("failed to get startTS, which is required for TiKV to ensure consistency")
	}
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
	return errors.New("not implemented, use kvMeta.LoadMetaV2 instead")
}

func (m *kvMeta) prepareLoad(ctx Context, opt *LoadOption) error {
	opt.check()

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

func (m *kvMeta) insertKVs(ctx context.Context, pairs []*pair, threads int) error {
	if len(pairs) == 0 {
		return nil
	}

	sort.Slice(pairs, func(i, j int) bool {
		return bytes.Compare(pairs[i].key, pairs[j].key) < 0
	})

	maxSize, maxNum := 5<<20, m.maxTxnBatchNum()
	n := len(pairs)
	last, num, size := 0, 0, 0

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(threads)

	for i, pair := range pairs {
		num++
		size += len(pair.key) + len(pair.value)
		if num >= maxNum || size >= maxSize || i >= n-1 {
			ePairs := pairs[last : i+1]
			num, size, last = 0, 0, i+1
			eg.Go(func() error {
				return m.txn(egCtx, func(tx *kvTxn) error {
					for _, ep := range ePairs {
						tx.set(ep.key, ep.value)
					}
					return nil
				})
			})
		}
	}
	return eg.Wait()
}

func (m *kvMeta) loadFormat(ctx Context, msg proto.Message, pairs *[]*pair) {
	*pairs = append(*pairs, &pair{m.fmtKey("setting"), msg.(*pb.Format).Data})
}

func (m *kvMeta) loadCounters(ctx Context, msg proto.Message, pairs *[]*pair) {
	for _, counter := range msg.(*pb.Batch).Counters {
		*pairs = append(*pairs, &pair{m.counterKey(counter.Key), packCounter(counter.Value)})
	}
}

func (m *kvMeta) loadNodes(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, pn := range batch.Nodes {
		*pairs = append(*pairs, &pair{m.inodeKey(Ino(pn.Inode)), pn.Data})
	}
}

func (m *kvMeta) loadChunks(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, chk := range batch.Chunks {
		*pairs = append(*pairs, &pair{m.chunkKey(Ino(chk.Inode), chk.Index), chk.Slices})
	}
}

func (m *kvMeta) loadEdges(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, edge := range batch.Edges {
		buff := utils.NewBuffer(9)
		buff.Put8(uint8(edge.Type))
		buff.Put64(edge.Inode)
		*pairs = append(*pairs, &pair{m.entryKey(Ino(edge.Parent), string(edge.Name)), buff.Bytes()})
	}
}

func (m *kvMeta) loadSymlinks(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, symlink := range batch.Symlinks {
		*pairs = append(*pairs, &pair{m.symKey(Ino(symlink.Inode)), []byte(symlink.Target)})
	}
}

func (m *kvMeta) loadSustained(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, sustained := range batch.Sustained {
		for _, inode := range sustained.Inodes {
			*pairs = append(*pairs, &pair{m.sustainedKey(sustained.Sid, Ino(inode)), []byte{1}})
		}
	}
}

func (m *kvMeta) loadDelFiles(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, f := range batch.Delfiles {
		*pairs = append(*pairs, &pair{m.delfileKey(Ino(f.Inode), f.Length), m.packInt64(f.Expire)})
	}
}

func (m *kvMeta) loadSliceRefs(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, r := range batch.SliceRefs {
		*pairs = append(*pairs, &pair{m.sliceKey(r.Id, r.Size), packCounter(r.Refs - 1)})
	}
}

func (m *kvMeta) loadAcl(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	var maxId uint32 = 0
	if val := ctx.Value("maxAclId"); val != nil {
		maxId = val.(uint32)
	}
	for _, acl := range batch.Acls {
		if acl.Id > maxId {
			maxId = acl.Id
		}
		*pairs = append(*pairs, &pair{m.aclKey(acl.Id), acl.Data})
	}
	ctx.WithValue("maxAclId", maxId)
}

func (m *kvMeta) loadXattrs(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, xattr := range batch.Xattrs {
		*pairs = append(*pairs, &pair{m.xattrKey(Ino(xattr.Inode), xattr.Name), xattr.Value})
	}
}

func (m *kvMeta) loadQuota(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, q := range batch.Quotas {
		b := utils.NewBuffer(32)
		b.Put64(uint64(q.MaxSpace))
		b.Put64(uint64(q.MaxInodes))
		b.Put64(uint64(q.UsedSpace))
		b.Put64(uint64(q.UsedInodes))
		*pairs = append(*pairs, &pair{m.dirQuotaKey(Ino(q.Inode)), b.Bytes()})
	}
}

func (m *kvMeta) loadDirStats(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, s := range batch.Dirstats {
		b := utils.NewBuffer(24)
		b.Put64(uint64(s.DataLength))
		b.Put64(uint64(s.UsedSpace))
		b.Put64(uint64(s.UsedInodes))
		*pairs = append(*pairs, &pair{m.dirStatKey(Ino(s.Inode)), b.Bytes()})
	}
}

func (m *kvMeta) loadParents(ctx Context, msg proto.Message, pairs *[]*pair) {
	batch := msg.(*pb.Batch)
	for _, parent := range batch.Parents {
		*pairs = append(*pairs, &pair{m.parentKey(Ino(parent.Inode), Ino(parent.Parent)), packCounter(parent.Cnt)})
	}
}

func (m *kvMeta) maxTxnBatchNum() int {
	if m.Name() == "etcd" {
		return 128
	}
	return 10240
}

func (m *kvMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	if opt == nil {
		opt = &LoadOption{}
	}
	if err := m.en.prepareLoad(ctx, opt); err != nil {
		return err
	}

	type task struct {
		typ int
		msg proto.Message
	}
	taskCh := make(chan *task, 100)

	var wg sync.WaitGroup
	workerFunc := func(ctx Context, taskCh <-chan *task) {
		defer wg.Done()
		var task *task
		maxNum := m.maxTxnBatchNum() * opt.Threads
		pairs := make([]*pair, 0, maxNum)
		for {
			select {
			case <-ctx.Done():
				return
			case task = <-taskCh:
			}
			if task == nil {
				if err := m.insertKVs(ctx, pairs, opt.Threads); err != nil {
					logger.Errorf("insert kvs failed: %v", err)
				}

				if val := ctx.Value("maxAclId"); val != nil {
					if err := m.txn(ctx, func(tx *kvTxn) error {
						tx.set(m.counterKey(aclCounter), packCounter(int64(val.(uint32))))
						return nil
					}); err != nil {
						logger.Errorf("update maxAclId failed: %v", err)
					}
				}
				break
			}
			switch task.typ {
			case segTypeFormat:
				m.loadFormat(ctx, task.msg, &pairs)
			case segTypeCounter:
				m.loadCounters(ctx, task.msg, &pairs)
			case segTypeNode:
				m.loadNodes(ctx, task.msg, &pairs)
			case segTypeEdge:
				m.loadEdges(ctx, task.msg, &pairs)
			case segTypeChunk:
				m.loadChunks(ctx, task.msg, &pairs)
			case segTypeSymlink:
				m.loadSymlinks(ctx, task.msg, &pairs)
			case segTypeXattr:
				m.loadXattrs(ctx, task.msg, &pairs)
			case segTypeParent:
				m.loadParents(ctx, task.msg, &pairs)
			case segTypeSustained:
				m.loadSustained(ctx, task.msg, &pairs)
			case segTypeDelFile:
				m.loadDelFiles(ctx, task.msg, &pairs)
			case segTypeSliceRef:
				m.loadSliceRefs(ctx, task.msg, &pairs)
			case segTypeAcl:
				m.loadAcl(ctx, task.msg, &pairs)
			case segTypeQuota:
				m.loadQuota(ctx, task.msg, &pairs)
			case segTypeStat:
				m.loadDirStats(ctx, task.msg, &pairs)
			}
			if len(pairs) >= maxNum {
				if err := m.insertKVs(ctx, pairs, opt.Threads); err != nil {
					logger.Errorf("insert kvs failed: %v", err)
					ctx.Cancel()
					return
				}
				pairs = make([]*pair, 0, maxNum)
			}
		}
	}

	wg.Add(1)
	go workerFunc(ctx, taskCh)

	bak := &BakFormat{}
	for {
		seg, err := bak.ReadSegment(r)
		if err != nil {
			if errors.Is(err, errBakEOF) {
				close(taskCh)
				break
			}
			ctx.Cancel()
			wg.Wait()
			return err
		}

		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case taskCh <- &task{int(seg.typ), seg.val}:
			if opt.Progress != nil {
				opt.Progress(seg.Name(), int(seg.num()))
			}
		}
	}
	wg.Wait()
	return nil
}
