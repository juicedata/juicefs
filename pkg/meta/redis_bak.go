package meta

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

var (
	redisBatchSize = 10000
)

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
	case SegTypeSliceRef:
		return &redisSliceRefDS{dumpedSeg{typ: typ, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.SliceRef{} }}}}
	case SegTypeAcl:
		return &redisAclDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeXattr:
		return &redisXattrDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeQuota:
		return &redisQuotaDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeStat:
		return &redisStatDS{dumpedSeg{typ: typ, meta: m}}
	case SegTypeNode:
		return &redisNodeDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeNode, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Node{} }}}}}
	case SegTypeChunk:
		return &redisChunkDBS{
			dumpedBatchSeg{dumpedSeg{typ: SegTypeChunk, meta: m},
				[]*sync.Pool{{New: func() interface{} { return &pb.Chunk{} }}, {New: func() interface{} { return &pb.Slice{} }}}},
		}
	case SegTypeEdge:
		return &redisEdgeDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeEdge, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Edge{} }}}}}
	case SegTypeParent:
		return &redisParentDBS{dumpedSeg{typ: SegTypeParent, meta: m}}
	case SegTypeSymlink:
		return &redisSymlinkDBS{dumpedBatchSeg{dumpedSeg{typ: SegTypeSymlink, meta: m}, []*sync.Pool{{New: func() interface{} { return &pb.Symlink{} }}}}}
	}
	return nil
}

func (m *redisMeta) buildLoadedPools(typ int) []*sync.Pool {
	loadedPoolOnce.Do(func() {
		loadedPools = map[int][]*sync.Pool{
			SegTypeNode: {{New: func() interface{} { return make([]byte, BakNodeSizeWithoutAcl) }}, {New: func() interface{} { return make([]byte, BakNodeSize) }}},
		}
	})
	return loadedPools[typ]
}

func (m *redisMeta) BuildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	switch typ {
	case SegTypeFormat:
		return &redisFormatLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeCounter:
		return &redisCounterLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSustained:
		return &redisSustainedLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeDelFile:
		return &redisDelFileLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSliceRef:
		return &redisSliceRefLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeAcl:
		return &redisAclLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeXattr:
		return &redisXattrLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeQuota:
		return &redisQuotaLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeStat:
		return &redisStatLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeNode:
		return &redisNodeLS{loadedSeg{typ: typ, meta: m}, m.buildLoadedPools(typ)}
	case SegTypeChunk:
		return &redisChunkLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeEdge:
		return &redisEdgeLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeParent:
		return &redisParentLS{loadedSeg{typ: typ, meta: m}}
	case SegTypeSymlink:
		return &redisSymlinkLS{loadedSeg{typ: typ, meta: m}}
	}
	return nil
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

func execPipe(ctx context.Context, pipe redis.Pipeliner) error {
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		for i, cmd := range cmds {
			if cmd.Err() != nil {
				return fmt.Errorf("failed command %d %+v: %w", i, cmd, cmd.Err())
			}
		}
	}
	return err
}

func tryExecPipe(ctx context.Context, pipe redis.Pipeliner) error {
	if pipe.Len() < redisBatchSize {
		return nil
	}
	return execPipe(ctx, pipe)
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
				cnt++ // Redis nextInode/nextChunk is one smaller than sql/tkv
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
			if val >= 1 {
				ps := strings.Split(key, "_")
				if len(ps) == 2 {
					id, _ := strconv.ParseUint(ps[0][1:], 10, 64)
					size, _ := strconv.ParseUint(ps[1], 10, 32)
					sl := &pb.SliceRef{Id: id, Size: uint32(size), Refs: int64(val) + 1} // Redis sliceRef is one smaller than sql
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

type redisXattrDS struct {
	dumpedSeg
}

func (s *redisXattrDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	xattrs := &pb.XattrList{List: make([]*pb.Xattr, 0, 128)}
	if err := meta.scan(ctx, "x*", func(keys []string) error {
		pipe := meta.rdb.Pipeline()
		for _, key := range keys {
			pipe.HGetAll(ctx, key)
		}
		cmds, err := pipe.Exec(ctx)
		if err != nil {
			return err
		}

		for idx, cmd := range cmds {
			inode, _ := strconv.ParseUint(keys[idx][len(meta.prefix)+1:], 10, 64)
			res, err := cmd.(*redis.MapStringStringCmd).Result()
			if err != nil {
				return err
			}

			if len(res) > 0 {
				for k, v := range res {
					xattrs.List = append(xattrs.List, &pb.Xattr{
						Inode: uint64(inode),
						Name:  k,
						Value: []byte(v),
					})
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, xattrs}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(xattrs.List))
	return nil
}

type redisQuotaDS struct {
	dumpedSeg
}

func (s *redisQuotaDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)

	quotas := make(map[Ino]*pb.Quota)
	vals, err := meta.rdb.HGetAll(ctx, meta.dirQuotaKey()).Result()
	if err != nil {
		return fmt.Errorf("get dirQuotaKey err: %w", err)
	}
	for k, v := range vals {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse quota inode: %s: %v", k, err)
			continue
		}
		if len(v) != 16 {
			logger.Warnf("invalid quota string: %s", hex.EncodeToString([]byte(v)))
			continue
		}
		space, inodes := meta.parseQuota([]byte(v))
		quotas[Ino(inode)] = &pb.Quota{
			Inode:     inode,
			MaxSpace:  space,
			MaxInodes: inodes,
		}
	}

	vals, err = meta.rdb.HGetAll(ctx, meta.dirQuotaUsedInodesKey()).Result()
	if err != nil {
		return fmt.Errorf("get dirQuotaUsedInodesKey err: %w", err)
	}
	for k, v := range vals {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse used inodes inode: %s: %v", k, err)
			continue
		}
		if q, ok := quotas[Ino(inode)]; !ok {
			logger.Warnf("quota for used inodes not found: %d", inode)
		} else {
			q.UsedInodes, _ = strconv.ParseInt(v, 10, 64)
		}
	}

	vals, err = meta.rdb.HGetAll(ctx, meta.dirQuotaUsedSpaceKey()).Result()
	if err != nil {
		return fmt.Errorf("get dirQuotaUsedSpaceKey err: %w", err)
	}
	for k, v := range vals {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse used space inode: %s: %v", k, err)
			continue
		}
		if q, ok := quotas[Ino(inode)]; !ok {
			logger.Warnf("quota for used space not found: %d", inode)
		} else {
			q.UsedSpace, _ = strconv.ParseInt(v, 10, 64)
		}
	}

	pqs := &pb.QuotaList{List: make([]*pb.Quota, 0, len(quotas))}
	for _, q := range quotas {
		pqs.List = append(pqs.List, q)
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, pqs}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(pqs.List))
	return nil
}

type redisStatDS struct {
	dumpedSeg
}

func (s *redisStatDS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)

	stats := make(map[Ino]*pb.Stat)
	vals, err := meta.rdb.HGetAll(ctx, meta.dirDataLengthKey()).Result()
	if err != nil {
		return fmt.Errorf("get dirDataLengthKey err: %w", err)
	}
	for k, v := range vals {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse length stat inode: %s: %v", k, err)
			continue
		}
		length, _ := strconv.ParseInt(v, 10, 64)
		stats[Ino(inode)] = &pb.Stat{
			Inode:      inode,
			DataLength: length,
		}
	}

	vals, err = meta.rdb.HGetAll(ctx, meta.dirUsedInodesKey()).Result()
	if err != nil {
		return fmt.Errorf("get dirUsedInodesKey err: %w", err)
	}
	for k, v := range vals {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse inodes stat inode: %s: %v", k, err)
			continue
		}
		inodes, _ := strconv.ParseInt(v, 10, 64)
		if q, ok := stats[Ino(inode)]; !ok {
			logger.Warnf("stat for used inodes not found: %d", inode)
		} else {
			q.UsedInodes = inodes
		}
	}

	vals, err = meta.rdb.HGetAll(ctx, meta.dirUsedSpaceKey()).Result()
	if err != nil {
		return fmt.Errorf("get dirUsedSpaceKey err: %w", err)
	}
	for k, v := range vals {
		inode, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			logger.Warnf("parse space stat inode: %s: %v", k, err)
			continue
		}
		space, _ := strconv.ParseInt(v, 10, 64)
		if q, ok := stats[Ino(inode)]; !ok {
			logger.Warnf("stat for used space not found: %d", inode)
		} else {
			q.UsedSpace = space
		}
	}

	pss := &pb.StatList{List: make([]*pb.Stat, 0, len(stats))}
	for _, q := range stats {
		pss.List = append(pss.List, q)
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, pss}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(pss.List))
	return nil
}

type redisNodeDBS struct {
	dumpedBatchSeg
}

func (s *redisNodeDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)
	var sum int64

	_ = meta.scan(nCtx, "i*", func(iKeys []string) error {
		tKeys := iKeys
		eg.Go(func() error {
			vals, err := meta.rdb.MGet(nCtx, tKeys...).Result()
			if err != nil {
				return err
			}
			pnb := &pb.NodeList{
				List: make([]*pb.Node, 0, len(vals)),
			}
			var inode uint64
			for idx, v := range vals {
				if v == nil {
					continue
				}
				inode, _ = strconv.ParseUint(tKeys[idx][len(meta.prefix)+1:], 10, 64)
				node := s.pools[0].Get().(*pb.Node)
				rb := utils.FromBuffer([]byte(v.(string)))
				node.Inode = inode
				node.Flags = uint32(rb.Get8())
				node.Mode = uint32(rb.Get16())
				node.Type = node.Mode >> 12
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
				if rb.Left() >= 8 {
					node.Parent = rb.Get64()
				}
				if rb.Left() >= 8 {
					node.AccessAclId = rb.Get32()
					node.DefaultAclId = rb.Get32()
				}
				pnb.List = append(pnb.List, node)
			}
			atomic.AddInt64(&sum, int64(len(pnb.List)))
			return dumpResult(nCtx, ch, &dumpedResult{s, pnb})
		})
		return nil
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	logger.Debugf("dump %s total num %d", s, sum)
	return nil
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
	eg.SetLimit(opt.CoNum)
	var sum int64

	cPool := &sync.Pool{
		New: func() any { return new(chk) },
	}

	_ = meta.scan(nCtx, "c*", func(cKeys []string) error {
		pipe := meta.rdb.Pipeline()
		chks := make([]*chk, 0, redisBatchSize)

		for _, cKey := range cKeys {
			ps := strings.Split(cKey, "_")
			if len(ps) != 2 {
				continue
			}
			ino, _ := strconv.ParseUint(ps[0][len(meta.prefix)+1:], 10, 64)
			idx, _ := strconv.ParseUint(ps[1], 10, 32)
			pipe.LRange(ctx, meta.chunkKey(Ino(ino), uint32(idx)), 0, -1)
			c := cPool.Get().(*chk)
			c.inode, c.index = ino, uint32(idx)
			chks = append(chks, c)
		}

		tPipe, tChks := pipe, chks
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
				pc.Inode = tChks[k].inode
				pc.Index = tChks[k].index
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
		return nil
	})

	if err := eg.Wait(); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, sum)
	return nil
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
	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)
	var sum int64

	// TODO merge small dirs?
	_ = meta.scan(nCtx, "d[0-9]*", func(pKeys []string) error {
		for _, pKey := range pKeys {
			parent, _ := strconv.ParseUint(pKey[len(meta.prefix)+1:], 10, 64)
			eg.Go(func() error {
				pes := &pb.EdgeList{
					List: make([]*pb.Edge, 0, redisBatchSize),
				}
				var pe *pb.Edge
				err := meta.hscan(nCtx, meta.entryKey(Ino(parent)), func(keys []string) error {
					for i := 0; i < len(keys); i += 2 {
						pe = s.pools[0].Get().(*pb.Edge)
						pe.Parent = parent
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
		return nil
	})

	if err := eg.Wait(); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, sum)
	return nil
}

func (s *redisEdgeDBS) release(msg proto.Message) {
	pes := msg.(*pb.EdgeList)
	for _, pe := range pes.List {
		s.pools[0].Put(pe)
	}
	pes.List = nil
}

type redisParentDBS struct {
	dumpedSeg
}

func (s *redisParentDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	// TODO: optimize?
	meta := s.meta.(*redisMeta)
	pls := &pb.ParentList{
		List: make([]*pb.Parent, 0, 1024),
	}
	if err := meta.scan(ctx, "p*", func(keys []string) error {
		pipe := meta.rdb.Pipeline()
		for _, key := range keys {
			pipe.HGetAll(ctx, key)
		}
		cmds, err := pipe.Exec(ctx)
		if err != nil {
			return err
		}

		for idx, cmd := range cmds {
			inode, _ := strconv.ParseUint(keys[idx][len(meta.prefix)+1:], 10, 64)
			res, err := cmd.(*redis.MapStringStringCmd).Result()
			if err != nil {
				return err
			}

			if len(res) > 0 {
				for k, v := range res {
					parent, _ := strconv.ParseUint(k, 10, 64)
					cnt, _ := strconv.ParseInt(v, 10, 64)
					pls.List = append(pls.List, &pb.Parent{
						Inode:  inode,
						Parent: parent,
						Cnt:    cnt,
					})
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, pls}); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, len(pls.List))
	return nil
}

type redisSymlinkDBS struct {
	dumpedBatchSeg
}

func (s *redisSymlinkDBS) query(ctx Context, opt *DumpOption, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	eg, nCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.CoNum)
	var sum int64

	_ = meta.scan(nCtx, "s[0-9]*", func(sKeys []string) error {
		tKeys := sKeys
		eg.Go(func() error {
			vals, err := meta.rdb.MGet(nCtx, tKeys...).Result()
			if err != nil {
				return err
			}
			pss := &pb.SymlinkList{
				List: make([]*pb.Symlink, 0, len(vals)),
			}
			var ps *pb.Symlink
			for idx, v := range vals {
				ps = s.pools[0].Get().(*pb.Symlink)
				ps.Inode, _ = strconv.ParseUint(tKeys[idx][len(meta.prefix)+1:], 10, 64)
				ps.Target = unescape(v.(string))
				pss.List = append(pss.List, ps)
			}
			atomic.AddInt64(&sum, int64(len(pss.List)))
			return dumpResult(nCtx, ch, &dumpedResult{s, pss})
		})
		return nil
	})

	if err := eg.Wait(); err != nil {
		return err
	}
	logger.Debugf("dump %s total num %d", s, sum)
	return nil
}

func (s *redisSymlinkDBS) release(msg proto.Message) {
	pss := msg.(*pb.SymlinkList)
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

type redisFormatLS struct {
	loadedSeg
}

func (s *redisFormatLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	format := UnmarshalFormatPB(msg.(*pb.Format))
	fData, _ := json.MarshalIndent(*format, "", "")
	return meta.rdb.Set(ctx, meta.setting(), fData, 0).Err()
}

type redisCounterLS struct {
	loadedSeg
}

func (s *redisCounterLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	cs := make(map[string]interface{})
	for k, v := range getRedisCounterFields(meta.prefix, msg.(*pb.Counters)) {
		if k == meta.prefix+"nextinode" || k == meta.prefix+"nextchunk" {
			cs[k] = *v - 1
		} else {
			cs[k] = *v
		}
	}
	return meta.rdb.MSet(ctx, cs).Err()
}

type redisSustainedLS struct {
	loadedSeg
}

func (s *redisSustainedLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pss := msg.(*pb.SustainedList)
	pipe := meta.rdb.Pipeline()
	for _, ps := range pss.List {
		inodes := make([]interface{}, len(ps.Inodes))
		for i, inode := range ps.Inodes {
			inodes[i] = inode
		}
		pipe.SAdd(ctx, meta.sustained(ps.Sid), inodes...)
	}
	return execPipe(ctx, pipe)
}

type redisDelFileLS struct {
	loadedSeg
}

func (s *redisDelFileLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pds := msg.(*pb.DelFileList)
	mbs := make([]redis.Z, 0, len(pds.List))
	for _, pd := range pds.List {
		mbs = append(mbs, redis.Z{
			Score:  float64(pd.Expire),
			Member: meta.toDelete(Ino(pd.Inode), pd.Length),
		})
	}
	return meta.rdb.ZAdd(ctx, meta.delfiles(), mbs...).Err()
}

type redisSliceRefLS struct {
	loadedSeg
}

func (s *redisSliceRefLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	ps := msg.(*pb.SliceRefList)

	slices := make(map[string]interface{})
	for _, p := range ps.List {
		slices[meta.sliceKey(p.Id, p.Size)] = strconv.Itoa(int(p.Refs - 1))
	}
	return meta.rdb.HSet(ctx, meta.sliceRefs(), slices).Err()
}

type redisAclLS struct {
	loadedSeg
}

func (s *redisAclLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pas := msg.(*pb.AclList)

	var maxId uint32 = 0
	acls := make(map[string]interface{}, len(pas.List))
	for _, pa := range pas.List {
		if pa.Id > maxId {
			maxId = pa.Id
		}
		acls[strconv.FormatUint(uint64(pa.Id), 10)] = UnmarshalAclPB(pa)
	}
	if err := meta.rdb.HSet(ctx, meta.aclKey(), acls).Err(); err != nil {
		return err
	}
	return meta.rdb.Set(ctx, meta.prefix+aclCounter, maxId, 0).Err()
}

type redisXattrLS struct {
	loadedSeg
}

func (s *redisXattrLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pxs := msg.(*pb.XattrList)
	pipe := meta.rdb.Pipeline()

	xm := make(map[uint64]map[string]interface{}) // {inode: {name: value}}
	for _, px := range pxs.List {
		if _, ok := xm[px.Inode]; !ok {
			xm[px.Inode] = make(map[string]interface{})
		}
		xm[px.Inode][px.Name] = px.Value
	}

	for inode, xattrs := range xm {
		pipe.HSet(ctx, meta.xattrKey(Ino(inode)), xattrs)
	}
	return execPipe(ctx, pipe)
}

type redisQuotaLS struct {
	loadedSeg
}

func (s *redisQuotaLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pqs := msg.(*pb.QuotaList)
	pipe := meta.rdb.Pipeline()

	var inodeKey string
	for _, pq := range pqs.List {
		inodeKey = Ino(pq.Inode).String()
		pipe.HSet(ctx, meta.dirQuotaKey(), inodeKey, meta.packQuota(pq.MaxSpace, pq.MaxInodes))
		pipe.HSet(ctx, meta.dirQuotaUsedInodesKey(), inodeKey, pq.UsedInodes)
		pipe.HSet(ctx, meta.dirQuotaUsedSpaceKey(), inodeKey, pq.UsedSpace)
	}
	return execPipe(ctx, pipe)
}

type redisStatLS struct {
	loadedSeg
}

func (s *redisStatLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pss := msg.(*pb.StatList)
	pipe := meta.rdb.Pipeline()

	var inodeKey string
	for _, ps := range pss.List {
		inodeKey = Ino(ps.Inode).String()
		pipe.HSet(ctx, meta.dirDataLengthKey(), inodeKey, ps.DataLength)
		pipe.HSet(ctx, meta.dirUsedInodesKey(), inodeKey, ps.UsedInodes)
		pipe.HSet(ctx, meta.dirUsedSpaceKey(), inodeKey, ps.UsedSpace)
	}
	return execPipe(ctx, pipe)
}

type redisNodeLS struct {
	loadedSeg
	pools []*sync.Pool
}

func (s *redisNodeLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pns := msg.(*pb.NodeList)
	nodes := make(map[string]interface{}, redisBatchSize)
	for _, pn := range pns.List {
		nodes[meta.inodeKey(Ino(pn.Inode))] = UnmarshalNodePB(pn, s.pools[0], s.pools[1])

		if len(nodes) >= redisBatchSize {
			if err := meta.rdb.MSet(ctx, nodes).Err(); err != nil {
				return err
			}
			for k := range nodes {
				delete(nodes, k)
			}
		}
	}
	return meta.rdb.MSet(ctx, nodes).Err()
}

type redisChunkLS struct {
	loadedSeg
}

func (s *redisChunkLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pcs := msg.(*pb.ChunkList)

	pipe := meta.rdb.Pipeline()
	for idx, chk := range pcs.List {
		slices := make([]string, 0, len(chk.Slices))
		for _, slice := range chk.Slices {
			slices = append(slices, string(marshalSlice(slice.Pos, slice.Id, slice.Size, slice.Off, slice.Len)))
		}
		pipe.RPush(ctx, meta.chunkKey(Ino(chk.Inode), chk.Index), slices)

		if idx%100 == 0 {
			if err := tryExecPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

type redisEdgeLS struct {
	loadedSeg
}

func (s *redisEdgeLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pes := msg.(*pb.EdgeList)
	pipe := meta.rdb.Pipeline()
	for idx, pe := range pes.List {
		pipe.HSet(ctx, meta.entryKey(Ino(pe.Parent)), pe.Name, meta.packEntry(uint8(pe.Type), Ino(pe.Inode)))
		if idx%100 == 0 {
			if err := tryExecPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

type redisParentLS struct {
	loadedSeg
}

func (s *redisParentLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pls := msg.(*pb.ParentList)
	pipe := meta.rdb.Pipeline()
	for _, p := range pls.List {
		pipe.HIncrBy(ctx, meta.parentKey(Ino(p.Inode)), Ino(p.Parent).String(), p.Cnt)
	}
	return execPipe(ctx, pipe)
}

type redisSymlinkLS struct {
	loadedSeg
}

func (s *redisSymlinkLS) insert(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pss := msg.(*pb.SymlinkList)

	syms := make(map[string]interface{}, redisBatchSize)
	for _, ps := range pss.List {
		syms[meta.symKey(Ino(ps.Inode))] = ps.Target

		if len(syms) >= redisBatchSize {
			if err := meta.rdb.MSet(ctx, syms).Err(); err != nil {
				return err
			}
			for k := range syms {
				delete(syms, k)
			}
		}
	}
	return meta.rdb.MSet(ctx, syms).Err()
}

func (m *redisMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	opt = opt.check()

	// TODO engine check
	if _, ok := m.rdb.(*redis.ClusterClient); ok {
		err := m.scan(ctx, "*", func(keys []string) error {
			return fmt.Errorf("found key with same prefix: %s", keys[0])
		})
		if err != nil {
			return err
		}
	} else {
		dbsize, err := m.rdb.DBSize(ctx).Result()
		if err != nil {
			return err
		}
		if dbsize > 0 {
			return fmt.Errorf("Database redis://%s is not empty", m.addr)
		}
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
