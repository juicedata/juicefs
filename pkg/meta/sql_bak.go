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
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"xorm.io/xorm"
)

var (
	sqlDumpBatchSize = 100000
)

func (m *dbMeta) dump(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var dumps = []func(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error{
		m.dumpFormat,
		m.dumpCounters,
		m.dumpNodes,
		m.dumpChunks,
		m.dumpEdges,
		m.dumpSymlinks,
		m.dumpSustained,
		m.dumpDelFiles,
		m.dumpSliceRef,
		m.dumpACL,
		m.dumpXattr,
		m.dumpQuota,
		m.dumpDirStat,
	}

	ctx.WithValue(txMaxRetryKey{}, 3)
	if opt.Threads == 1 {
		// use same txn for all dumps
		sess := m.db.NewSession()
		defer sess.Close()

		opt := sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		}
		err := sess.BeginTx(&opt)
		if err != nil && (strings.Contains(err.Error(), "READ") || strings.Contains(err.Error(), "driver does not support read-only transactions")) {
			logger.Warnf("the database does not support read-only transaction")
			opt = sql.TxOptions{} // use default level
			if err = sess.BeginTx(&opt); err != nil {
				return err
			}
		}
		defer sess.Rollback() //nolint:errcheck
		ctx.WithValue(txSessionKey{}, sess)
	} else {
		logger.Warnf("dump database with %d threads, please make sure that it's readonly, "+
			"otherwise the dumped metadata will be inconsistent", opt.Threads)
	}
	for _, f := range dumps {
		err := f(ctx, opt, ch)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *dbMeta) execTxn(ctx context.Context, f func(s *xorm.Session) error) error {
	if val := ctx.Value(txSessionKey{}); val != nil {
		return f(val.(*xorm.Session))
	}
	return m.roTxn(ctx, f)
}

func sqlQueryBatch(ctx Context, opt *DumpOption, maxId uint64, query func(ctx context.Context, start, end uint64) (int, error)) error {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.Threads)

	sum := int64(0)
	batch := uint64(sqlDumpBatchSize)
	for id := uint64(0); id <= maxId; id += batch {
		startId := id
		eg.Go(func() error {
			n, err := query(egCtx, startId, startId+batch)
			atomic.AddInt64(&sum, int64(n))
			return err
		})
	}
	logger.Debugf("dump %d rows", sum)
	return eg.Wait()
}

func (m *dbMeta) dumpNodes(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	pool := sync.Pool{New: func() interface{} { return &pb.Node{} }}
	release := func(p proto.Message) {
		for _, s := range p.(*pb.Batch).Nodes {
			pool.Put(s)
		}
	}

	var rows []node
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Where("inode >= ?", TrashInode).Find(&rows)
	}); err != nil {
		return err
	}
	nodes := make([]*pb.Node, 0, len(rows))
	var attr Attr
	for _, n := range rows {
		pn := pool.Get().(*pb.Node)
		pn.Inode = uint64(n.Inode)
		m.parseAttr(&n, &attr)
		pn.Data = m.marshal(&attr)
		nodes = append(nodes, pn)
	}
	if err := dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Nodes: nodes}, release}); err != nil {
		return errors.Wrap(err, "dump trash nodes")
	}

	var maxInode uint64
	err := m.execTxn(ctx, func(s *xorm.Session) error {
		var row node
		ok, err := s.Select("max(inode) as inode").Where("inode < ?", TrashInode).Get(&row)
		if ok {
			maxInode = uint64(row.Inode)
		}
		return err
	})
	if err != nil {
		return errors.Wrap(err, "max inode")
	}

	return sqlQueryBatch(ctx, opt, maxInode, func(ctx context.Context, start, end uint64) (int, error) {
		var rows []node
		if err := m.execTxn(ctx, func(s *xorm.Session) error {
			return s.Where("inode >= ? AND inode < ?", start, end).Find(&rows)
		}); err != nil {
			return 0, err
		}
		nodes := make([]*pb.Node, 0, len(rows))
		var attr Attr
		for _, n := range rows {
			pn := pool.Get().(*pb.Node)
			pn.Inode = uint64(n.Inode)
			m.parseAttr(&n, &attr)
			pn.Data = m.marshal(&attr)
			nodes = append(nodes, pn)
		}
		return len(rows), dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Nodes: nodes}, release})
	})
}

func (m *dbMeta) dumpChunks(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	pool := sync.Pool{New: func() interface{} { return &pb.Chunk{} }}
	release := func(p proto.Message) {
		for _, s := range p.(*pb.Batch).Chunks {
			pool.Put(s)
		}
	}

	var maxId uint64
	err := m.execTxn(ctx, func(s *xorm.Session) error {
		var row chunk
		ok, err := s.Select("MAX(id) as id").Get(&row)
		if ok {
			maxId = uint64(row.Id)
		}
		return err
	})
	if err != nil {
		return err
	}

	return sqlQueryBatch(ctx, opt, maxId, func(ctx context.Context, start, end uint64) (int, error) {
		var rows []chunk
		if err := m.execTxn(ctx, func(s *xorm.Session) error {
			return s.Where("id >= ? AND id < ?", start, end).Find(&rows)
		}); err != nil {
			return 0, err
		}
		chunks := make([]*pb.Chunk, 0, len(rows))
		for _, c := range rows {
			pc := pool.Get().(*pb.Chunk)
			pc.Inode = uint64(c.Inode)
			pc.Index = c.Indx
			pc.Slices = c.Slices
			chunks = append(chunks, pc)
		}
		return len(rows), dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Chunks: chunks}, release})
	})
}

func (m *dbMeta) dumpEdges(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	pool := sync.Pool{New: func() interface{} { return &pb.Edge{} }}
	release := func(p proto.Message) {
		for _, s := range p.(*pb.Batch).Edges {
			pool.Put(s)
		}
	}

	var maxId uint64
	err := m.execTxn(ctx, func(s *xorm.Session) error {
		var row edge
		ok, err := s.Select("MAX(id) as id").Get(&row)
		if ok {
			maxId = uint64(row.Id)
		}
		return err
	})
	if err != nil {
		return err
	}

	var mu sync.Mutex
	dumpParents := make(map[uint64][]uint64)
	err = sqlQueryBatch(ctx, opt, maxId, func(ctx context.Context, start, end uint64) (int, error) {
		var rows []edge
		if err := m.execTxn(ctx, func(s *xorm.Session) error {
			return s.Where("id >= ? AND id < ?", start, end).Find(&rows)
		}); err != nil {
			return 0, err
		}
		edges := make([]*pb.Edge, 0, len(rows))
		for _, e := range rows {
			pe := pool.Get().(*pb.Edge)
			pe.Parent = uint64(e.Parent)
			pe.Inode = uint64(e.Inode)
			pe.Name = e.Name
			pe.Type = uint32(e.Type)
			edges = append(edges, pe)
			mu.Lock()
			dumpParents[uint64(e.Inode)] = append(dumpParents[uint64(e.Inode)], uint64(e.Parent))
			mu.Unlock()
		}
		return len(rows), dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Edges: edges}, release})
	})
	if err != nil {
		return err
	}

	parents := make([]*pb.Parent, 0, sqlDumpBatchSize)
	st := make(map[uint64]int64)
	for inode, ps := range dumpParents {
		if len(ps) > 1 {
			for k := range st {
				delete(st, k)
			}
			for _, p := range ps {
				st[p] = st[p] + 1
			}
			for parent, cnt := range st {
				parents = append(parents, &pb.Parent{Inode: inode, Parent: parent, Cnt: cnt})
			}
		}
		if len(parents) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Parents: parents}}); err != nil {
				return err
			}
			parents = make([]*pb.Parent, 0, sqlDumpBatchSize)
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Parents: parents}})
}

func (m *dbMeta) dumpSymlinks(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []symlink
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}

	symlinks := make([]*pb.Symlink, 0, min(len(rows), sqlDumpBatchSize))
	for i, r := range rows {
		symlinks = append(symlinks, &pb.Symlink{Inode: uint64(r.Inode), Target: r.Target})
		if len(symlinks) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Symlinks: symlinks}}); err != nil {
				return err
			}
			symlinks = make([]*pb.Symlink, 0, min(len(rows)-i-1, sqlDumpBatchSize))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Symlinks: symlinks}})
}

func (m *dbMeta) dumpCounters(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []counter
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	var counters = make([]*pb.Counter, 0, len(rows))
	for _, row := range rows {
		counters = append(counters, &pb.Counter{Key: row.Name, Value: row.Value})
	}
	logger.Debugf("dump counters %+v", counters)
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Counters: counters}})
}

func (m *dbMeta) dumpSustained(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []sustained
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	ss := make(map[uint64][]uint64)
	for _, row := range rows {
		ss[row.Sid] = append(ss[row.Sid], uint64(row.Inode))
	}
	sustained := make([]*pb.Sustained, 0, len(rows))
	for k, v := range ss {
		sustained = append(sustained, &pb.Sustained{Sid: k, Inodes: v})
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Sustained: sustained}})
}

func (m *dbMeta) dumpDelFiles(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []delfile
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	delFiles := make([]*pb.DelFile, 0, min(sqlDumpBatchSize, len(rows)))
	for i, row := range rows {
		delFiles = append(delFiles, &pb.DelFile{Inode: uint64(row.Inode), Length: row.Length, Expire: row.Expire})
		if len(delFiles) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Delfiles: delFiles}}); err != nil {
				return err
			}
			delFiles = make([]*pb.DelFile, 0, min(sqlDumpBatchSize, len(rows)-i-1))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Delfiles: delFiles}})
}

func (m *dbMeta) dumpSliceRef(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []sliceRef
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Where("refs != 1").Find(&rows) // skip default refs
	}); err != nil {
		return err
	}
	sliceRefs := make([]*pb.SliceRef, 0, min(sqlDumpBatchSize, len(rows)))
	for i, sr := range rows {
		sliceRefs = append(sliceRefs, &pb.SliceRef{Id: sr.Id, Size: sr.Size, Refs: int64(sr.Refs)})
		if len(sliceRefs) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{SliceRefs: sliceRefs}}); err != nil {
				return err
			}
			sliceRefs = make([]*pb.SliceRef, 0, min(sqlDumpBatchSize, len(rows)-i-1))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{SliceRefs: sliceRefs}})
}

func (m *dbMeta) dumpACL(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []acl
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	acls := make([]*pb.Acl, 0, len(rows))
	for _, row := range rows {
		acls = append(acls, &pb.Acl{
			Id:   row.Id,
			Data: row.toRule().Encode(),
		})
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Acls: acls}})
}

func (m *dbMeta) dumpXattr(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []xattr
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	xattrs := make([]*pb.Xattr, 0, min(sqlDumpBatchSize, len(rows)))
	for i, x := range rows {
		xattrs = append(xattrs, &pb.Xattr{
			Inode: uint64(x.Inode),
			Name:  x.Name,
			Value: x.Value,
		})
		if len(xattrs) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Xattrs: xattrs}}); err != nil {
				return err
			}
			xattrs = make([]*pb.Xattr, 0, min(sqlDumpBatchSize, len(rows)-i-1))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Xattrs: xattrs}})
}

func (m *dbMeta) dumpQuota(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []dirQuota
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	quotas := make([]*pb.Quota, 0, len(rows))
	for _, q := range rows {
		quotas = append(quotas, &pb.Quota{
			Inode:      uint64(q.Inode),
			MaxSpace:   q.MaxSpace,
			MaxInodes:  q.MaxInodes,
			UsedSpace:  q.UsedSpace,
			UsedInodes: q.UsedInodes,
		})
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Quotas: quotas}})
}

func (m *dbMeta) dumpDirStat(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	var rows []dirStats
	if err := m.execTxn(ctx, func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	dirStats := make([]*pb.Stat, 0, min(sqlDumpBatchSize, len(rows)))
	for i, st := range rows {
		dirStats = append(dirStats, &pb.Stat{
			Inode:      uint64(st.Inode),
			DataLength: st.DataLength,
			UsedInodes: st.UsedInodes,
			UsedSpace:  st.UsedSpace,
		})
		if len(dirStats) >= sqlDumpBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Dirstats: dirStats}}); err != nil {
				return err
			}
			dirStats = make([]*pb.Stat, 0, min(sqlDumpBatchSize, len(rows)-i-1))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Dirstats: dirStats}})
}

func (m *dbMeta) load(ctx Context, typ int, opt *LoadOption, val proto.Message) error {
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
		return nil // skip
	default:
		logger.Warnf("skip segment type %d", typ)
		return nil
	}
}

func (m *dbMeta) loadFormat(ctx Context, msg proto.Message) error {
	return m.insertRows([]interface{}{
		&setting{
			Name:  "format",
			Value: string(msg.(*pb.Format).Data),
		},
	})
}

func (m *dbMeta) loadCounters(ctx Context, msg proto.Message) error {
	var rows []interface{}
	for _, c := range msg.(*pb.Batch).Counters {
		rows = append(rows, counter{Name: c.Key, Value: c.Value})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadNodes(ctx Context, msg proto.Message) error {
	nodes := msg.(*pb.Batch).Nodes
	b := m.getBase()
	rows := make([]interface{}, 0, len(nodes))
	ns := make([]node, len(nodes))
	attr := &Attr{}
	for i, n := range nodes {
		pn := &ns[i]
		pn.Inode = Ino(n.Inode)
		attr.reset()
		b.parseAttr(n.Data, attr)
		m.parseNode(attr, pn)
		rows = append(rows, pn)
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadChunks(ctx Context, msg proto.Message) error {
	chunks := msg.(*pb.Batch).Chunks
	rows := make([]interface{}, 0, len(chunks))
	cs := make([]chunk, len(chunks))
	for i, c := range chunks {
		pc := &cs[i]
		pc.Inode = Ino(c.Inode)
		pc.Indx = c.Index
		pc.Slices = c.Slices
		rows = append(rows, pc)
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadEdges(ctx Context, msg proto.Message) error {
	edges := msg.(*pb.Batch).Edges
	rows := make([]interface{}, 0, len(edges))
	es := make([]edge, len(edges))
	for i, e := range edges {
		pe := &es[i]
		pe.Parent = Ino(e.Parent)
		pe.Inode = Ino(e.Inode)
		pe.Name = e.Name
		pe.Type = uint8(e.Type)
		rows = append(rows, pe)
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadSymlinks(ctx Context, msg proto.Message) error {
	symlinks := msg.(*pb.Batch).Symlinks
	rows := make([]interface{}, 0, len(symlinks))
	for _, sl := range symlinks {
		rows = append(rows, &symlink{Ino(sl.Inode), sl.Target})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadSustained(ctx Context, msg proto.Message) error {
	sustaineds := msg.(*pb.Batch).Sustained
	rows := make([]interface{}, 0, len(sustaineds))
	for _, s := range sustaineds {
		for _, inode := range s.Inodes {
			rows = append(rows, sustained{Sid: s.Sid, Inode: Ino(inode)})
		}
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadDelFiles(ctx Context, msg proto.Message) error {
	delfiles := msg.(*pb.Batch).Delfiles
	rows := make([]interface{}, 0, len(delfiles))
	for _, f := range delfiles {
		rows = append(rows, &delfile{Inode: Ino(f.Inode), Length: f.Length, Expire: f.Expire})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadSliceRefs(ctx Context, msg proto.Message) error {
	srs := msg.(*pb.Batch).SliceRefs
	rows := make([]interface{}, 0, len(srs))
	for _, sr := range srs {
		rows = append(rows, &sliceRef{Id: sr.Id, Size: sr.Size, Refs: int(sr.Refs)})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadAcl(ctx Context, msg proto.Message) error {
	acls := msg.(*pb.Batch).Acls
	rows := make([]interface{}, 0, len(acls))
	for _, pa := range acls {
		rule := &aclAPI.Rule{}
		rule.Decode(pa.Data)
		acl := newSQLAcl(rule)
		acl.Id = pa.Id
		rows = append(rows, acl)
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadXattrs(ctx Context, msg proto.Message) error {
	xattrs := msg.(*pb.Batch).Xattrs
	rows := make([]interface{}, 0, len(xattrs))
	for _, x := range xattrs {
		rows = append(rows, &xattr{Inode: Ino(x.Inode), Name: x.Name, Value: x.Value})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadQuota(ctx Context, msg proto.Message) error {
	quotas := msg.(*pb.Batch).Quotas
	rows := make([]interface{}, 0, len(quotas))
	for _, q := range quotas {
		rows = append(rows, &dirQuota{
			Inode:      Ino(q.Inode),
			MaxSpace:   q.MaxSpace,
			MaxInodes:  q.MaxInodes,
			UsedSpace:  q.UsedSpace,
			UsedInodes: q.UsedInodes,
		})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) loadDirStats(ctx Context, msg proto.Message) error {
	stats := msg.(*pb.Batch).Dirstats
	rows := make([]interface{}, 0, len(stats))
	for _, st := range stats {
		rows = append(rows, &dirStats{
			Inode:      Ino(st.Inode),
			DataLength: st.DataLength,
			UsedInodes: st.UsedInodes,
			UsedSpace:  st.UsedSpace,
		})
	}
	return m.insertRows(rows)
}

func (m *dbMeta) insertRows(beans []interface{}) error {
	batch := m.getTxnBatchNum()
	for len(beans) > 0 {
		bs := utils.Min(batch, len(beans))
		err := m.txn(func(s *xorm.Session) error {
			n, err := s.Insert(beans[:bs])
			if err == nil && int(n) != bs {
				err = fmt.Errorf("only %d records inserted", n)
			}
			return err
		})
		if err != nil {
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
