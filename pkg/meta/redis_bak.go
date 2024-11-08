package meta

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

const redisBatchSize = 10000

func (m *redisMeta) BuildDumpedSeg(typ int, opt *DumpOption) iDumpedSeg {
	switch typ {
	case SegTypeFormat:
		return &formatDS{dumpedSeg{typ: typ}, m.getFormat(), opt.KeepSecret}
	case SegTypeCounter:
		return &redisCounterDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeSustained:
		return &redisSustainedDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeDelFile:
		return &redisDelFileDS{dumpedSeg{typ: typ, meta: m}}
		// case SegTypeSliceRef:
		// 	return &sqlSliceRefDS{dumpedSeg{typ: typ, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.SliceRef{} }}}}
		// case SegTypeAcl:
		// 	return &sqlAclDS{dumpedSeg{typ: typ, meta: m}}
		// case SegTypeXattr:
		// 	return &sqlXattrDS{dumpedSeg{typ: typ, meta: m}}
		// case SegTypeQuota:
		// 	return &sqlQuotaDS{dumpedSeg{typ: typ, meta: m}}
		// case SegTypeStat:
		// 	return &sqlStatDS{dumpedSeg{typ: typ, meta: m}}
		// case SegTypeNode:
		// 	return &sqlNodeDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeNode, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Node{} }}}}}
		// case SegTypeChunk:
		// 	return &sqlChunkDBS{
		// 		dumpedBatchSeg{dumpedSeg{typ: SegTypeChunk, meta: m},
		// 			[]*sync.Pool{{New: func() interface{} { return &pb.Chunk{} }}, {New: func() interface{} { return &pb.Slice{} }}}},
		// 	}
		// case SegTypeEdge:
		// 	return &sqlEdgeDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeEdge, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Edge{} }}}}}
		// case SegTypeSymlink:
		// 	return &sqlSymlinkDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeSymlink, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Symlink{} }}}}}
	}
	return nil
}

func (m *redisMeta) BuildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	panic("implement me")
}

func getRedisCounterFields(prefix string, c *pb.Counters) map[string]*int64 {
	return map[string]*int64{
		prefix + usedSpace:     &c.UsedSpace,
		prefix + totalInodes:   &c.UsedInodes,
		prefix + "nextinode":   &c.NextInode,
		prefix + "nextchunk":   &c.NextChunk,
		prefix + "nextsession": &c.NextSession,
		prefix + "nexttrash":   &c.NextTrash,
	}
}

type redisCounterDS struct {
	dumpedSeg
}

func (s *redisCounterDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	counters := &pb.Counters{}
	fieldMap := getRedisCounterFields(meta.prefix, counters)

	prefixNames := make([]string, 0, len(fieldMap))
	for name := range fieldMap {
		prefixNames = append(prefixNames, name)
	}
	rs, err := meta.rdb.MGet(ctx, prefixNames...).Result()
	if err != nil {
		return err
	}

	var cnt int64
	for i, r := range rs {
		if r != nil {
			cnt, _ = strconv.ParseInt(r.(string), 10, 64)
			if prefixNames[i] == "nextinode" || prefixNames[i] == "nextchunk" {
				cnt++ // Redis nextInode/nextChunk is 1 smaller than sql/tkv
				ctx.WithValue(prefixNames[i], cnt)
			}
			*(fieldMap[prefixNames[i]]) = cnt
		}
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, counters}); err != nil {
		return err
	}
	logger.Debugf("dump %s result %+v", s, counters)
	return nil
}

type redisSustainedDS struct {
	dumpedSeg
}

func (s *redisSustainedDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	keys, err := meta.rdb.ZRange(ctx, meta.allSessions(), 0, -1).Result()
	if err != nil {
		return err
	}

	pss := &pb.SustainedList{
		List: make([]*pb.Sustained, 0, len(keys)),
	}
	for _, k := range keys {
		sid, _ := strconv.ParseUint(k, 10, 64)
		var ss []string
		ss, err = meta.rdb.SMembers(ctx, meta.sustained(sid)).Result()
		if err != nil {
			return err
		}
		if len(ss) > 0 {
			inodes := make([]uint64, 0, len(ss))
			for _, s := range ss {
				inode, _ := strconv.ParseUint(s, 10, 64)
				inodes = append(inodes, inode)
			}
			pss.List = append(pss.List, &pb.Sustained{Sid: sid, Inodes: inodes})
		}
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, pss}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(pss.List))
	return nil
}

type redisDelFileDS struct {
	dumpedSeg
}

func (s *redisDelFileDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	zs, err := meta.rdb.ZRangeWithScores(ctx, meta.delfiles(), 0, -1).Result()
	if err != nil {
		return err
	}

	delFiles := &pb.DelFileList{List: make([]*pb.DelFile, 0, len(zs))}
	for _, z := range zs {
		parts := strings.Split(z.Member.(string), ":")
		if len(parts) != 2 {
			logger.Warnf("invalid delfile string: %s", z.Member.(string))
			continue
		}
		inode, _ := strconv.ParseUint(parts[0], 10, 64)
		length, _ := strconv.ParseUint(parts[1], 10, 64)
		delFiles.List = append(delFiles.List, &pb.DelFile{Inode: inode, Length: length, Expire: int64(z.Score)})
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, delFiles}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(delFiles.List))
	return nil
}

type redisSliceRefDS struct {
	dumpedSeg
	pools []*sync.Pool
}

func (s *redisSliceRefDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	sls := &pb.SliceRefList{List: make([]*pb.SliceRef, 0, 1024)}
	var key string
	var val int
	var inErr error
	if err := meta.hscan(ctx, meta.sliceRefs(), func(keys []string) error {
		for i := 0; i < len(keys); i += 2 {
			key = keys[i]
			val, inErr = strconv.Atoi(keys[i+1])
			if inErr != nil {
				logger.Errorf("invalid value: %s", keys[i+1])
				continue
			}
			if val > 1 {
				ps := strings.Split(key, "_")
				if len(ps) == 2 {
					id, _ := strconv.ParseUint(ps[0][1:], 10, 64)
					size, _ := strconv.ParseUint(ps[1], 10, 32)
					sl := &pb.SliceRef{Id: id, Size: uint32(size), Refs: int64(val)}
					sls.List = append(sls.List, sl)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := dumpResult(ctx, ch, &dumpedResult{s, sls}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(sls.List))
	return nil
}

type redisAclDS struct {
	dumpedSeg
}

func (s *redisAclDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	vals, err := meta.rdb.HGetAll(ctx, meta.aclKey()).Result()
	if err != nil {
		return err
	}

	acls := &pb.AclList{List: make([]*pb.Acl, 0, len(vals))}
	for k, v := range vals {
		id, _ := strconv.ParseUint(k, 10, 32)
		acl := &pb.Acl{
			Id: uint32(id),
		}

		rb := utils.ReadBuffer([]byte(v))
		acl.Owner = uint32(rb.Get16())
		acl.Group = uint32(rb.Get16())
		acl.Mask = uint32(rb.Get16())
		acl.Other = uint32(rb.Get16())

		uCnt := rb.Get32()
		acl.Users = make([]*pb.AclEntry, 0, uCnt)
		for i := 0; i < int(uCnt); i++ {
			acl.Users = append(acl.Users, &pb.AclEntry{
				Id:   rb.Get32(),
				Perm: uint32(rb.Get16()),
			})
		}

		gCnt := rb.Get32()
		acl.Groups = make([]*pb.AclEntry, 0, gCnt)
		for i := 0; i < int(gCnt); i++ {
			acl.Groups = append(acl.Groups, &pb.AclEntry{
				Id:   rb.Get32(),
				Perm: uint32(rb.Get16()),
			})
		}
		acls.List = append(acls.List, acl)
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, acls}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(acls.List))
	return nil
}

type redisNodeDBS struct {
	dumpedBatchSeg
}

func (s *redisNodeDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	maxInode := uint64(ctx.Value("nextinode").(int64))

	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)

	// TODO save intermediate results in redis or kv DB?
	var lock sync.Mutex
	file2Len := make(map[uint64]uint64) // For 1B+ files, cost 8GiB+ memory
	dirs := []uint64{}
	syms := []uint64{}

	var sum int64
	for i := uint64(RootInode); i < maxInode; i += redisBatchSize {
		start, size := i, utils.Min64(redisBatchSize, maxInode-i)
		eg.Go(func() error {
			keys := make([]string, 0, size)
			for j := start; j < start+size; j++ {
				keys = append(keys, meta.inodeKey(Ino(j)))
			}
			vals, err := meta.rdb.MGet(nCtx, keys...).Result()
			if err != nil {
				logger.Errorf("get node batch [%d,%d] err: %v", start, start+size, err)
				ctx.Cancel()
				return err
			}

			pnb := &pb.NodeList{
				List: make([]*pb.Node, 0, len(vals)),
			}
			for k, v := range vals {
				if v == nil {
					continue
				}
				node := s.pools[0].Get().(*pb.Node)
				rb := utils.FromBuffer([]byte(v.(string)))
				node.Inode = uint64(start + uint64(k))
				node.Flags = uint32(rb.Get8())
				node.Mode = uint32(rb.Get16())
				node.Type = node.Mode >> 12

				lock.Lock()
				switch node.Type {
				case TypeFile:
					file2Len[node.Inode] = uint64(len(v.(string)))
				case TypeDirectory:
					dirs = append(dirs, node.Inode)
				case TypeSymlink:
					syms = append(syms, node.Inode)
				}
				lock.Unlock()

				node.Mode &= 0777
				node.Uid = rb.Get32()
				node.Gid = rb.Get32()
				node.Atime = int64(rb.Get64())
				node.AtimeNsec = int32(rb.Get32())
				node.Mtime = int64(rb.Get64())
				node.MtimeNsec = int32(rb.Get32())
				node.Ctime = int64(rb.Get64())
				node.CtimeNsec = int32(rb.Get32())
				node.Nlink = rb.Get32()
				node.Length = rb.Get64()
				node.Rdev = rb.Get32()
				node.Parent = rb.Get64()
				node.AccessAclId = rb.Get32()
				node.DefaultAclId = rb.Get32()
				pnb.List = append(pnb.List, node)
			}
			atomic.AddInt64(&sum, int64(len(pnb.List)))
			return dumpResult(nCtx, ch, &dumpedResult{s, pnb})
		})
	}

	ctx.WithValue("file2Len", file2Len)
	ctx.WithValue("dirs", dirs)
	ctx.WithValue("syms", syms)

	logger.Debugf("dump %s total num %d", s, maxInode)
	return eg.Wait()
}

func (s *redisNodeDBS) release(msg proto.Message) {
	pns := msg.(*pb.NodeList)
	for _, pn := range pns.List {
		s.pools[0].Put(pn)
	}
	pns.List = nil
}

type redisChunkDBS struct {
	dumpedBatchSeg
}

func (s *redisChunkDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	type chk struct {
		inode uint64
		index uint32
	}

	meta := s.meta.(*redisMeta)
	eg, nCtx := errgroup.WithContext(ctx)
	var sum int64
	file2Len := ctx.Value("file2Len").(map[uint64]uint64)

	pipe := meta.rdb.Pipeline()
	cis := make([]*chk, 0, redisBatchSize)
	var cnt uint64
	for inode, length := range file2Len {
		if ctx.Canceled() {
			break
		}
		chkNum := length / ChunkSize
		for idx := uint64(0); idx < chkNum; idx++ {
			cis = append(cis, &chk{inode: inode, index: uint32(idx)})
			pipe.LRange(nCtx, meta.chunkKey(Ino(inode), uint32(idx)), 0, -1)
			cnt++

			if cnt >= redisBatchSize {
				tPipe, tcis := pipe, cis
				eg.Go(func() error {
					cmds, err := tPipe.Exec(nCtx)
					if err != nil {
						logger.Errorf("chunk pipeline exec err: %v", err)
						return err
					}

					pcs := &pb.ChunkList{
						List: make([]*pb.Chunk, 0, len(cmds)),
					}
					for k, cmd := range cmds {
						vals, err := cmd.(*redis.StringSliceCmd).Result()
						if err != nil {
							logger.Errorf("get chunk result err: %v", err)
							return err
						}
						if len(vals) == 0 {
							continue
						}

						pc := s.pools[0].Get().(*pb.Chunk)
						pc.Inode = tcis[k].inode
						pc.Index = tcis[k].index
						var ps *pb.Slice
						for _, val := range vals {
							if len(val) != sliceBytes {
								logger.Errorf("corrupt slice: len=%d, val=%v", len(val), []byte(val))
								continue
							}
							ps = s.pools[1].Get().(*pb.Slice)
							rb := utils.ReadBuffer([]byte(val))
							ps.Pos = rb.Get32()
							ps.Id = rb.Get64()
							ps.Size = rb.Get32()
							ps.Off = rb.Get32()
							ps.Len = rb.Get32()
							pc.Slices = append(pc.Slices, ps)
						}
						pcs.List = append(pcs.List, pc)
					}

					atomic.AddInt64(&sum, int64(len(pcs.List)))
					return dumpResult(nCtx, ch, &dumpedResult{s, pcs})
				})

				pipe = meta.rdb.Pipeline()
				cis = make([]*chk, 0, redisBatchSize)
				cnt = 0
			}
		}
	}
	logger.Debugf("dump %s total num %d", s, sum)
	return eg.Wait()
}

func (s *redisChunkDBS) release(msg proto.Message) {
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

type redisEdgeDBS struct {
	dumpedBatchSeg
}

func (s *redisEdgeDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	dirs := ctx.Value("dirs").([]uint64)
	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)
	var sum int64
	for _, dIno := range dirs {
		inode := dIno
		eg.Go(func() error {
			pes := &pb.EdgeList{
				List: make([]*pb.Edge, 0, redisBatchSize),
			}
			var pe *pb.Edge
			err := meta.hscan(nCtx, meta.entryKey(Ino(inode)), func(keys []string) error {
				for i := 0; i < len(keys); i += 2 {
					pe = s.pools[0].Get().(*pb.Edge)
					pe.Parent = uint64(inode)
					pe.Name = []byte(keys[i])
					typ, ino := meta.parseEntry([]byte(keys[i+1]))
					pe.Type, pe.Inode = uint32(typ), uint64(ino)
					pes.List = append(pes.List, pe)
				}
				return nil
			})
			if err != nil {
				return err
			}
			atomic.AddInt64(&sum, int64(len(pes.List)))
			return dumpResult(nCtx, ch, &dumpedResult{s, pes})
		})
	}

	logger.Debugf("dump %s total num %d", s, sum)
	return eg.Wait()
}

func (s *redisEdgeDBS) release(msg proto.Message) {
	pes := msg.(*pb.EdgeList)
	for _, pe := range pes.List {
		s.pools[0].Put(pe)
	}
	pes.List = nil
}

type redisSymlinkDBS struct {
	dumpedBatchSeg
}

func (s *redisSymlinkDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	syms := ctx.Value("syms").([]uint64)

	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)
	var sum int64
	for li, ri := 0, redisBatchSize; li < len(syms); li, ri = ri, ri+redisBatchSize {
		keys := make([]string, 0, redisBatchSize)
		for i := li; i < utils.Min(ri, len(syms)); i++ {
			keys = append(keys, meta.symKey(Ino(syms[i])))
		}
		eg.Go(func() error {
			vals, err := meta.rdb.MGet(nCtx, keys...).Result()
			if err != nil {
				return err
			}

			pss := &pb.SymlinkList{
				List: make([]*pb.Symlink, 0, len(vals)),
			}
			var ps *pb.Symlink
			for _, v := range vals {
				ps = s.pools[0].Get().(*pb.Symlink)
				ps.Target = []byte(v.(string))
				pss.List = append(pss.List, ps)
			}
			atomic.AddInt64(&sum, int64(len(pss.List)))
			return dumpResult(nCtx, ch, &dumpedResult{s, pss})
		})
	}

	logger.Debugf("dump %s total num %d", s, sum)
	return eg.Wait()
}

func (s *redisSymlinkDBS) release(msg proto.Message) {
	pss := msg.(*pb.SymlinkList)
	for _, ps := range pss.List {
		s.pools[0].Put(ps)
	}
	pss.List = nil
}

type redisXattrDBS struct {
	dumpedBatchSeg
}

func (s *redisXattrDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	syms := ctx.Value("syms").([]uint64)

	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)
	var sum int64
	for li, ri := 0, redisBatchSize; li < len(syms); li, ri = ri, ri+redisBatchSize {
		keys := make([]string, 0, redisBatchSize)
		for i := li; i < utils.Min(ri, len(syms)); i++ {
			keys = append(keys, meta.symKey(Ino(syms[i])))
		}
		eg.Go(func() error {
			vals, err := meta.rdb.MGet(nCtx, keys...).Result()
			if err != nil {
				return err
			}

			pss := &pb.SymlinkList{
				List: make([]*pb.Symlink, 0, len(vals)),
			}
			var ps *pb.Symlink
			for _, v := range vals {
				ps = s.pools[0].Get().(*pb.Symlink)
				ps.Target = []byte(v.(string))
				pss.List = append(pss.List, ps)
			}
			atomic.AddInt64(&sum, int64(len(pss.List)))
			return dumpResult(nCtx, ch, &dumpedResult{s, pss})
		})
	}

	logger.Debugf("dump %s total num %d", s, sum)
	return eg.Wait()
}

func (s *redisXattrDBS) release(msg proto.Message) {
	pss := msg.(*pb.XattrList)
	for _, ps := range pss.List {
		s.pools[0].Put(ps)
	}
	pss.List = nil
}

func (m *redisMeta) DumpMetaV2(ctx Context, w io.Writer, opt *DumpOption) (err error) {
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

		for typ := SegTypeFormat; typ <= SegTypeSymlink; typ++ {
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

func (m *redisMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	return nil
}
