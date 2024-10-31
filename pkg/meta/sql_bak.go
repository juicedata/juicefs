package meta

import (
	"encoding/binary"
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

type DumpOption struct {
	KeepSecret bool
	CoNum      int
	// SkipTrash  bool
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

type segNewData interface {
	newData() interface{}
}

type segWriter interface {
	write(w io.Writer, msg proto.Message) error
	release(msg proto.Message)
}

type segEncoder interface {
	encode(interface{}) proto.Message
	count(proto.Message) int
}

type iDumpSeg interface {
	String() string
	query() proto.Message
	segNewData
	segEncoder
	segWriter
}

type dumpSeg struct {
	iDumpSeg
	name string
	bak  *BakFormat
}

func (s *dumpSeg) String() string                             { return s.name }
func (s *dumpSeg) write(w io.Writer, msg proto.Message) error { return s.bak.writeSeg(w, msg) }
func (s *dumpSeg) release(msg proto.Message)                  {}

type formatDS struct {
	dumpSeg
	f          *Format
	keepSecret bool
}

func newFormatDS(bak *BakFormat, f *Format, keepSecret bool) iDumpSeg {
	return &formatDS{dumpSeg{name: "Format", bak: bak}, f, keepSecret}
}

func (s *formatDS) newData() interface{} { return nil }

func (s *formatDS) encode(rows interface{}) proto.Message {
	return newPBFormat(s.f, s.keepSecret)
}

func (s *formatDS) count(msg proto.Message) int { return 1 }

type counterDS struct {
	dumpSeg
}

func newCounterDS(bak *BakFormat) iDumpSeg {
	return &counterDS{dumpSeg: dumpSeg{name: "Counter", bak: bak}}
}

func (s *counterDS) newData() interface{} {
	return &[]counter{}
}

func (s *counterDS) encode(rows interface{}) proto.Message {
	crows := *(rows.(*[]counter))
	if len(crows) == 0 {
		return nil
	}
	counters := &pb.Counters{}
	fieldMap := getCounterFields(counters)
	for _, row := range crows {
		if fieldPtr, ok := fieldMap[row.Name]; ok {
			*fieldPtr = row.Value
		}
	}
	return counters
}

func (s *counterDS) count(msg proto.Message) int { return 6 }

type delFileDS struct {
	dumpSeg
}

func newDelFileDS(bak *BakFormat) iDumpSeg {
	return &delFileDS{dumpSeg: dumpSeg{name: "DelFiles", bak: bak}}
}

func (s *delFileDS) newData() interface{} { return &[]delfile{} }

func (s *delFileDS) encode(rows interface{}) proto.Message {
	drows := *(rows.(*[]delfile))
	if len(drows) == 0 {
		return nil
	}
	delFiles := &pb.DelFiles{
		Files: make([]*pb.DelFile, 0, len(drows)),
	}
	for _, row := range drows {
		delFiles.Files = append(delFiles.Files, &pb.DelFile{Inode: uint64(row.Inode), Length: row.Length, Expire: row.Expire})
	}
	return delFiles
}

func (s *delFileDS) count(msg proto.Message) int { return len(msg.(*pb.DelFiles).Files) }

type sustainedDS struct {
	dumpSeg
}

func newSustainedDS(bak *BakFormat) iDumpSeg {
	return &sustainedDS{dumpSeg: dumpSeg{name: "Sustaineds", bak: bak}}
}

func (s *sustainedDS) newData() interface{} { return &[]sustained{} }

func (s *sustainedDS) encode(rows interface{}) proto.Message {
	srows := *(rows.(*[]sustained))
	if len(srows) == 0 {
		return nil
	}
	ss := make(map[uint64][]uint64)
	for _, row := range srows {
		ss[row.Sid] = append(ss[row.Sid], uint64(row.Inode))
	}

	pss := &pb.Sustaineds{
		Sustaineds: make([]*pb.Sustained, 0, len(ss)),
	}
	for k, v := range ss {
		pss.Sustaineds = append(pss.Sustaineds, &pb.Sustained{Sid: k, Inodes: v})
	}
	return pss
}

func (s *sustainedDS) count(msg proto.Message) int { return len(msg.(*pb.Sustaineds).Sustaineds) }

type aclDS struct {
	dumpSeg
}

func newAclDS(bak *BakFormat) iDumpSeg {
	return &aclDS{dumpSeg: dumpSeg{name: "Acls", bak: bak}}
}

func (s *aclDS) newData() interface{} { return &[]acl{} }

func (s *aclDS) encode(rows interface{}) proto.Message {
	acls := *(rows.(*[]acl))
	if len(acls) == 0 {
		return nil
	}
	pas := &pb.Acls{
		Acls: make([]*pb.Acl, 0, len(acls)),
	}
	for _, a := range acls {
		pa := &pb.Acl{
			Id:    a.Id,
			Owner: uint32(a.Owner),
			Group: uint32(a.Group),
			Other: uint32(a.Other),
			Mask:  uint32(a.Mask),
		}
		r := utils.ReadBuffer(a.NamedUsers)
		for r.HasMore() {
			pa.Users = append(pa.Users, &pb.AclEntry{
				Id:   r.Get32(),
				Perm: uint32(r.Get16()),
			})
		}
		r = utils.ReadBuffer(a.NamedGroups)
		for r.HasMore() {
			pa.Groups = append(pa.Groups, &pb.AclEntry{
				Id:   r.Get32(),
				Perm: uint32(r.Get16()),
			})
		}
		pas.Acls = append(pas.Acls, pa)
	}
	return pas
}

func (s *aclDS) count(msg proto.Message) int { return len(msg.(*pb.Acls).Acls) }

type iDumpBatchSeg interface {
	String() string
	segNewData
	segEncoder
	segWriter
}

type dumpBatchSeg struct {
	iDumpBatchSeg
	bak   *BakFormat
	name  string
	pools []*sync.Pool
}

func (s *dumpBatchSeg) String() string { return s.name }

func (s *dumpBatchSeg) write(w io.Writer, msg proto.Message) error {
	return s.bak.writeBatchSeg(w, msg)
}

type edgeDBS struct {
	dumpBatchSeg
}

func newEdgeDBS(bak *BakFormat) iDumpBatchSeg {
	return &edgeDBS{dumpBatchSeg{name: "Edges", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Edge{} }}}}}
}

func (s *edgeDBS) newData() interface{} { return &[]edge{} }

func (s *edgeDBS) encode(rows interface{}) proto.Message {
	edges := *(rows.(*[]edge))
	if len(edges) == 0 {
		return nil
	}
	pes := &pb.EdgeBatch{
		Batch: make([]*pb.Edge, 0, len(edges)),
	}
	var pe *pb.Edge
	for _, e := range edges {
		pe = s.pools[0].Get().(*pb.Edge)
		pe.Parent = uint64(e.Parent)
		pe.Inode = uint64(e.Inode)
		pe.Name = e.Name
		pe.Type = uint32(e.Type)
		pes.Batch = append(pes.Batch, pe)
	}
	return pes
}

func (s *edgeDBS) count(msg proto.Message) int { return len(msg.(*pb.EdgeBatch).Batch) }

func (s *edgeDBS) release(msg proto.Message) {
	pes := msg.(*pb.EdgeBatch)
	for _, pe := range pes.Batch {
		s.pools[0].Put(pe)
	}
	pes.Batch = nil
}

type nodeDBS struct {
	dumpBatchSeg
}

func newNodeDBS(bak *BakFormat) iDumpBatchSeg {
	return &nodeDBS{dumpBatchSeg{name: "Nodes", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Node{} }}}}}
}

func (s *nodeDBS) newData() interface{} { return &[]node{} }

func (s *nodeDBS) encode(rows interface{}) proto.Message {
	nodes := *(rows.(*[]node))
	if len(nodes) == 0 {
		return nil
	}
	pns := &pb.NodeBatch{
		Batch: make([]*pb.Node, 0, len(nodes)),
	}
	var pn *pb.Node
	for _, n := range nodes {
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
		pns.Batch = append(pns.Batch, pn)
	}
	return pns
}

func (s *nodeDBS) count(msg proto.Message) int { return len(msg.(*pb.NodeBatch).Batch) }

func (s *nodeDBS) release(msg proto.Message) {
	pns := msg.(*pb.NodeBatch)
	for _, pn := range pns.Batch {
		s.pools[0].Put(pn)
	}
	pns.Batch = nil
}

type chunkDBS struct {
	dumpBatchSeg
}

func newChunkDBS(bak *BakFormat) iDumpBatchSeg {
	return &chunkDBS{dumpBatchSeg{name: "Chunks", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Chunk{} }}, {New: func() interface{} { return &pb.Slice{} }}}}}
}

func (s *chunkDBS) newData() interface{} { return &[]chunk{} }

func (s *chunkDBS) encode(rows interface{}) proto.Message {
	chunks := *(rows.(*[]chunk))
	if len(chunks) == 0 {
		return nil
	}
	pcs := &pb.ChunkBatch{
		Batch: make([]*pb.Chunk, 0, len(chunks)),
	}
	var pc *pb.Chunk
	for _, c := range chunks {
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
		pcs.Batch = append(pcs.Batch, pc)
	}
	return pcs
}

func (s *chunkDBS) count(msg proto.Message) int { return len(msg.(*pb.ChunkBatch).Batch) }

func (s *chunkDBS) release(msg proto.Message) {
	pcs := msg.(*pb.ChunkBatch)
	for _, pc := range pcs.Batch {
		for _, ps := range pc.Slices {
			s.pools[1].Put(ps)
		}
		pc.Slices = nil
		s.pools[0].Put(pc)
	}
	pcs.Batch = nil
}

type sliceRefDBS struct {
	dumpBatchSeg
}

func newSliceRefDBS(bak *BakFormat) iDumpBatchSeg {
	return &sliceRefDBS{dumpBatchSeg{name: "SliceRefs", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.SliceRef{} }}}}}
}

func (s *sliceRefDBS) newData() interface{} { return &[]sliceRef{} }

func (s *sliceRefDBS) encode(rows interface{}) proto.Message {
	sliceRefs := *(rows.(*[]sliceRef))
	if len(sliceRefs) == 0 {
		return nil
	}
	psrs := &pb.SliceRefBatch{
		Batch: make([]*pb.SliceRef, 0, len(sliceRefs)),
	}
	var psr *pb.SliceRef
	for _, sr := range sliceRefs {
		psr = s.pools[0].Get().(*pb.SliceRef)
		psr.Id = sr.Id
		psr.Size = sr.Size
		psr.Refs = int64(sr.Refs)
		psrs.Batch = append(psrs.Batch, psr)
	}
	return psrs
}

func (s *sliceRefDBS) count(msg proto.Message) int { return len(msg.(*pb.SliceRefBatch).Batch) }

func (s *sliceRefDBS) release(msg proto.Message) {
	psrs := msg.(*pb.SliceRefBatch)
	for _, psr := range psrs.Batch {
		s.pools[0].Put(psr)
	}
	psrs.Batch = nil
}

type symlinkDBS struct {
	dumpBatchSeg
}

func newSymlinkDBS(bak *BakFormat) iDumpBatchSeg {
	return &symlinkDBS{dumpBatchSeg{name: "Symlinks", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Symlink{} }}}}}
}

func (s *symlinkDBS) newData() interface{} { return &[]symlink{} }

func (s *symlinkDBS) encode(rows interface{}) proto.Message {
	symlinks := *(rows.(*[]symlink))
	if len(symlinks) == 0 {
		return nil
	}
	pss := &pb.SymlinkBatch{
		Batch: make([]*pb.Symlink, 0, len(symlinks)),
	}
	var ps *pb.Symlink
	for _, sl := range symlinks {
		ps = s.pools[0].Get().(*pb.Symlink)
		ps.Inode = uint64(sl.Inode)
		ps.Target = sl.Target
		pss.Batch = append(pss.Batch, ps)
	}
	return pss
}

func (s *symlinkDBS) count(msg proto.Message) int { return len(msg.(*pb.SymlinkBatch).Batch) }

func (s *symlinkDBS) release(msg proto.Message) {
	pss := msg.(*pb.SymlinkBatch)
	for _, ps := range pss.Batch {
		s.pools[0].Put(ps)
	}
	pss.Batch = nil
}

type xattrDBS struct {
	dumpBatchSeg
}

func newXattrDBS(bak *BakFormat) iDumpBatchSeg {
	return &xattrDBS{dumpBatchSeg{name: "Xattrs", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Xattr{} }}}}}
}

func (s *xattrDBS) newData() interface{} { return &[]xattr{} }

func (s *xattrDBS) encode(rows interface{}) proto.Message {
	xattrs := *(rows.(*[]xattr))
	if len(xattrs) == 0 {
		return nil
	}
	pxs := &pb.XattrBatch{
		Batch: make([]*pb.Xattr, 0, len(xattrs)),
	}
	var px *pb.Xattr
	for _, x := range xattrs {
		px = s.pools[0].Get().(*pb.Xattr)
		px.Inode = uint64(x.Inode)
		px.Name = x.Name
		px.Value = x.Value
		pxs.Batch = append(pxs.Batch, px)
	}
	return pxs
}

func (s *xattrDBS) count(msg proto.Message) int { return len(msg.(*pb.XattrBatch).Batch) }

func (s *xattrDBS) release(msg proto.Message) {
	pxs := msg.(*pb.XattrBatch)
	for _, px := range pxs.Batch {
		s.pools[0].Put(px)
	}
	pxs.Batch = nil
}

type quotaDBS struct {
	dumpBatchSeg
}

func newQuotaDBS(bak *BakFormat) iDumpBatchSeg {
	return &quotaDBS{dumpBatchSeg{name: "Quotas", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Quota{} }}}}}
}

func (s *quotaDBS) newData() interface{} { return &[]dirQuota{} }

func (s *quotaDBS) encode(rows interface{}) proto.Message {
	quotas := *(rows.(*[]dirQuota))
	if len(quotas) == 0 {
		return nil
	}
	pqs := &pb.QuotaBatch{
		Batch: make([]*pb.Quota, 0, len(quotas)),
	}
	var pq *pb.Quota
	for _, q := range quotas {
		pq = s.pools[0].Get().(*pb.Quota)
		pq.Inode = uint64(q.Inode)
		pq.MaxSpace = q.MaxSpace
		pq.MaxInodes = q.MaxInodes
		pq.UsedSpace = q.UsedSpace
		pq.UsedInodes = q.UsedInodes
		pqs.Batch = append(pqs.Batch, pq)
	}
	return pqs
}

func (s *quotaDBS) count(msg proto.Message) int { return len(msg.(*pb.QuotaBatch).Batch) }

func (s *quotaDBS) release(msg proto.Message) {
	pqs := msg.(*pb.QuotaBatch)
	for _, pq := range pqs.Batch {
		s.pools[0].Put(pq)
	}
	pqs.Batch = nil
}

type statDBS struct {
	dumpBatchSeg
}

func newStatDBS(bak *BakFormat) iDumpBatchSeg {
	return &statDBS{dumpBatchSeg{name: "Stats", bak: bak, pools: []*sync.Pool{{New: func() interface{} { return &pb.Stat{} }}}}}
}

func (s *statDBS) newData() interface{} { return &[]dirStats{} }

func (s *statDBS) encode(rows interface{}) proto.Message {
	stats := *(rows.(*[]dirStats))
	if len(stats) == 0 {
		return nil
	}
	pss := &pb.StatBatch{
		Batch: make([]*pb.Stat, 0, len(stats)),
	}
	var ps *pb.Stat
	for _, st := range stats {
		ps = s.pools[0].Get().(*pb.Stat)
		ps.Inode = uint64(st.Inode)
		ps.DataLength = st.DataLength
		ps.UsedInodes = st.UsedInodes
		ps.UsedSpace = st.UsedSpace
		pss.Batch = append(pss.Batch, ps)
	}
	return pss
}

func (s *statDBS) count(msg proto.Message) int { return len(msg.(*pb.StatBatch).Batch) }

func (s *statDBS) release(msg proto.Message) {
	pss := msg.(*pb.StatBatch)
	for _, ps := range pss.Batch {
		s.pools[0].Put(ps)
	}
	pss.Batch = nil
}

func (m *dbMeta) DumpMetaV2(ctx Context, w io.WriteSeeker, opt *DumpOption) (err error) {
	opt = opt.check()

	bak := newBakFormat()
	bak.seekForWrite(w)

	queryAll := func(rows interface{}) error {
		if rows == nil {
			return nil
		}
		return m.roTxn(func(s *xorm.Session) error {
			return s.Find(rows)
		})
	}

	queryPage := func(rows interface{}, limit, start int) error {
		return m.roTxn(func(s *xorm.Session) error {
			return s.Limit(limit, start).Find(rows)
		})
	}

	type result struct {
		seg segWriter
		msg proto.Message
	}

	ch := make(chan *result, 100)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		for _, seg := range []iDumpSeg{
			newFormatDS(bak, m.getFormat(), opt.KeepSecret),
			newCounterDS(bak),
			newSustainedDS(bak),
			newDelFileDS(bak),
			newAclDS(bak),
		} {
			// 1. 不同engine查询实现 -> proto.Message
			// 1.1 sql: newData + encode, 查询语句相同, 只是面向对象不同
			// 1.2 redis: 不同查询语句, 转换成proto.Message
			// 1.3 tkv: 不同遍历语句, 转换成proto.Message
			// 2. proto.Message 写入到 w

			// TODO: 不同存储引擎创建不同seg实现, 其中的查询实现不同

			rows := seg.newData()
			if err := queryAll(rows); err != nil {
				logger.Errorf("query %s err: %v", seg, err)
				ctx.Cancel()
				return
			}
			msg := seg.encode(rows)
			if msg == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case ch <- &result{seg, msg}:
			}
		}

		for _, seg := range []iDumpBatchSeg{
			newNodeDBS(bak),
			newChunkDBS(bak),
			newEdgeDBS(bak),
			newSliceRefDBS(bak),
			newSymlinkDBS(bak),
			newXattrDBS(bak),
			newQuotaDBS(bak),
			newStatDBS(bak),
		} {
			// 不同存储引擎, batch的处理实现不同
			// 内部的查询实现也要区分
			eg, nCtx := errgroup.WithContext(ctx)
			eg.SetLimit(opt.CoNum)

			taskFinished := false
			limit := 40960
			sum := int64(0)
			for start := 0; !taskFinished; start += limit {
				nSeg, nStart := seg, start
				eg.Go(func() error {
					rows := nSeg.newData()
					if err := queryPage(rows, limit, int(nStart)); err != nil {
						taskFinished = true
						return err
					}
					msg := nSeg.encode(rows)
					if msg == nil {
						taskFinished = true // real end
						return nil
					}
					atomic.AddInt64(&sum, int64(nSeg.count(msg)))
					select {
					case <-nCtx.Done():
						taskFinished = true
						return nCtx.Err()
					case ch <- &result{nSeg, msg}:
					}
					return nil
				})
			}
			if err := eg.Wait(); err != nil {
				logger.Errorf("query %s err: %v", seg, err)
				ctx.Cancel()
				return
			}
			logger.Infof("dump %s total num %d", seg, sum) // TODO hjf debug
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
			if err := res.seg.write(w, res.msg); err != nil {
				logger.Errorf("write %s err: %v", res.seg, err)
				ctx.Cancel()
				wg.Wait()
				return err
			}
			res.seg.release(res.msg)
		}
	}

	wg.Wait()
	return bak.writeHeader(w)
}

type LoadOption struct {
	CoNum int
}

func (opt *LoadOption) check() *LoadOption {
	if opt == nil {
		opt = &LoadOption{}
	}
	if opt.CoNum < 1 {
		opt.CoNum = 1
	}
	return opt
}

type segReader interface {
	read(r io.Reader, msg proto.Message) error
}

type segDecoder interface {
	decode(proto.Message) []interface{}
	count(proto.Message) int
	release(rows []interface{})
}

type iLoadSeg interface {
	String() string
	newMsg() proto.Message
	segReader
	segDecoder
}

type loadSeg struct {
	iLoadSeg
	bak  *BakFormat
	name string
	size uint32
}

func (s *loadSeg) String() string { return s.name }

func (s *loadSeg) read(r io.Reader, msg proto.Message) error {
	return s.bak.readSeg(r, s.name, int(s.size), msg)
}

func (s *loadSeg) release(rows []interface{}) {}

type formatLS struct {
	loadSeg
}

func newFormatLS(bak *BakFormat) iLoadSeg {
	return &formatLS{loadSeg{bak: bak, name: "Format", size: bak.Header.FormatSize}}
}

func (s *formatLS) newMsg() proto.Message                  { return nil }
func (s *formatLS) decode(msg proto.Message) []interface{} { return nil }
func (s *formatLS) count(msg proto.Message) int            { return 1 }

type counterLS struct {
	loadSeg
}

func newCounterLS(bak *BakFormat) iLoadSeg {
	return &counterLS{loadSeg{bak: bak, name: "Counters", size: bak.Header.CounterSize}}
}

func (s *counterLS) newMsg() proto.Message { return &pb.Counters{} }

func (s *counterLS) decode(msg proto.Message) []interface{} {
	fields := getCounterFields(msg.(*pb.Counters))
	counters := make([]interface{}, 0, len(fields))
	for name, field := range fields {
		counters = append(counters, &counter{Name: name, Value: *field})
	}
	return counters
}

func (s *counterLS) count(msg proto.Message) int { return 6 }

type sustainedLS struct {
	loadSeg
}

func newSustainedLS(bak *BakFormat) iLoadSeg {
	return &sustainedLS{loadSeg{bak: bak, name: "Sustaineds", size: bak.Header.SustainedSize}}
}

func (s *sustainedLS) newMsg() proto.Message { return &pb.Sustaineds{} }

func (s *sustainedLS) decode(msg proto.Message) []interface{} {
	sustaineds := msg.(*pb.Sustaineds)
	rows := make([]interface{}, 0, len(sustaineds.Sustaineds))
	for _, s := range sustaineds.Sustaineds {
		for _, inode := range s.Inodes {
			rows = append(rows, &sustained{Sid: s.Sid, Inode: Ino(inode)})
		}
	}
	return rows
}

func (s *sustainedLS) count(msg proto.Message) int { return len(msg.(*pb.Sustaineds).Sustaineds) }

type delFileLS struct {
	loadSeg
}

func newDelFileLS(bak *BakFormat) iLoadSeg {
	return &delFileLS{loadSeg{bak: bak, name: "DelFiles", size: bak.Header.DelFileSize}}
}

func (s *delFileLS) newMsg() proto.Message { return &pb.DelFiles{} }

func (s *delFileLS) decode(msg proto.Message) []interface{} {
	delfiles := msg.(*pb.DelFiles)
	rows := make([]interface{}, 0, len(delfiles.Files))
	for _, f := range delfiles.Files {
		rows = append(rows, &delfile{Inode: Ino(f.Inode), Length: f.Length, Expire: f.Expire})
	}
	return rows
}

func (s *delFileLS) count(msg proto.Message) int { return len(msg.(*pb.DelFiles).Files) }

type aclLS struct {
	loadSeg
}

func newAclLS(bak *BakFormat) iLoadSeg {
	return &aclLS{loadSeg: loadSeg{bak: bak, name: "Acls", size: bak.Header.AclSize}}
}

func (s *aclLS) newMsg() proto.Message { return &pb.Acls{} }

func (s *aclLS) decode(msg proto.Message) []interface{} {
	acls := msg.(*pb.Acls)
	rows := make([]interface{}, 0, len(acls.Acls))
	for _, a := range acls.Acls {
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
	return rows
}

func (s *aclLS) count(msg proto.Message) int { return len(msg.(*pb.Acls).Acls) }

type batchSegReader interface {
	read(r io.Reader, idx int, msg proto.Message) error
}

type iLoadBatchSeg interface {
	num() int
	String() string
	newMsg() proto.Message
	batchSegReader
	segDecoder
}

type loadBatchSeg struct {
	iLoadBatchSeg
	bak   *BakFormat
	name  string
	n     uint32
	pools []*sync.Pool
}

func (s *loadBatchSeg) String() string { return s.name }

func (s *loadBatchSeg) num() int { return int(s.n) }

func (s *loadBatchSeg) read(r io.Reader, idx int, msg proto.Message) error {
	return s.bak.readBatchSeg(r, fmt.Sprintf("%s-%d", s.name, idx), msg)
}

func (s *loadBatchSeg) release(rows []interface{}) {
	for _, row := range rows {
		s.pools[0].Put(row)
	}
}

type edgeLBS struct {
	loadBatchSeg
}

func newEdgeLBS(bak *BakFormat) iLoadBatchSeg {
	return &edgeLBS{loadBatchSeg{bak: bak, name: "Edges", n: bak.Header.EdgeBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &edge{} }}}}}
}

func (s *edgeLBS) newMsg() proto.Message { return &pb.EdgeBatch{} }

func (s *edgeLBS) decode(msg proto.Message) []interface{} {
	edges := msg.(*pb.EdgeBatch)
	rows := make([]interface{}, 0, len(edges.Batch))
	var pe *edge
	for _, e := range edges.Batch {
		pe = s.pools[0].Get().(*edge)
		pe.Id = 0
		pe.Parent = Ino(e.Parent)
		pe.Inode = Ino(e.Inode)
		pe.Name = e.Name
		pe.Type = uint8(e.Type)
		rows = append(rows, pe)
	}
	return rows
}

func (s *edgeLBS) count(msg proto.Message) int { return len(msg.(*pb.EdgeBatch).Batch) }

type nodeLBS struct {
	loadBatchSeg
}

func newNodeLBS(bak *BakFormat) iLoadBatchSeg {
	return &nodeLBS{loadBatchSeg{bak: bak, name: "Nodes", n: bak.Header.NodeBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &node{} }}}}}
}

func (s *nodeLBS) newMsg() proto.Message { return &pb.NodeBatch{} }

func (s *nodeLBS) decode(msg proto.Message) []interface{} {
	nodes := msg.(*pb.NodeBatch)
	rows := make([]interface{}, 0, len(nodes.Batch))
	var pn *node
	for _, n := range nodes.Batch {
		pn = s.pools[0].Get().(*node)
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
	return rows
}

func (s *nodeLBS) count(msg proto.Message) int { return len(msg.(*pb.NodeBatch).Batch) }

type chunkLBS struct {
	loadBatchSeg
}

func newChunkLBS(bak *BakFormat) iLoadBatchSeg {
	return &chunkLBS{
		loadBatchSeg{
			bak:   bak,
			name:  "Chunks",
			n:     bak.Header.ChunkBatchNum,
			pools: []*sync.Pool{{New: func() interface{} { return &chunk{} }}, {New: func() interface{} { return make([]byte, 0) }}},
		},
	}
}

func (s *chunkLBS) newMsg() proto.Message { return &pb.ChunkBatch{} }

func (s *chunkLBS) decode(msg proto.Message) []interface{} {
	chunks := msg.(*pb.ChunkBatch)
	rows := make([]interface{}, 0, len(chunks.Batch))
	var pc *chunk
	for _, c := range chunks.Batch {
		pc = s.pools[0].Get().(*chunk)
		pc.Id = 0
		pc.Inode = Ino(c.Inode)
		pc.Indx = c.Index

		n := len(c.Slices) * sliceBytes
		pc.Slices = s.pools[1].Get().([]byte)[:0]
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
	return rows
}

func (s *chunkLBS) count(msg proto.Message) int { return len(msg.(*pb.ChunkBatch).Batch) }

func (s *chunkLBS) release(rows []interface{}) {
	for _, row := range rows {
		pc := row.(*chunk)
		s.pools[1].Put(pc.Slices)
		pc.Slices = nil
		s.pools[0].Put(pc)
	}
}

type sliceRefLBS struct {
	loadBatchSeg
}

func newSliceRefLBS(bak *BakFormat) iLoadBatchSeg {
	return &sliceRefLBS{loadBatchSeg{bak: bak, name: "SliceRefs", n: bak.Header.SliceRefBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &sliceRef{} }}}}}
}

func (s *sliceRefLBS) newMsg() proto.Message { return &pb.SliceRefBatch{} }

func (s *sliceRefLBS) decode(msg proto.Message) []interface{} {
	sliceRefs := msg.(*pb.SliceRefBatch)
	rows := make([]interface{}, 0, len(sliceRefs.Batch))
	var ps *sliceRef
	for _, sr := range sliceRefs.Batch {
		ps = s.pools[0].Get().(*sliceRef)
		ps.Id = sr.Id
		ps.Size = sr.Size
		ps.Refs = int(sr.Refs)
		rows = append(rows, ps)
	}
	return rows
}

func (s *sliceRefLBS) count(msg proto.Message) int { return len(msg.(*pb.SliceRefBatch).Batch) }

type symlinkLBS struct {
	loadBatchSeg
}

func newSymLinkLBS(bak *BakFormat) iLoadBatchSeg {
	return &symlinkLBS{loadBatchSeg{bak: bak, name: "Symlinks", n: bak.Header.SymlinkBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &symlink{} }}}}}
}

func (s *symlinkLBS) newMsg() proto.Message { return &pb.SymlinkBatch{} }

func (s *symlinkLBS) decode(msg proto.Message) []interface{} {
	symlinks := msg.(*pb.SymlinkBatch)
	rows := make([]interface{}, 0, len(symlinks.Batch))
	var ps *symlink
	for _, sl := range symlinks.Batch {
		ps = s.pools[0].Get().(*symlink)
		ps.Inode = Ino(sl.Inode)
		ps.Target = sl.Target
		rows = append(rows, ps)
	}
	return rows
}

func (s *symlinkLBS) count(msg proto.Message) int { return len(msg.(*pb.SymlinkBatch).Batch) }

type xattrLBS struct {
	loadBatchSeg
}

func newXattrLBS(bak *BakFormat) iLoadBatchSeg {
	return &xattrLBS{loadBatchSeg{bak: bak, name: "Xattrs", n: bak.Header.XattrBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &xattr{} }}}}}
}

func (s *xattrLBS) newMsg() proto.Message { return &pb.XattrBatch{} }

func (s *xattrLBS) decode(msg proto.Message) []interface{} {
	xattrs := msg.(*pb.XattrBatch)
	rows := make([]interface{}, 0, len(xattrs.Batch))
	var px *xattr
	for _, x := range xattrs.Batch {
		px = s.pools[0].Get().(*xattr)
		px.Id = 0
		px.Inode = Ino(x.Inode)
		px.Name = x.Name
		px.Value = x.Value
		rows = append(rows, px)
	}
	return rows
}

func (s *xattrLBS) count(msg proto.Message) int { return len(msg.(*pb.XattrBatch).Batch) }

type quotaLBS struct {
	loadBatchSeg
}

func newQuotaLBS(bak *BakFormat) iLoadBatchSeg {
	return &quotaLBS{loadBatchSeg{bak: bak, name: "Quotas", n: bak.Header.QuotaBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &dirQuota{} }}}}}
}

func (s *quotaLBS) newMsg() proto.Message { return &pb.QuotaBatch{} }

func (s *quotaLBS) decode(msg proto.Message) []interface{} {
	quotas := msg.(*pb.QuotaBatch)
	rows := make([]interface{}, 0, len(quotas.Batch))
	var pq *dirQuota
	for _, q := range quotas.Batch {
		pq = s.pools[0].Get().(*dirQuota)
		pq.Inode = Ino(q.Inode)
		pq.MaxSpace = q.MaxSpace
		pq.MaxInodes = q.MaxInodes
		pq.UsedSpace = q.UsedSpace
		pq.UsedInodes = q.UsedInodes
		rows = append(rows, pq)
	}
	return rows
}

func (s *quotaLBS) count(msg proto.Message) int { return len(msg.(*pb.QuotaBatch).Batch) }

type statLBS struct {
	loadBatchSeg
}

func newStatLBS(bak *BakFormat) iLoadBatchSeg {
	return &statLBS{loadBatchSeg{bak: bak, name: "Stats", n: bak.Header.StatBatchNum, pools: []*sync.Pool{{New: func() interface{} { return &dirStats{} }}}}}
}

func (s *statLBS) newMsg() proto.Message { return &pb.StatBatch{} }

func (s *statLBS) decode(msg proto.Message) []interface{} {
	stats := msg.(*pb.StatBatch)
	rows := make([]interface{}, 0, len(stats.Batch))
	var ps *dirStats
	for _, st := range stats.Batch {
		ps = s.pools[0].Get().(*dirStats)
		ps.Inode = Ino(st.Inode)
		ps.DataLength = st.DataLength
		ps.UsedInodes = st.UsedInodes
		ps.UsedSpace = st.UsedSpace
		rows = append(rows, ps)
	}
	return rows
}

func (s *statLBS) count(msg proto.Message) int { return len(msg.(*pb.StatBatch).Batch) }

func (m *dbMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	opt = opt.check()
	if err := m.checkAddr(); err != nil {
		return err
	}
	if err := m.syncAllTables(); err != nil {
		return err
	}

	bak := newBakFormat()
	if err := bak.readHeader(r); err != nil {
		return err
	}

	insert := func(beans []interface{}) error {
		return m.txn(func(s *xorm.Session) error {
			n, err := s.Insert(beans...)
			if err == nil && int(n) != len(beans) {
				err = fmt.Errorf("only %d records inserted", n)
			}
			return err
		})
	}

	type task struct {
		msg proto.Message
		seg segDecoder
	}

	var wg sync.WaitGroup
	batch := m.getTxnBatchNum()
	simTaskCh := make(chan *task, 6)
	batchTaskCh := make(chan *task, 100)

	workerFunc := func(ctx Context, batchSize int, taskCh <-chan *task) {
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
				if task.msg == nil {
					continue
				}
				rows, bs := task.seg.decode(task.msg), batchSize
				for len(rows) > 0 {
					if len(rows) < bs {
						bs = len(rows)
					}
					if err := insert(rows[:bs]); err != nil {
						// TODO hjf
						for _, row := range rows[:bs] {
							logger.Errorf("Write bean: %+v", row)
						}

						logger.Errorf("Write %d beans: %s", len(rows), err)
						ctx.Cancel()
						return
					}
					task.seg.release(rows[:bs])
					rows = rows[bs:]
				}
			}
		}
	}

	wg.Add(1)
	go workerFunc(ctx, batch, simTaskCh)

	for i := 0; i < opt.CoNum; i++ {
		wg.Add(1)
		go workerFunc(ctx, batch, batchTaskCh)
	}

	var msg proto.Message
	var err error
	for _, seg := range []iLoadSeg{
		newFormatLS(bak),
		newCounterLS(bak),
		newSustainedLS(bak),
		newDelFileLS(bak),
		newAclLS(bak),
	} {
		msg = seg.newMsg()
		if err := seg.read(r, seg.newMsg()); err != nil {
			ctx.Cancel()
			wg.Wait()
			return err
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case simTaskCh <- &task{msg, seg}:
		}
	}
	close(simTaskCh)

	for _, seg := range []iLoadBatchSeg{
		newNodeLBS(bak),
		newChunkLBS(bak),
		newEdgeLBS(bak),
		newSliceRefLBS(bak),
		newSymLinkLBS(bak),
		newXattrLBS(bak),
		newQuotaLBS(bak),
		newStatLBS(bak),
	} {
		sum := int64(0)
		for i := 0; i < seg.num(); i++ {
			msg = seg.newMsg()
			if err = seg.read(r, i+1, msg); err != nil {
				ctx.Cancel()
				wg.Wait()
				return err
			}
			select {
			case <-ctx.Done():
				wg.Wait()
				return ctx.Err()
			case batchTaskCh <- &task{msg, seg}:
			}
			atomic.AddInt64(&sum, int64(seg.count(msg)))
		}
		logger.Infof("load %s total num %d", seg, sum) // TODO hjf debug
	}
	close(batchTaskCh)

	wg.Wait()
	return nil
}
