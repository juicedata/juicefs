//go:build !nosqlite || !nomysql || !nopg
// +build !nosqlite !nomysql !nopg

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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"xorm.io/xorm"
)

var (
	sqlDumpBatchSize = 40960
)

func (m *dbMeta) buildDumpedSeg(typ int, opt *DumpOption, txn *eTxn) iDumpedSeg {
	ds := dumpedSeg{typ: typ, meta: m, opt: opt, txn: txn}
	switch typ {
	case SegTypeFormat:
		return &formatDS{ds}
	case SegTypeCounter:
		return &sqlCounterDS{ds}
	case SegTypeSustained:
		return &sqlSustainedDS{ds}
	case SegTypeDelFile:
		return &sqlDelFileDS{ds}
	case SegTypeSliceRef:
		return &sqlSliceRefDS{ds, []*sync.Pool{{New: func() interface{} { return &pb.SliceRef{} }}}}
	case SegTypeAcl:
		return &sqlAclDS{ds}
	case SegTypeXattr:
		return &sqlXattrDS{ds}
	case SegTypeQuota:
		return &sqlQuotaDS{ds}
	case SegTypeStat:
		return &sqlStatDS{ds}
	case SegTypeNode:
		return &sqlNodeDBS{dumpedBatchSeg{ds, []*sync.Pool{{New: func() interface{} { return &pb.Node{} }}}}}
	case SegTypeChunk:
		return &sqlChunkDBS{
			dumpedBatchSeg{
				ds,
				[]*sync.Pool{
					{New: func() interface{} { return &pb.Chunk{} }},
				},
			},
		}
	case SegTypeEdge:
		return &sqlEdgeDBS{dumpedBatchSeg{ds, []*sync.Pool{{New: func() interface{} { return &pb.Edge{} }}}}, sync.Mutex{}}
	case SegTypeParent:
		return &sqlParentDS{ds}
	case SegTypeSymlink:
		return &sqlSymlinkDBS{dumpedBatchSeg{ds, []*sync.Pool{{New: func() interface{} { return &pb.Symlink{} }}}}}
	}
	return nil
}

var sqlLoadedPoolOnce sync.Once
var sqlLoadedPools = make(map[int][]*sync.Pool)

func (m *dbMeta) buildLoadedPools(typ int) []*sync.Pool {
	sqlLoadedPoolOnce.Do(func() {
		sqlLoadedPools = map[int][]*sync.Pool{
			SegTypeNode:    {{New: func() interface{} { return &node{} }}},
			SegTypeChunk:   {{New: func() interface{} { return &chunk{} }}, {New: func() interface{} { return make([]byte, sliceBytes*10) }}},
			SegTypeEdge:    {{New: func() interface{} { return &edge{} }}},
			SegTypeSymlink: {{New: func() interface{} { return &symlink{} }}},
		}
	})
	return sqlLoadedPools[typ]
}

func (m *dbMeta) buildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	switch typ {
	case SegTypeFormat:
		return &sqlFormatLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeCounter:
		return &sqlCounterLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSustained:
		return &sqlSustainedLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeDelFile:
		return &sqlDelFileLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSliceRef:
		return &sqlSliceRefLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeAcl:
		return &sqlAclLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeXattr:
		return &sqlXattrLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeQuota:
		return &sqlQuotaLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeStat:
		return &sqlStatLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeNode:
		return &sqlNodeLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeChunk:
		return &sqlChunkLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeEdge:
		return &sqlEdgeLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeParent:
		return &sqlParentLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSymlink:
		return &sqlSymlinkLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	}
	return nil
}

func (m *dbMeta) execETxn(ctx Context, txn *eTxn, f func(Context, *eTxn) error) error {
	if txn.opt.coNum > 1 {
		// only use same txn when coNum == 1 for sql
		txn.opt.notUsed = true
		return f(ctx, txn)
	}
	ctx.WithValue(txMaxRetryKey{}, txn.opt.maxRetry)
	return m.roTxn(ctx, func(sess *xorm.Session) error {
		txn.obj = sess
		return f(ctx, txn)
	})
}

func (m *dbMeta) execStmt(ctx context.Context, txn *eTxn, f func(*xorm.Session) error) error {
	if txn.opt.notUsed {
		return m.roTxn(ctx, func(s *xorm.Session) error {
			return f(s)
		})
	}

	var err error
	cnt := 0
	for cnt < txn.opt.maxStmtRetry {
		err = f(txn.obj.(*xorm.Session))
		if err == nil || !m.shouldRetry(err) {
			break
		}
		cnt++
		time.Sleep(time.Duration(cnt) * time.Microsecond)
	}
	return err
}

func getSQLCounterFields(c *pb.Counters) map[string]*int64 {
	return map[string]*int64{
		usedSpace:     &c.UsedSpace,
		totalInodes:   &c.UsedInodes,
		"nextInode":   &c.NextInode,
		"nextChunk":   &c.NextChunk,
		"nextSession": &c.NextSession,
		"nextTrash":   &c.NextTrash,
	}
}

type sqlCounterDS struct {
	dumpedSeg
}

func (s *sqlCounterDS) dump(ctx Context, ch chan *dumpedResult) error {
	meta := s.meta.(*dbMeta)
	var rows []counter
	if err := meta.execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	counters := &pb.Counters{}
	fieldMap := getSQLCounterFields(counters)
	for _, row := range rows {
		if fieldPtr, ok := fieldMap[row.Name]; ok {
			*fieldPtr = row.Value
		}
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, counters}); err != nil {
		return err
	}
	logger.Debugf("dump %s result %+v", s, counters)
	return nil
}

type sqlSustainedDS struct {
	dumpedSeg
}

func (s *sqlSustainedDS) dump(ctx Context, ch chan *dumpedResult) error {
	var rows []sustained
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	ss := make(map[uint64][]uint64)
	for _, row := range rows {
		ss[row.Sid] = append(ss[row.Sid], uint64(row.Inode))
	}

	pss := &pb.SustainedList{
		List: make([]*pb.Sustained, 0, len(ss)),
	}
	for k, v := range ss {
		pss.List = append(pss.List, &pb.Sustained{Sid: k, Inodes: v})
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, pss}); err != nil {
		return err
	}
	logger.Debugf("dump %s num %d", s, len(ss))
	return nil
}

type sqlDelFileDS struct {
	dumpedSeg
}

func (s *sqlDelFileDS) dump(ctx Context, ch chan *dumpedResult) error {
	var rows []delfile
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	delFiles := &pb.DelFileList{List: make([]*pb.DelFile, 0, len(rows))}
	for _, row := range rows {
		delFiles.List = append(delFiles.List, &pb.DelFile{Inode: uint64(row.Inode), Length: row.Length, Expire: row.Expire})
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, delFiles}); err != nil {
		return err
	}
	logger.Debugf("dump %s num %d", s, len(delFiles.List))
	return nil
}

type sqlSliceRefDS struct {
	dumpedSeg
	pools []*sync.Pool
}

func (s *sqlSliceRefDS) dump(ctx Context, ch chan *dumpedResult) error {
	eg, _ := errgroup.WithContext(ctx)
	eg.SetLimit(s.opt.CoNum)

	taskFinished := false
	psrs := &pb.SliceRefList{List: make([]*pb.SliceRef, 0, 1024)}
	for start := 0; !taskFinished; start += sqlDumpBatchSize {
		nStart := start
		eg.Go(func() error {
			var rows []sliceRef
			if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
				rows = rows[:0]
				return s.Where("refs != 1").Limit(sqlDumpBatchSize, nStart).Find(&rows) // skip default refs
			}); err != nil || len(rows) == 0 {
				taskFinished = true
				return err
			}
			var psr *pb.SliceRef
			for _, sr := range rows {
				psr = s.pools[0].Get().(*pb.SliceRef)
				psr.Id = sr.Id
				psr.Size = sr.Size
				psr.Refs = int64(sr.Refs)
				psrs.List = append(psrs.List, psr)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		logger.Errorf("query %s err: %v", s, err)
		return err
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, psrs}); err != nil {
		return err
	}
	logger.Debugf("dump %s num %d", s, len(psrs.List))
	return nil
}

func (s *sqlSliceRefDS) release(msg proto.Message) {
	psrs := msg.(*pb.SliceRefList)
	for _, psr := range psrs.List {
		s.pools[0].Put(psr)
	}
	psrs.List = nil
}

type sqlAclDS struct {
	dumpedSeg
}

func (s *sqlAclDS) dump(ctx Context, ch chan *dumpedResult) error {
	var rows []acl
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	acls := &pb.AclList{List: make([]*pb.Acl, 0, len(rows))}
	for _, row := range rows {
		acls.List = append(acls.List, &pb.Acl{
			Id:   row.Id,
			Data: row.toRule().Encode(),
		})
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, acls}); err != nil {
		return err
	}
	logger.Debugf("dump %s num %d", s, len(acls.List))
	return nil
}

type sqlXattrDS struct {
	dumpedSeg
}

func (s *sqlXattrDS) dump(ctx Context, ch chan *dumpedResult) error {
	var rows []xattr
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}

	if len(rows) == 0 {
		return nil
	}

	pxs := &pb.XattrList{
		List: make([]*pb.Xattr, 0, len(rows)),
	}
	for _, x := range rows {
		pxs.List = append(pxs.List, &pb.Xattr{
			Inode: uint64(x.Inode),
			Name:  x.Name,
			Value: x.Value,
		})
	}

	logger.Debugf("dump %s num %d", s, len(pxs.List))
	return dumpResult(ctx, ch, &dumpedResult{s, pxs})
}

type sqlQuotaDS struct {
	dumpedSeg
}

func (s *sqlQuotaDS) dump(ctx Context, ch chan *dumpedResult) error {
	var rows []dirQuota
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pqs := &pb.QuotaList{
		List: make([]*pb.Quota, 0, len(rows)),
	}
	for _, q := range rows {
		pqs.List = append(pqs.List, &pb.Quota{
			Inode:      uint64(q.Inode),
			MaxSpace:   q.MaxSpace,
			MaxInodes:  q.MaxInodes,
			UsedSpace:  q.UsedSpace,
			UsedInodes: q.UsedInodes,
		})
	}
	logger.Debugf("dump %s num %d", s, len(pqs.List))
	return dumpResult(ctx, ch, &dumpedResult{s, pqs})
}

type sqlStatDS struct {
	dumpedSeg
}

func (s *sqlStatDS) dump(ctx Context, ch chan *dumpedResult) error {
	var rows []dirStats
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pss := &pb.StatList{
		List: make([]*pb.Stat, 0, len(rows)),
	}
	for _, st := range rows {
		pss.List = append(pss.List, &pb.Stat{
			Inode:      uint64(st.Inode),
			DataLength: st.DataLength,
			UsedInodes: st.UsedInodes,
			UsedSpace:  st.UsedSpace,
		})
	}
	logger.Debugf("dump %s num %d", s, len(pss.List))
	return dumpResult(ctx, ch, &dumpedResult{s, pss})
}

func sqlQueryBatch(ctx Context, s iDumpedSeg, opt *DumpOption, ch chan *dumpedResult, query func(ctx context.Context, limit, start int, sum *int64) (proto.Message, error)) error {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)

	taskFinished := false
	sum := int64(0)
	for start := 0; !taskFinished; start += sqlDumpBatchSize {
		nStart := start
		eg.Go(func() error {
			msg, err := query(egCtx, sqlDumpBatchSize, nStart, &sum)
			if err != nil || msg == nil {
				taskFinished = true
				return err
			}
			return dumpResult(egCtx, ch, &dumpedResult{s, msg})
		})
	}
	if err := eg.Wait(); err != nil {
		logger.Errorf("query %s err: %v", s, err)
		return err
	}
	logger.Debugf("dump %s num %d", s, sum)
	return nil
}

type sqlNodeDBS struct {
	dumpedBatchSeg
}

func (s *sqlNodeDBS) dump(ctx Context, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, s.opt, ch, s.doQuery)
}

func (s *sqlNodeDBS) doQuery(ctx context.Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []node
	m := s.meta.(*dbMeta)
	if err := m.execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Limit(limit, start).Find(&rows)
	}); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	pns := &pb.NodeList{
		List: make([]*pb.Node, 0, len(rows)),
	}
	var pn *pb.Node
	attr := &Attr{}
	for _, n := range rows {
		pn = s.pools[0].Get().(*pb.Node)
		pn.Inode = uint64(n.Inode)
		m.parseAttr(&n, attr)
		pn.Data = m.marshal(attr)
		pns.List = append(pns.List, pn)
	}
	atomic.AddInt64(sum, int64(len(pns.List)))
	return pns, nil
}

func (s *sqlNodeDBS) release(msg proto.Message) {
	pns := msg.(*pb.NodeList)
	for _, node := range pns.List {
		s.pools[0].Put(node)
	}
	pns.List = nil
}

type sqlChunkDBS struct {
	dumpedBatchSeg
}

func (s *sqlChunkDBS) dump(ctx Context, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, s.opt, ch, s.doQuery)
}

func (s *sqlChunkDBS) doQuery(ctx context.Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []chunk
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Limit(limit, start).Find(&rows)
	}); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	pcs := &pb.ChunkList{
		List: make([]*pb.Chunk, 0, len(rows)),
	}
	var pc *pb.Chunk
	for _, c := range rows {
		pc = s.pools[0].Get().(*pb.Chunk)
		pc.Inode = uint64(c.Inode)
		pc.Index = c.Indx
		pc.Slices = c.Slices
		pcs.List = append(pcs.List, pc)
	}
	atomic.AddInt64(sum, int64(len(pcs.List)))
	return pcs, nil
}

func (s *sqlChunkDBS) release(msg proto.Message) {
	pcs := msg.(*pb.ChunkList)
	for _, pc := range pcs.List {
		s.pools[0].Put(pc)
	}
	pcs.List = nil
}

type sqlEdgeDBS struct {
	dumpedBatchSeg
	lock sync.Mutex
}

func (s *sqlEdgeDBS) dump(ctx Context, ch chan *dumpedResult) error {
	ctx.WithValue("parents", make(map[uint64][]uint64))
	return sqlQueryBatch(ctx, s, s.opt, ch, s.doQuery)
}

func (s *sqlEdgeDBS) doQuery(ctx context.Context, limit, start int, sum *int64) (proto.Message, error) {
	// TODO: optimize parents
	s.lock.Lock()
	parents := ctx.Value("parents").(map[uint64][]uint64)
	s.lock.Unlock()

	var rows []edge
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Limit(limit, start).Find(&rows)
	}); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	pes := &pb.EdgeList{
		List: make([]*pb.Edge, 0, len(rows)),
	}
	var pe *pb.Edge
	for _, e := range rows {
		pe = s.pools[0].Get().(*pb.Edge)
		pe.Parent = uint64(e.Parent)
		pe.Inode = uint64(e.Inode)
		pe.Name = e.Name
		pe.Type = uint32(e.Type)

		s.lock.Lock()
		parents[uint64(e.Inode)] = append(parents[uint64(e.Inode)], uint64(e.Parent))
		s.lock.Unlock()
		pes.List = append(pes.List, pe)
	}
	atomic.AddInt64(sum, int64(len(pes.List)))
	return pes, nil
}

func (s *sqlEdgeDBS) release(msg proto.Message) {
	pes := msg.(*pb.EdgeList)
	for _, pe := range pes.List {
		s.pools[0].Put(pe)
	}
	pes.List = nil
}

type sqlParentDS struct {
	dumpedSeg
}

func (s *sqlParentDS) dump(ctx Context, ch chan *dumpedResult) error {
	val := ctx.Value("parents")
	if val == nil {
		return nil
	}

	parents := val.(map[uint64][]uint64)
	pls := &pb.ParentList{
		List: make([]*pb.Parent, 0, sqlDumpBatchSize),
	}
	st := make(map[uint64]int64)
	for inode, ps := range parents {
		if len(ps) > 1 {
			for k := range st {
				delete(st, k)
			}
			for _, p := range ps {
				st[p] = st[p] + 1
			}
			for parent, cnt := range st {
				pls.List = append(pls.List, &pb.Parent{Inode: inode, Parent: parent, Cnt: cnt})
			}
		}
		if len(pls.List) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{s, pls}); err != nil {
				return err
			}
			pls = &pb.ParentList{
				List: make([]*pb.Parent, 0, sqlDumpBatchSize),
			}
		}
	}

	if len(pls.List) > 0 {
		if err := dumpResult(ctx, ch, &dumpedResult{s, pls}); err != nil {
			return err
		}
	}
	return nil
}

type sqlSymlinkDBS struct {
	dumpedBatchSeg
}

func (s *sqlSymlinkDBS) dump(ctx Context, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, s.opt, ch, s.doQuery)
}

func (s *sqlSymlinkDBS) doQuery(ctx context.Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []symlink
	if err := s.meta.(*dbMeta).execStmt(ctx, s.txn, func(s *xorm.Session) error {
		rows = rows[:0]
		return s.Limit(limit, start).Find(&rows)
	}); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	pss := &pb.SymlinkList{
		List: make([]*pb.Symlink, 0, len(rows)),
	}
	var ps *pb.Symlink
	for _, sl := range rows {
		ps = s.pools[0].Get().(*pb.Symlink)
		ps.Inode = uint64(sl.Inode)
		ps.Target = sl.Target
		pss.List = append(pss.List, ps)
	}
	atomic.AddInt64(sum, int64(len(pss.List)))
	return pss, nil
}

func (s *sqlSymlinkDBS) release(msg proto.Message) {
	pss := msg.(*pb.SymlinkList)
	for _, ps := range pss.List {
		s.pools[0].Put(ps)
	}
	pss.List = nil
}

type sqlFormatLS struct {
	loadedSeg
}

func (s *sqlFormatLS) load(ctx Context, msg proto.Message) error {
	return s.meta.(*dbMeta).insertRows([]interface{}{
		&setting{
			Name:  "format",
			Value: string(msg.(*pb.Format).Data),
		},
	})
}

type sqlCounterLS struct {
	loadedSeg
}

func (s *sqlCounterLS) load(ctx Context, msg proto.Message) error {
	counters := msg.(*pb.Counters)
	fields := getSQLCounterFields(counters)

	var rows []interface{}
	for name, field := range fields {
		rows = append(rows, counter{Name: name, Value: *field})
	}
	logger.Debugf("insert counters %+v", rows)
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlSustainedLS struct {
	loadedSeg
}

func (s *sqlSustainedLS) load(ctx Context, msg proto.Message) error {
	sustaineds := msg.(*pb.SustainedList)
	rows := make([]interface{}, 0, len(sustaineds.List))
	for _, s := range sustaineds.List {
		for _, inode := range s.Inodes {
			rows = append(rows, sustained{Sid: s.Sid, Inode: Ino(inode)})
		}
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlDelFileLS struct {
	loadedSeg
}

func (s *sqlDelFileLS) load(ctx Context, msg proto.Message) error {
	delfiles := msg.(*pb.DelFileList)
	rows := make([]interface{}, 0, len(delfiles.List))
	for _, f := range delfiles.List {
		rows = append(rows, &delfile{Inode: Ino(f.Inode), Length: f.Length, Expire: f.Expire})
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlSliceRefLS struct {
	loadedSeg
}

func (s *sqlSliceRefLS) load(ctx Context, msg proto.Message) error {
	srs := msg.(*pb.SliceRefList)
	rows := make([]interface{}, 0, len(srs.List))
	for _, sr := range srs.List {
		rows = append(rows, &sliceRef{Id: sr.Id, Size: sr.Size, Refs: int(sr.Refs)})
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlAclLS struct {
	loadedSeg
}

func (s *sqlAclLS) load(ctx Context, msg proto.Message) error {
	acls := msg.(*pb.AclList)
	rows := make([]interface{}, 0, len(acls.List))
	for _, pa := range acls.List {
		rule := &aclAPI.Rule{}
		rule.Decode(pa.Data)
		acl := newSQLAcl(rule)
		acl.Id = pa.Id
		rows = append(rows, acl)
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlXattrLS struct {
	loadedSeg
}

func (s *sqlXattrLS) load(ctx Context, msg proto.Message) error {
	xattrs := msg.(*pb.XattrList)
	rows := make([]interface{}, 0, len(xattrs.List))
	for _, x := range xattrs.List {
		rows = append(rows, &xattr{Inode: Ino(x.Inode), Name: x.Name, Value: x.Value})
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlQuotaLS struct {
	loadedSeg
}

func (s *sqlQuotaLS) load(ctx Context, msg proto.Message) error {
	quotas := msg.(*pb.QuotaList)
	rows := make([]interface{}, 0, len(quotas.List))
	for _, q := range quotas.List {
		rows = append(rows, &dirQuota{
			Inode:      Ino(q.Inode),
			MaxSpace:   q.MaxSpace,
			MaxInodes:  q.MaxInodes,
			UsedSpace:  q.UsedSpace,
			UsedInodes: q.UsedInodes,
		})
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlStatLS struct {
	loadedSeg
}

func (s *sqlStatLS) load(ctx Context, msg proto.Message) error {
	stats := msg.(*pb.StatList)
	rows := make([]interface{}, 0, len(stats.List))
	for _, st := range stats.List {
		rows = append(rows, &dirStats{
			Inode:      Ino(st.Inode),
			DataLength: st.DataLength,
			UsedInodes: st.UsedInodes,
			UsedSpace:  st.UsedSpace,
		})
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return s.meta.(*dbMeta).insertRows(rows)
}

type sqlNodeLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *sqlNodeLS) load(ctx Context, msg proto.Message) error {
	nodes := msg.(*pb.NodeList)
	m := s.meta.(*dbMeta)
	b := m.getBase()
	rows := make([]interface{}, 0, len(nodes.List))
	var pn *node
	attr := &Attr{}
	for _, n := range nodes.List {
		pn = s.pools[0].Get().(*node)
		pn.Inode = Ino(n.Inode)
		attr.Parent, attr.AccessACL, attr.DefaultACL = 0, 0, 0
		b.parseAttr(n.Data, attr)
		m.parseNode(attr, pn)
		rows = append(rows, pn)
	}
	err := s.meta.(*dbMeta).insertRows(rows)
	for _, n := range rows {
		s.pools[0].Put(n)
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return err
}

type sqlChunkLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *sqlChunkLS) load(ctx Context, msg proto.Message) error {
	chunks := msg.(*pb.ChunkList)
	rows := make([]interface{}, 0, len(chunks.List))
	var pc *chunk
	for _, c := range chunks.List {
		pc = s.pools[0].Get().(*chunk)
		pc.Id = 0
		pc.Inode = Ino(c.Inode)
		pc.Indx = c.Index
		pc.Slices = c.Slices
		rows = append(rows, pc)
	}
	err := s.meta.(*dbMeta).insertRows(rows)

	for _, chk := range rows {
		s.pools[0].Put(chk)
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return err
}

type sqlEdgeLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *sqlEdgeLS) load(ctx Context, msg proto.Message) error {
	edges := msg.(*pb.EdgeList)
	rows := make([]interface{}, 0, len(edges.List))
	var pe *edge
	for _, e := range edges.List {
		pe = s.pools[0].Get().(*edge)
		pe.Id = 0
		pe.Parent = Ino(e.Parent)
		pe.Inode = Ino(e.Inode)
		pe.Name = e.Name
		pe.Type = uint8(e.Type)
		rows = append(rows, pe)
	}

	err := s.meta.(*dbMeta).insertRows(rows)
	for _, e := range rows {
		s.pools[0].Put(e)
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return err
}

type sqlParentLS struct {
	loadedSeg
}

func (s *sqlParentLS) load(ctx Context, msg proto.Message) error {
	return nil // No need for SQL, skip.
}

type sqlSymlinkLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *sqlSymlinkLS) load(ctx Context, msg proto.Message) error {
	symlinks := msg.(*pb.SymlinkList)
	rows := make([]interface{}, 0, len(symlinks.List))
	var ps *symlink
	for _, sl := range symlinks.List {
		ps = s.pools[0].Get().(*symlink)
		ps.Inode = Ino(sl.Inode)
		ps.Target = sl.Target
		rows = append(rows, ps)
	}

	err := s.meta.(*dbMeta).insertRows(rows)
	for _, sl := range rows {
		s.pools[0].Put(sl)
	}
	logger.Debugf("insert %s num %d", s, len(rows))
	return err
}

func (m *dbMeta) insertRows(beans []interface{}) error {
	insert := func(rows []interface{}) error {
		return m.txn(func(s *xorm.Session) error {
			n, err := s.Insert(rows...)
			if err == nil && int(n) != len(rows) {
				err = fmt.Errorf("only %d records inserted", n)
			}
			return err
		})
	}

	batch := m.getTxnBatchNum()
	for len(beans) > 0 {
		bs := utils.Min(batch, len(beans))
		if err := insert(beans[:bs]); err != nil {
			logger.Errorf("Write %d beans: %s", bs, err)
			return err
		}
		beans = beans[bs:]
	}
	return nil
}

func (m *dbMeta) prepareLoad(ctx Context, opt *LoadOption) error {
	opt.check()
	if err := m.checkAddr(); err != nil {
		return err
	}
	if err := m.syncAllTables(); err != nil {
		return err
	}
	return nil
}
