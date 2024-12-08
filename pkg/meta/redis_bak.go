//go:build !noredis
// +build !noredis

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
	"encoding/hex"
	"fmt"
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

var (
	redisBatchSize = 10000
)

func (m *redisMeta) buildDumpedSeg(typ int, opt *DumpOption, txn *eTxn) iDumpedSeg {
	ds := dumpedSeg{typ: typ, meta: m, opt: opt, txn: txn}
	switch typ {
	case SegTypeFormat:
		return &formatDS{ds}
	case SegTypeCounter:
		return &redisCounterDS{ds}
	case SegTypeSustained:
		return &redisSustainedDS{ds}
	case SegTypeDelFile:
		return &redisDelFileDS{ds}
	case SegTypeSliceRef:
		return &redisSliceRefDS{ds}
	case SegTypeAcl:
		return &redisAclDS{ds}
	case SegTypeQuota:
		return &redisQuotaDS{ds}
	case SegTypeStat:
		return &redisStatDS{ds}
	case SegTypeMix:
		return &redisMixDBS{dumpedBatchSeg{ds, []*sync.Pool{
			{New: func() interface{} { return &pb.Node{} }},
			{New: func() interface{} { return &pb.Edge{} }},
			{New: func() interface{} { return &pb.Chunk{} }},
			{New: func() interface{} { return make([]byte, 8*sliceBytes) }},
			{New: func() interface{} { return &pb.Symlink{} }},
			{New: func() interface{} { return &pb.Xattr{} }},
			{New: func() interface{} { return &pb.Parent{} }},
		}}}
	}
	return nil
}

func (m *redisMeta) buildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	ls := loadedSeg{typ: typ, meta: m}
	switch typ {
	case SegTypeFormat:
		return &redisFormatLS{ls}
	case SegTypeCounter:
		return &redisCounterLS{ls}
	case SegTypeSustained:
		return &redisSustainedLS{ls}
	case SegTypeDelFile:
		return &redisDelFileLS{ls}
	case SegTypeSliceRef:
		return &redisSliceRefLS{ls}
	case SegTypeAcl:
		return &redisAclLS{ls}
	case SegTypeXattr:
		return &redisXattrLS{ls}
	case SegTypeQuota:
		return &redisQuotaLS{ls}
	case SegTypeStat:
		return &redisStatLS{ls}
	case SegTypeNode:
		return &redisNodeLS{ls}
	case SegTypeChunk:
		return &redisChunkLS{ls}
	case SegTypeEdge:
		return &redisEdgeLS{ls}
	case SegTypeParent:
		return &redisParentLS{ls}
	case SegTypeSymlink:
		return &redisSymlinkLS{ls}
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

func (m *redisMeta) execETxn(ctx Context, txn *eTxn, f func(Context, *eTxn) error) error {
	txn.opt.notUsed = true
	return f(ctx, txn)
}

func execPipe(ctx context.Context, pipe redis.Pipeliner) error {
	if pipe.Len() == 0 {
		return nil
	}
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

func tryExecPipe(ctx context.Context, pipe redis.Pipeliner) (bool, error) {
	if pipe.Len() < redisBatchSize {
		return false, nil
	}
	return true, execPipe(ctx, pipe)
}

type redisCounterDS struct {
	dumpedSeg
}

func (s *redisCounterDS) dump(ctx Context, ch chan *dumpedResult) error {
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

func (s *redisSustainedDS) dump(ctx Context, ch chan *dumpedResult) error {
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
	logger.Debugf("dump %s num %d", s, len(pss.List))
	return nil
}

type redisDelFileDS struct {
	dumpedSeg
}

func (s *redisDelFileDS) dump(ctx Context, ch chan *dumpedResult) error {
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
	logger.Debugf("dump %s num %d", s, len(delFiles.List))
	return nil
}

type redisSliceRefDS struct {
	dumpedSeg
}

func (s *redisSliceRefDS) dump(ctx Context, ch chan *dumpedResult) error {
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
	logger.Debugf("dump %s num %d", s, len(sls.List))
	return nil
}

type redisAclDS struct {
	dumpedSeg
}

func (s *redisAclDS) dump(ctx Context, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	vals, err := meta.rdb.HGetAll(ctx, meta.aclKey()).Result()
	if err != nil {
		return err
	}

	acls := &pb.AclList{List: make([]*pb.Acl, 0, len(vals))}
	for k, v := range vals {
		id, _ := strconv.ParseUint(k, 10, 32)
		acls.List = append(acls.List, &pb.Acl{
			Id:   uint32(id),
			Data: []byte(v),
		})
	}

	if err := dumpResult(ctx, ch, &dumpedResult{s, acls}); err != nil {
		return err
	}
	logger.Debugf("dump %s num %d", s, len(acls.List))
	return nil
}

type redisQuotaDS struct {
	dumpedSeg
}

func (s *redisQuotaDS) dump(ctx Context, ch chan *dumpedResult) error {
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
	logger.Debugf("dump %s num %d", s, len(pqs.List))
	return nil
}

type redisStatDS struct {
	dumpedSeg
}

func (s *redisStatDS) dump(ctx Context, ch chan *dumpedResult) error {
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
	logger.Debugf("dump %s num %d", s, len(pss.List))
	return nil
}

type redisMixDBS struct {
	dumpedBatchSeg
}

func (s *redisMixDBS) dump(ctx Context, ch chan *dumpedResult) error {
	meta := s.meta.(*redisMeta)
	char2Typ := map[byte]int{
		'i': SegTypeNode,
		'd': SegTypeEdge,
		'c': SegTypeChunk,
		's': SegTypeSymlink,
		'x': SegTypeXattr,
		'p': SegTypeParent,
	}
	typ2Keys := map[int][]string{
		SegTypeNode:    make([]string, 0, redisBatchSize),
		SegTypeEdge:    make([]string, 0, redisBatchSize),
		SegTypeChunk:   make([]string, 0, redisBatchSize),
		SegTypeSymlink: make([]string, 0, redisBatchSize),
		SegTypeXattr:   make([]string, 0, redisBatchSize),
		SegTypeParent:  make([]string, 0, redisBatchSize),
	}
	var sums = map[int]*atomic.Uint64{
		SegTypeNode:    {},
		SegTypeEdge:    {},
		SegTypeChunk:   {},
		SegTypeSymlink: {},
		SegTypeXattr:   {},
		SegTypeParent:  {},
	}
	typ2Handles := map[int]func(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error{
		SegTypeNode:    s.dumpNodes,
		SegTypeEdge:    s.dumpEdges,
		SegTypeChunk:   s.dumpChunks,
		SegTypeSymlink: s.dumpSymlinks,
		SegTypeXattr:   s.dumpXattrs,
		SegTypeParent:  s.dumpParents,
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(s.opt.CoNum)

	keyCh := make(chan []string, s.opt.CoNum*2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var keys []string
		for {
			select {
			case <-ctx.Done():
				return
			case keys = <-keyCh:
			}
			if keys == nil {
				break
			}
			for _, key := range keys {
				if typ, ok := char2Typ[key[len(meta.prefix)]]; ok {
					typ2Keys[typ] = append(typ2Keys[typ], key)
					if len(typ2Keys[typ]) >= redisBatchSize {
						sum, keys := sums[typ], typ2Keys[typ]
						eg.Go(func() error {
							return typ2Handles[typ](ctx, ch, keys, s.pools, sum)
						})
						typ2Keys[typ] = make([]string, 0, redisBatchSize)
					}
				}
			}
		}
		for typ, keys := range typ2Keys {
			if len(keys) > 0 {
				iKeys, iTyp := keys, typ
				eg.Go(func() error {
					return typ2Handles[iTyp](ctx, ch, iKeys, s.pools, sums[iTyp])
				})
			}
		}
	}()

	if err := meta.scan(egCtx, "*", func(sKeys []string) error {
		keyCh <- sKeys
		return nil
	}); err != nil {
		ctx.Cancel()
		wg.Wait()
		eg.Wait()
		return err
	}

	close(keyCh)
	wg.Wait()
	if err := eg.Wait(); err != nil {
		return err
	}

	logger.Debugf("dump %s result: %s", s, printSums(sums))
	return nil
}

func (s *redisMixDBS) release(msg proto.Message) {
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
			s.pools[2].Put(chunk.Slices) // nolint:staticcheck
			chunk.Slices = nil
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

func (s *redisMixDBS) dumpNodes(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error {
	m := s.meta.(*redisMeta)
	vals, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	msg := &pb.NodeList{
		List: make([]*pb.Node, 0, len(vals)),
	}
	var inode uint64
	for idx, v := range vals {
		if v == nil {
			continue
		}
		inode, _ = strconv.ParseUint(keys[idx][len(m.prefix)+1:], 10, 64)
		node := pools[0].Get().(*pb.Node)
		node.Inode = inode
		node.Data = []byte(v.(string))
		msg.List = append(msg.List, node)
	}
	sum.Add(uint64(len(msg.List)))
	return dumpResult(ctx, ch, &dumpedResult{s, msg})
}

func (s *redisMixDBS) dumpEdges(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error {
	m := s.meta.(*redisMeta)
	msg := &pb.EdgeList{List: make([]*pb.Edge, 0, redisBatchSize)}
	for _, key := range keys {
		parent, _ := strconv.ParseUint(key[len(m.prefix)+1:], 10, 64)
		var pe *pb.Edge
		if err := m.hscan(ctx, m.entryKey(Ino(parent)), func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				pe = pools[1].Get().(*pb.Edge)
				pe.Parent = parent
				pe.Name = []byte(keys[i])
				typ, ino := m.parseEntry([]byte(keys[i+1]))
				pe.Type, pe.Inode = uint32(typ), uint64(ino)
				msg.List = append(msg.List, pe)

				if len(msg.List) >= redisBatchSize {
					if err := dumpResult(ctx, ch, &dumpedResult{s, msg}); err != nil {
						return err
					}
					sum.Add(uint64(len(msg.List)))
					msg = &pb.EdgeList{List: make([]*pb.Edge, 0, redisBatchSize)}
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	sum.Add(uint64(len(msg.List)))
	return dumpResult(ctx, ch, &dumpedResult{s, msg})
}

func (s *redisMixDBS) dumpChunks(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error {
	m := s.meta.(*redisMeta)
	pipe := m.rdb.Pipeline()
	inos := make([]uint64, 0, len(keys))
	idxs := make([]uint32, 0, len(keys))
	for _, key := range keys {
		ps := strings.Split(key, "_")
		if len(ps) != 2 {
			continue
		}
		ino, _ := strconv.ParseUint(ps[0][len(m.prefix)+1:], 10, 64)
		idx, _ := strconv.ParseUint(ps[1], 10, 32)
		pipe.LRange(ctx, m.chunkKey(Ino(ino), uint32(idx)), 0, -1)
		inos = append(inos, ino)
		idxs = append(idxs, uint32(idx))
	}

	cmds, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("chunk pipeline exec err: %w", err)
	}

	msg := &pb.ChunkList{
		List: make([]*pb.Chunk, 0, len(cmds)),
	}

	for k, cmd := range cmds {
		vals, err := cmd.(*redis.StringSliceCmd).Result()
		if err != nil {
			return fmt.Errorf("get chunk result err: %w", err)
		}
		if len(vals) == 0 {
			continue
		}

		pc := pools[2].Get().(*pb.Chunk)
		pc.Inode = inos[k]
		pc.Index = idxs[k]

		pc.Slices = pools[3].Get().([]byte)
		if len(pc.Slices) < len(vals)*sliceBytes {
			pc.Slices = make([]byte, len(vals)*sliceBytes)
		}
		pc.Slices = pc.Slices[:len(vals)*sliceBytes]

		for i, val := range vals {
			if len(val) != sliceBytes {
				logger.Errorf("corrupt slice: len=%d, val=%v", len(val), []byte(val))
				continue
			}
			copy(pc.Slices[i*sliceBytes:], []byte(val))
		}
		msg.List = append(msg.List, pc)
	}
	sum.Add(uint64(len(msg.List)))
	return dumpResult(ctx, ch, &dumpedResult{s, msg})
}

func (s *redisMixDBS) dumpSymlinks(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error {
	m := s.meta.(*redisMeta)
	vals, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	msg := &pb.SymlinkList{
		List: make([]*pb.Symlink, 0, len(vals)),
	}
	var ps *pb.Symlink
	for idx, v := range vals {
		if v == nil {
			continue
		}
		ps = s.pools[4].Get().(*pb.Symlink)
		ps.Inode, _ = strconv.ParseUint(keys[idx][len(m.prefix)+1:], 10, 64)
		ps.Target = unescape(v.(string))
		msg.List = append(msg.List, ps)
	}

	sum.Add(uint64(len(msg.List)))
	return dumpResult(ctx, ch, &dumpedResult{s, msg})
}

func (s *redisMixDBS) dumpXattrs(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error {
	m := s.meta.(*redisMeta)
	msg := &pb.XattrList{List: make([]*pb.Xattr, 0, 128)}
	pipe := m.rdb.Pipeline()
	for _, key := range keys {
		pipe.HGetAll(ctx, key)
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}

	var xattr *pb.Xattr
	for idx, cmd := range cmds {
		inode, _ := strconv.ParseUint(keys[idx][len(m.prefix)+1:], 10, 64)
		res, err := cmd.(*redis.MapStringStringCmd).Result()
		if err != nil {
			return err
		}

		if len(res) > 0 {
			for k, v := range res {
				xattr = pools[5].Get().(*pb.Xattr)
				xattr.Inode = inode
				xattr.Name = k
				xattr.Value = []byte(v)
				msg.List = append(msg.List, xattr)
			}
		}
	}
	sum.Add(uint64(len(msg.List)))
	return dumpResult(ctx, ch, &dumpedResult{s, msg})
}

func (s *redisMixDBS) dumpParents(ctx context.Context, ch chan *dumpedResult, keys []string, pools []*sync.Pool, sum *atomic.Uint64) error {
	m := s.meta.(*redisMeta)
	msg := &pb.ParentList{List: make([]*pb.Parent, 0, 1024)}
	pipe := m.rdb.Pipeline()
	for _, key := range keys {
		pipe.HGetAll(ctx, key)
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}

	var pp *pb.Parent
	for idx, cmd := range cmds {
		inode, _ := strconv.ParseUint(keys[idx][len(m.prefix)+1:], 10, 64)
		res, err := cmd.(*redis.MapStringStringCmd).Result()
		if err != nil {
			return err
		}

		if len(res) > 0 {
			for k, v := range res {
				pp = pools[6].Get().(*pb.Parent)
				parent, _ := strconv.ParseUint(k, 10, 64)
				cnt, _ := strconv.ParseInt(v, 10, 64)

				pp.Inode = inode
				pp.Parent = parent
				pp.Cnt = cnt
				msg.List = append(msg.List, pp)
			}
		}
	}
	sum.Add(uint64(len(msg.List)))
	return dumpResult(ctx, ch, &dumpedResult{s, msg})
}

type redisFormatLS struct {
	loadedSeg
}

func (s *redisFormatLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	return meta.rdb.Set(ctx, meta.setting(), msg.(*pb.Format).Data, 0).Err()
}

type redisCounterLS struct {
	loadedSeg
}

func (s *redisCounterLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	cs := make(map[string]interface{})
	fields := getRedisCounterFields(meta.prefix, msg.(*pb.Counters))
	for k, v := range fields {
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

func (s *redisSustainedLS) load(ctx Context, msg proto.Message) error {
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

func (s *redisDelFileLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pds := msg.(*pb.DelFileList)
	mbs := make([]redis.Z, 0, len(pds.List))
	for _, pd := range pds.List {
		mbs = append(mbs, redis.Z{
			Score:  float64(pd.Expire),
			Member: meta.toDelete(Ino(pd.Inode), pd.Length),
		})
	}
	if len(mbs) == 0 {
		return nil
	}
	return meta.rdb.ZAdd(ctx, meta.delfiles(), mbs...).Err()
}

type redisSliceRefLS struct {
	loadedSeg
}

func (s *redisSliceRefLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	ps := msg.(*pb.SliceRefList)

	slices := make(map[string]interface{})
	for _, p := range ps.List {
		slices[meta.sliceKey(p.Id, p.Size)] = strconv.Itoa(int(p.Refs - 1))
	}
	if len(slices) == 0 {
		return nil
	}
	return meta.rdb.HSet(ctx, meta.sliceRefs(), slices).Err()
}

type redisAclLS struct {
	loadedSeg
}

func (s *redisAclLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pas := msg.(*pb.AclList)

	var maxId uint32 = 0
	acls := make(map[string]interface{}, len(pas.List))
	for _, pa := range pas.List {
		if pa.Id > maxId {
			maxId = pa.Id
		}
		acls[strconv.FormatUint(uint64(pa.Id), 10)] = pa.Data
	}
	if len(acls) == 0 {
		return nil
	}
	if err := meta.rdb.HSet(ctx, meta.aclKey(), acls).Err(); err != nil {
		return err
	}
	return meta.rdb.Set(ctx, meta.prefix+aclCounter, maxId, 0).Err()
}

type redisXattrLS struct {
	loadedSeg
}

func (s *redisXattrLS) load(ctx Context, msg proto.Message) error {
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

func (s *redisQuotaLS) load(ctx Context, msg proto.Message) error {
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

func (s *redisStatLS) load(ctx Context, msg proto.Message) error {
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
}

func (s *redisNodeLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pns := msg.(*pb.NodeList)
	nodes := make(map[string]interface{}, redisBatchSize)

	mset := func(nodes map[string]interface{}) error {
		if len(nodes) == 0 {
			return nil
		}
		if err := meta.rdb.MSet(ctx, nodes).Err(); err != nil {
			return err
		}
		for k := range nodes {
			delete(nodes, k)
		}
		return nil
	}

	for _, pn := range pns.List {
		nodes[meta.inodeKey(Ino(pn.Inode))] = pn.Data
		if len(nodes) >= redisBatchSize {
			if err := mset(nodes); err != nil {
				return err
			}
		}
	}
	return mset(nodes)
}

type redisChunkLS struct {
	loadedSeg
}

func (s *redisChunkLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pcs := msg.(*pb.ChunkList)

	pipe := meta.rdb.Pipeline()
	for idx, chk := range pcs.List {
		slices := make([]string, 0, len(chk.Slices))
		for off := 0; off < len(chk.Slices); off += sliceBytes {
			slices = append(slices, string(chk.Slices[off:off+sliceBytes]))
		}
		pipe.RPush(ctx, meta.chunkKey(Ino(chk.Inode), chk.Index), slices)

		if idx%100 == 0 {
			if _, err := tryExecPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

type redisEdgeLS struct {
	loadedSeg
}

func (s *redisEdgeLS) load(ctx Context, msg proto.Message) error {
	meta := s.meta.(*redisMeta)
	pes := msg.(*pb.EdgeList)
	pipe := meta.rdb.Pipeline()

	for idx, edge := range pes.List {
		buff := utils.NewBuffer(9)
		buff.Put8(uint8(edge.Type))
		buff.Put64(edge.Inode)
		pipe.HSet(ctx, meta.entryKey(Ino(edge.Parent)), edge.Name, buff.Bytes())
		if idx%100 == 0 {
			if _, err := tryExecPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}

	if err := execPipe(ctx, pipe); err != nil {
		return err
	}
	return nil
}

type redisParentLS struct {
	loadedSeg
}

func (s *redisParentLS) load(ctx Context, msg proto.Message) error {
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

func (s *redisSymlinkLS) load(ctx Context, msg proto.Message) error {
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
	if len(syms) == 0 {
		return nil
	}
	return meta.rdb.MSet(ctx, syms).Err()
}

func (m *redisMeta) prepareLoad(ctx Context, opt *LoadOption) error {
	opt.check()
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
			return fmt.Errorf("database redis://%s is not empty", m.addr)
		}
	}
	return nil
}
