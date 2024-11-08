package meta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"xorm.io/xorm"
)

func (m *dbMeta) BuildDumpedSeg(typ int, opt *DumpOption) iDumpedSeg {
	switch typ {
	case SegTypeFormat:
		return &formatDS{dumpedSeg{typ: typ}, m.getFormat(), opt.KeepSecret}
	case SegTypeCounter:
		return &sqlCounterDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeSustained:
		return &sqlSustainedDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeDelFile:
		return &sqlDelFileDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeSliceRef:
		return &sqlSliceRefDS{dumpedSeg{typ: typ, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.SliceRef{} }}}}
	case SegTypeAcl:
		return &sqlAclDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeXattr:
		return &sqlXattrDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeQuota:
		return &sqlQuotaDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeStat:
		return &sqlStatDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeNode:
		return &sqlNodeDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeNode, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Node{} }}}}}
	case SegTypeChunk:
		return &sqlChunkDBS{
			dumpedBatchSeg{dumpedSeg{typ: SegTypeChunk, meta: m},
				[]*sync.Pool{{New: func() interface{} { return &pb.Chunk{} }}, {New: func() interface{} { return &pb.Slice{} }}}},
		}
	case SegTypeEdge:
		return &sqlEdgeDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeEdge, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Edge{} }}}}}
	case SegTypeSymlink:
		return &sqlSymlinkDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeSymlink, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Symlink{} }}}}}
	}
	return nil
}

var (
	lsNodePool  = &sync.Pool{New: func() interface{} { return &node{} }}
	lsChkPool   = &sync.Pool{New: func() interface{} { return &chunk{} }}
	lsSlicePool = &sync.Pool{New: func() interface{} { return make([]byte, 0, sliceBytes*10) }}
	lsEdgePool  = &sync.Pool{New: func() interface{} { return &edge{} }}
	lsSymPool   = &sync.Pool{New: func() interface{} { return &symlink{} }}
)

func (m *dbMeta) BuildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	switch typ {
	case SegTypeFormat:
		return &formatLS{loadedSeg{typ: typ}}
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
		return &sqlNodeLS{loadedSeg{typ: typ, meta: m}, lsNodePool}
	case SegTypeChunk:
		return &sqlChunkLS{loadedSeg{typ: typ, meta: m}, lsChkPool, lsSlicePool}
	case SegTypeEdge:
		return &sqlEdgeLS{loadedSeg{typ: typ, meta: m}, lsEdgePool}
	case SegTypeSymlink:
		return &sqlSymlinkLS{loadedSeg{typ: typ, meta: m}, lsSymPool}
	}
	return nil
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

func (m *dbMeta) newCounterDS() iDumpedSeg {
	return &sqlCounterDS{dumpedSeg: dumpedSeg{typ: SegTypeCounter, meta: m}}
}

func (s *sqlCounterDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []counter
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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

func (s *sqlSustainedDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []sustained
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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
	logger.Debugf("dump %s total num %d", s, len(ss))
	return nil
}

type sqlDelFileDS struct {
	dumpedSeg
}

func (s *sqlDelFileDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []delfile
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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
	logger.Debugf("dump %s total num %d", s, len(delFiles.List))
	return nil
}

type sqlSliceRefDS struct {
	dumpedSeg
	pools []*sync.Pool
}

func (s *sqlSliceRefDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	eg, _ := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)

	taskFinished := false
	limit := 40960
	psrs := &pb.SliceRefList{List: make([]*pb.SliceRef, 0, 1024)}
	for start := 0; !taskFinished; start += limit {
		nStart := start
		eg.Go(func() error {
			var rows []sliceRef
			if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
				return s.Where("refs > 1").Limit(limit, nStart).Find(&rows)
			}); err != nil {
				taskFinished = true
				return err
			}

			if len(rows) == 0 {
				taskFinished = true
				return nil
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
	logger.Debugf("dump %s total num %d", s, len(psrs.List))
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

func (s *sqlAclDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []acl
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
		return s.Find(&rows)
	}); err != nil {
		return err
	}
	acls := &pb.AclList{List: make([]*pb.Acl, 0, len(rows))}
	for _, row := range rows {
		acl := &pb.Acl{
			Id:    row.Id,
			Owner: uint32(row.Owner),
			Group: uint32(row.Group),
			Other: uint32(row.Other),
			Mask:  uint32(row.Mask),
		}
		r := utils.ReadBuffer(row.NamedUsers)
		for r.HasMore() {
			acl.Users = append(acl.Users, &pb.AclEntry{Id: r.Get32(), Perm: uint32(r.Get16())})
		}
		r = utils.ReadBuffer(row.NamedGroups)
		for r.HasMore() {
			acl.Groups = append(acl.Groups, &pb.AclEntry{Id: r.Get32(), Perm: uint32(r.Get16())})
		}
		acls.List = append(acls.List, acl)
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, acls}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(acls.List))
	return nil
}

type sqlXattrDS struct {
	dumpedSeg
}

func (s *sqlXattrDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []xattr
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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

	logger.Debugf("dump %s total num %d", s, len(pxs.List))
	return dumpResult(ctx, ch, &dumpedResult{s, pxs})
}

type sqlQuotaDS struct {
	dumpedSeg
}

func (s *sqlQuotaDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []dirQuota
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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
	logger.Debugf("dump %s total num %d", s, len(pqs.List))
	return dumpResult(ctx, ch, &dumpedResult{s, pqs})
}

type sqlStatDS struct {
	dumpedSeg
}

func (s *sqlStatDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	var rows []dirStats
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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
	logger.Debugf("dump %s total num %d", s, len(pss.List))
	return dumpResult(ctx, ch, &dumpedResult{s, pss})
}

func sqlQueryBatch(ctx Context, s iDumpedSeg, opt *DumpOption, ch chan *dumpedResult, query func(ctx Context, limit, start int, sum *int64) (proto.Message, error)) error {
	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)

	taskFinished := false
	limit := 40960
	sum := int64(0)
	for start := 0; !taskFinished; start += limit {
		nStart := start
		eg.Go(func() error {
			msg, err := query(ctx, limit, nStart, &sum)
			if err != nil {
				taskFinished = true
				return err
			}
			if msg == nil {
				taskFinished = true
				return nil // finished
			}
			return dumpResult(nCtx, ch, &dumpedResult{s, msg})
		})
	}
	if err := eg.Wait(); err != nil {
		logger.Errorf("query %s err: %v", s, err)
		return err
	}
	logger.Debugf("dump %s total num %d", s, sum)
	return nil
}

type sqlNodeDBS struct {
	dumpedBatchSeg
}

func (s *sqlNodeDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, opt, ch, s.doQuery)
}

func (s *sqlNodeDBS) doQuery(ctx Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []node
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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
	for _, n := range rows {
		pn = s.pools[0].Get().(*pb.Node)
		pn.Inode = uint64(n.Inode)
		pn.Type = uint32(n.Type)
		pn.Flags = uint32(n.Flags)
		pn.Mode = uint32(n.Mode)
		pn.Uid = n.Uid
		pn.Gid = n.Gid
		pn.Atime = n.Atime
		pn.Mtime = n.Mtime
		pn.Ctime = n.Ctime
		pn.AtimeNsec = int32(n.Atimensec)
		pn.MtimeNsec = int32(n.Mtimensec)
		pn.CtimeNsec = int32(n.Ctimensec)
		pn.Nlink = n.Nlink
		pn.Length = n.Length
		pn.Rdev = n.Rdev
		pn.Parent = uint64(n.Parent)
		pn.AccessAclId = n.AccessACLId
		pn.DefaultAclId = n.DefaultACLId
		pns.List = append(pns.List, pn)
	}
	atomic.AddInt64(sum, int64(len(pns.List)))
	return pns, nil
}

func (s *sqlNodeDBS) release(msg proto.Message) {
	pns := msg.(*pb.NodeList)
	for _, pn := range pns.List {
		s.pools[0].Put(pn)
	}
	pns.List = nil
}

type sqlChunkDBS struct {
	dumpedBatchSeg
}

func (s *sqlChunkDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, opt, ch, s.doQuery)
}

func (s *sqlChunkDBS) doQuery(ctx Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []chunk
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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

		n := len(c.Slices) / sliceBytes
		pc.Slices = make([]*pb.Slice, 0, n)
		var ps *pb.Slice
		for i := 0; i < n; i++ {
			ps = s.pools[1].Get().(*pb.Slice)
			rb := utils.ReadBuffer(c.Slices[i*sliceBytes:])
			ps.Pos = rb.Get32()
			ps.Id = rb.Get64()
			ps.Size = rb.Get32()
			ps.Off = rb.Get32()
			ps.Len = rb.Get32()
			pc.Slices = append(pc.Slices, ps)
		}
		pcs.List = append(pcs.List, pc)
	}
	atomic.AddInt64(sum, int64(len(pcs.List)))
	return pcs, nil
}

func (s *sqlChunkDBS) release(msg proto.Message) {
	pcs := msg.(*pb.ChunkList)
	for _, pc := range pcs.List {
		for _, ps := range pc.Slices {
			s.pools[1].Put(ps)
		}
		pc.Slices = nil
		s.pools[0].Put(pc)
	}
	pcs.List = nil
}

type sqlEdgeDBS struct {
	dumpedBatchSeg
}

func (s *sqlEdgeDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, opt, ch, s.doQuery)
}

func (s *sqlEdgeDBS) doQuery(ctx Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []edge
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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

type sqlSymlinkDBS struct {
	dumpedBatchSeg
}

func (s *sqlSymlinkDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	return sqlQueryBatch(ctx, s, opt, ch, s.doQuery)
}

func (s *sqlSymlinkDBS) doQuery(ctx Context, limit, start int, sum *int64) (proto.Message, error) {
	var rows []symlink
	if err := s.meta.(*dbMeta).roTxn(func(s *xorm.Session) error {
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

func (m *dbMeta) DumpMetaV2(ctx Context, w io.Writer, opt *DumpOption) (err error) {
	opt = opt.check()

	bak := NewBakFormat()
	ch := make(chan *dumpedResult, 100)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		var err error
		defer func() {
			if err != nil {
				ctx.Cancel()
			} else {
				close(ch)
			}
			wg.Done()
		}()

		for typ := SegTypeFormat; typ < SegTypeMax; typ++ {
			seg := m.BuildDumpedSeg(typ, opt)
			if seg == nil {
				logger.Warnf("skip dump segment %d", typ)
				continue
			}
			if err = seg.query(ctx, opt, ch); err != nil {
				return
			}
		}
	}()

	finished := false
	for !finished {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case res, ok := <-ch:
			if !ok {
				finished = true
				break
			}
			if err := bak.WriteSegment(w, &BakSegment{Val: res.msg}); err != nil {
				logger.Errorf("write %s err: %v", res.seg, err)
				ctx.Cancel()
				wg.Wait()
				return err
			}
			res.seg.release(res.msg)
		}
	}

	wg.Wait()
	return bak.WriteFooter(w)
}

type sqlCounterLS struct {
	loadedSeg
}

func (s *sqlCounterLS) insert(ctx Context, msg proto.Message) error {
	counters := msg.(*pb.Counters)
	fields := getSQLCounterFields(counters)

	var rows []interface{}
	for name, field := range fields {
		rows = append(rows, counter{Name: name, Value: *field})
	}
	logger.Debugf("insert counters %+v", rows)
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlSustainedLS struct {
	loadedSeg
}

func (s *sqlSustainedLS) insert(ctx Context, msg proto.Message) error {
	sustaineds := msg.(*pb.SustainedList)
	rows := make([]interface{}, 0, len(sustaineds.List))
	for _, s := range sustaineds.List {
		for _, inode := range s.Inodes {
			rows = append(rows, sustained{Sid: s.Sid, Inode: Ino(inode)})
		}
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlDelFileLS struct {
	loadedSeg
}

func (s *sqlDelFileLS) insert(ctx Context, msg proto.Message) error {
	delfiles := msg.(*pb.DelFileList)
	rows := make([]interface{}, 0, len(delfiles.List))
	for _, f := range delfiles.List {
		rows = append(rows, &delfile{Inode: Ino(f.Inode), Length: f.Length, Expire: f.Expire})
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlSliceRefLS struct {
	loadedSeg
}

func (s *sqlSliceRefLS) insert(ctx Context, msg proto.Message) error {
	srs := msg.(*pb.SliceRefList)
	rows := make([]interface{}, 0, len(srs.List))
	for _, sr := range srs.List {
		rows = append(rows, &sliceRef{Id: sr.Id, Size: sr.Size, Refs: int(sr.Refs)})
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlAclLS struct {
	loadedSeg
}

func (s *sqlAclLS) insert(ctx Context, msg proto.Message) error {
	acls := msg.(*pb.AclList)
	rows := make([]interface{}, 0, len(acls.List))
	for _, a := range acls.List {
		ba := &acl{}
		ba.Id = a.Id
		ba.Owner = uint16(a.Owner)
		ba.Group = uint16(a.Group)
		ba.Mask = uint16(a.Mask)
		ba.Other = uint16(a.Other)

		w := utils.NewBuffer(uint32(len(a.Users) * 6))
		for _, u := range a.Users {
			w.Put32(u.Id)
			w.Put16(uint16(u.Perm))
		}
		ba.NamedUsers = w.Bytes()

		w = utils.NewBuffer(uint32(len(a.Groups) * 6))
		for _, g := range a.Groups {
			w.Put32(g.Id)
			w.Put16(uint16(g.Perm))
		}
		ba.NamedGroups = w.Bytes()
		rows = append(rows, ba)
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlXattrLS struct {
	loadedSeg
}

func (s *sqlXattrLS) insert(ctx Context, msg proto.Message) error {
	xattrs := msg.(*pb.XattrList)
	rows := make([]interface{}, 0, len(xattrs.List))
	for _, x := range xattrs.List {
		rows = append(rows, &xattr{Inode: Ino(x.Inode), Name: x.Name, Value: x.Value})
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlQuotaLS struct {
	loadedSeg
}

func (s *sqlQuotaLS) insert(ctx Context, msg proto.Message) error {
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
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlStatLS struct {
	loadedSeg
}

func (s *sqlStatLS) insert(ctx Context, msg proto.Message) error {
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
	logger.Debugf("insert %s total num %d", s, len(rows))
	return s.meta.(*dbMeta).insertSQL(rows)
}

type sqlNodeLS struct {
	loadedSeg
	pool *sync.Pool
}

func (s *sqlNodeLS) insert(ctx Context, msg proto.Message) error {
	nodes := msg.(*pb.NodeList)
	rows := make([]interface{}, 0, len(nodes.List))
	var pn *node
	for _, n := range nodes.List {
		pn = s.pool.Get().(*node)
		pn.Inode = Ino(n.Inode)
		pn.Type = uint8(n.Type)
		pn.Flags = uint8(n.Flags)
		pn.Mode = uint16(n.Mode)
		pn.Uid = n.Uid
		pn.Gid = n.Gid
		pn.Atime = n.Atime
		pn.Mtime = n.Mtime
		pn.Ctime = n.Ctime
		pn.Atimensec = int16(n.AtimeNsec)
		pn.Mtimensec = int16(n.MtimeNsec)
		pn.Ctimensec = int16(n.CtimeNsec)
		pn.Nlink = n.Nlink
		pn.Length = n.Length
		pn.Rdev = n.Rdev
		pn.Parent = Ino(n.Parent)
		pn.AccessACLId = n.AccessAclId
		pn.DefaultACLId = n.DefaultAclId
		rows = append(rows, pn)
	}
	err := s.meta.(*dbMeta).insertSQL(rows)
	for _, n := range rows {
		s.pool.Put(n)
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return err
}

type sqlChunkLS struct {
	loadedSeg
	chunkPool *sync.Pool
	slicePool *sync.Pool
}

func (s *sqlChunkLS) insert(ctx Context, msg proto.Message) error {
	chunks := msg.(*pb.ChunkList)
	rows := make([]interface{}, 0, len(chunks.List))
	var pc *chunk
	for _, c := range chunks.List {
		pc = s.chunkPool.Get().(*chunk)
		pc.Id = 0
		pc.Inode = Ino(c.Inode)
		pc.Indx = c.Index

		n := len(c.Slices) * sliceBytes
		pc.Slices = s.slicePool.Get().([]byte)[:0]
		if cap(pc.Slices) < n {
			pc.Slices = make([]byte, 0, n)
		}
		for _, s := range c.Slices {
			// keep BigEndian order for slices, same as in slice.go
			pc.Slices = binary.BigEndian.AppendUint32(pc.Slices, s.Pos)
			pc.Slices = binary.BigEndian.AppendUint64(pc.Slices, s.Id)
			pc.Slices = binary.BigEndian.AppendUint32(pc.Slices, s.Size)
			pc.Slices = binary.BigEndian.AppendUint32(pc.Slices, s.Off)
			pc.Slices = binary.BigEndian.AppendUint32(pc.Slices, s.Len)
		}
		rows = append(rows, pc)
	}
	err := s.meta.(*dbMeta).insertSQL(rows)

	for _, chk := range rows {
		c := chk.(*chunk)
		s.slicePool.Put(c.Slices)
		s.chunkPool.Put(c)
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return err
}

type sqlEdgeLS struct {
	loadedSeg
	pool *sync.Pool
}

func (s *sqlEdgeLS) insert(ctx Context, msg proto.Message) error {
	edges := msg.(*pb.EdgeList)
	rows := make([]interface{}, 0, len(edges.List))
	var pe *edge
	for _, e := range edges.List {
		pe = s.pool.Get().(*edge)
		pe.Id = 0
		pe.Parent = Ino(e.Parent)
		pe.Inode = Ino(e.Inode)
		pe.Name = e.Name
		pe.Type = uint8(e.Type)
		rows = append(rows, pe)
	}

	err := s.meta.(*dbMeta).insertSQL(rows)
	for _, e := range rows {
		s.pool.Put(e)
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return err
}

type sqlSymlinkLS struct {
	loadedSeg
	pool *sync.Pool
}

func (s *sqlSymlinkLS) insert(ctx Context, msg proto.Message) error {
	symlinks := msg.(*pb.SymlinkList)
	rows := make([]interface{}, 0, len(symlinks.List))
	var ps *symlink
	for _, sl := range symlinks.List {
		ps = s.pool.Get().(*symlink)
		ps.Inode = Ino(sl.Inode)
		ps.Target = sl.Target
		rows = append(rows, ps)
	}

	err := s.meta.(*dbMeta).insertSQL(rows)
	for _, sl := range rows {
		s.pool.Put(sl)
	}
	logger.Debugf("insert %s total num %d", s, len(rows))
	return err
}

func (m *dbMeta) insertSQL(beans []interface{}) error {
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

func (m *dbMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	opt = opt.check()
	if err := m.checkAddr(); err != nil {
		return err
	}
	if err := m.syncAllTables(); err != nil {
		return err
	}

	type task struct {
		msg proto.Message
		seg iLoadedSeg
	}

	var wg sync.WaitGroup
	taskCh := make(chan *task, 100)

	workerFunc := func(ctx Context, taskCh <-chan *task) {
		defer wg.Done()
		finished := false
		for !finished {
			select {
			case <-ctx.Done():
				return
			case task, ok := <-taskCh:
				if !ok {
					finished = true
					break
				}

				if err := task.seg.insert(ctx, task.msg); err != nil {
					logger.Errorf("failed to insert %s: %s", task.seg, err)
					ctx.Cancel()
					return
				}
				/*
						rows, bs := task.decoder.decode(task.msg), batchSize
						for len(rows) > 0 {
							if len(rows) < bs {
								bs = len(rows)
							}
							if err := insert(rows[:bs]); err != nil {
								logger.Errorf("Write %d beans: %s", len(rows), err)
								ctx.Cancel()
								return
							}
							task.decoder.release(rows[:bs])
							rows = rows[bs:]
					}
				*/
			}
		}
	}

	for i := 0; i < opt.CoNum; i++ {
		wg.Add(1)
		go workerFunc(ctx, taskCh)
	}

	bak := NewBakFormat()
	finished := false
	for !finished {
		seg, err := bak.ReadSegment(r)
		if err != nil {
			if errors.Is(err, ErrBakEOF) {
				finished = true
				close(taskCh)
				break
			}
			ctx.Cancel()
			wg.Wait()
			return err
		}

		ls := m.BuildLoadedSeg(int(seg.Typ), opt)
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case taskCh <- &task{seg.Val, ls}:
		}
	}
	wg.Wait()
	return nil
}
