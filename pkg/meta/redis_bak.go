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
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

var (
	redisBatchSize = 10000
	redisPipeLimit = 1000
)

func (m *redisMeta) dump(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
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
	for _, f := range dumps {
		err := f(ctx, opt, ch)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *redisMeta) dumpCounters(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	counters := make([]*pb.Counter, 0, len(counterNames))
	for _, name := range counterNames {
		cnt, err := m.getCounter(name)
		if err != nil {
			return errors.Wrapf(err, "get counter %s", name)
		}
		if name == "nextInode" || name == "nextChunk" {
			cnt++ // Redis nextInode/nextChunk is one smaller than db
		}
		counters = append(counters, &pb.Counter{Key: name, Value: cnt})
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Counters: counters}})
}

func (m *redisMeta) dumpMix(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	logger.Warnf("please make sure the redis server is readonly, otherwise the dumped metadata will be inconsistent")
	pools := map[int][]*sync.Pool{
		segTypeNode:    {{New: func() interface{} { return &pb.Node{} }}},
		segTypeEdge:    {{New: func() interface{} { return &pb.Edge{} }}},
		segTypeChunk:   {{New: func() interface{} { return &pb.Chunk{} }}, {New: func() interface{} { return make([]byte, 8*sliceBytes) }}},
		segTypeSymlink: {{New: func() interface{} { return &pb.Symlink{} }}},
		segTypeXattr:   {{New: func() interface{} { return &pb.Xattr{} }}},
		segTypeParent:  {{New: func() interface{} { return &pb.Parent{} }}},
	}
	release := func(p proto.Message) {
		b := p.(*pb.Batch)
		for _, n := range b.Nodes {
			pools[segTypeNode][0].Put(n)
		}
		for _, e := range b.Edges {
			pools[segTypeEdge][0].Put(e)
		}
		for _, c := range b.Chunks {
			pools[segTypeChunk][1].Put(c.Slices) // nolint:staticcheck
			c.Slices = nil
			pools[segTypeChunk][0].Put(c)
		}
		for _, s := range b.Symlinks {
			pools[segTypeSymlink][0].Put(s)
		}
		for _, x := range b.Xattrs {
			pools[segTypeXattr][0].Put(x)
		}
		for _, p := range b.Parents {
			pools[segTypeParent][0].Put(p)
		}
	}
	char2Typ := map[byte]int{
		'i': segTypeNode,
		'd': segTypeEdge,
		'c': segTypeChunk,
		's': segTypeSymlink,
		'x': segTypeXattr,
		'p': segTypeParent,
	}
	typ2Limit := map[int]int{
		segTypeNode:    redisBatchSize,
		segTypeEdge:    redisBatchSize,
		segTypeChunk:   redisPipeLimit,
		segTypeSymlink: redisBatchSize,
		segTypeXattr:   redisPipeLimit,
		segTypeParent:  redisPipeLimit,
	}
	var typ2Keys = make(map[int][]string, len(typ2Limit))
	for typ, limit := range typ2Limit {
		typ2Keys[typ] = make([]string, 0, limit)
	}

	var sums = map[int]*atomic.Uint64{
		segTypeNode:    {},
		segTypeEdge:    {},
		segTypeChunk:   {},
		segTypeSymlink: {},
		segTypeXattr:   {},
		segTypeParent:  {},
	}
	typ2Handles := map[int]func(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error{
		segTypeNode:    m.dumpNodes,
		segTypeEdge:    m.dumpEdges,
		segTypeChunk:   m.dumpChunks,
		segTypeSymlink: m.dumpSymlinks,
		segTypeXattr:   m.dumpXattrs,
		segTypeParent:  m.dumpParents,
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(opt.Threads)

	keyCh := make(chan []string, opt.Threads*2)
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
				if typ, ok := char2Typ[key[len(m.prefix)]]; ok {
					typ2Keys[typ] = append(typ2Keys[typ], key)
					if len(typ2Keys[typ]) >= redisBatchSize {
						iPools, sum, keys := pools[typ], sums[typ], typ2Keys[typ]
						eg.Go(func() error {
							return typ2Handles[typ](ctx, ch, keys, iPools, release, sum)
						})
						typ2Keys[typ] = make([]string, 0, typ2Limit[typ])
					}
				}
			}
		}
		for typ, keys := range typ2Keys {
			if len(keys) > 0 {
				iKeys, iTyp := keys, typ
				eg.Go(func() error {
					return typ2Handles[iTyp](ctx, ch, iKeys, pools[iTyp], release, sums[iTyp])
				})
			}
		}
	}()

	if err := m.scan(egCtx, "*", func(sKeys []string) error {
		keyCh <- sKeys
		return nil
	}); err != nil {
		ctx.Cancel()
		wg.Wait()
		_ = eg.Wait()
		return err
	}

	close(keyCh)
	wg.Wait()
	if err := eg.Wait(); err != nil {
		return err
	}

	logger.Debugf("dump result: %s", printSums(sums))
	return nil
}

func (m *redisMeta) dumpSustained(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	keys, err := m.rdb.ZRange(ctx, m.allSessions(), 0, -1).Result()
	if err != nil {
		return err
	}

	sustained := make([]*pb.Sustained, 0, len(keys))
	for _, k := range keys {
		sid, _ := strconv.ParseUint(k, 10, 64)
		var ss []string
		ss, err = m.rdb.SMembers(ctx, m.sustained(sid)).Result()
		if err != nil {
			return err
		}
		if len(ss) > 0 {
			inodes := make([]uint64, 0, len(ss))
			for _, s := range ss {
				inode, _ := strconv.ParseUint(s, 10, 64)
				inodes = append(inodes, inode)
			}
			sustained = append(sustained, &pb.Sustained{Sid: sid, Inodes: inodes})
		}
	}

	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Sustained: sustained}})
}

func (m *redisMeta) dumpDelFiles(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	zs, err := m.rdb.ZRangeWithScores(ctx, m.delfiles(), 0, -1).Result()
	if err != nil {
		return err
	}

	delFiles := make([]*pb.DelFile, 0, utils.Min(len(zs), redisBatchSize))
	for i, z := range zs {
		parts := strings.Split(z.Member.(string), ":")
		if len(parts) != 2 {
			logger.Warnf("invalid delfile string: %s", z.Member.(string))
			continue
		}
		inode, _ := strconv.ParseUint(parts[0], 10, 64)
		length, _ := strconv.ParseUint(parts[1], 10, 64)
		delFiles = append(delFiles, &pb.DelFile{Inode: inode, Length: length, Expire: int64(z.Score)})
		if len(delFiles) >= redisBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Delfiles: delFiles}}); err != nil {
				return err
			}
			delFiles = make([]*pb.DelFile, 0, utils.Min(len(zs)-i-1, redisBatchSize))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Delfiles: delFiles}})
}

func (m *redisMeta) dumpSliceRef(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	sliceRefs := make([]*pb.SliceRef, 0, 1024)
	var key string
	var val int
	var inErr error
	if err := m.hscan(ctx, m.sliceRefs(), func(keys []string) error {
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
					sr := &pb.SliceRef{Id: id, Size: uint32(size), Refs: int64(val) + 1} // Redis sliceRef is one smaller than sql
					sliceRefs = append(sliceRefs, sr)
					if len(sliceRefs) >= redisBatchSize {
						if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{SliceRefs: sliceRefs}}); err != nil {
							return err
						}
						sliceRefs = make([]*pb.SliceRef, 0, 1024)
					}
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{SliceRefs: sliceRefs}})
}

func (m *redisMeta) dumpACL(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	vals, err := m.rdb.HGetAll(ctx, m.aclKey()).Result()
	if err != nil {
		return err
	}

	acls := make([]*pb.Acl, 0, len(vals))
	for k, v := range vals {
		id, _ := strconv.ParseUint(k, 10, 32)
		acls = append(acls, &pb.Acl{
			Id:   uint32(id),
			Data: []byte(v),
		})
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Acls: acls}})
}

func (m *redisMeta) dumpQuota(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	quotas := make(map[Ino]*pb.Quota)
	vals, err := m.rdb.HGetAll(ctx, m.dirQuotaKey()).Result()
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
		space, inodes := m.parseQuota([]byte(v))
		quotas[Ino(inode)] = &pb.Quota{
			Inode:     inode,
			MaxSpace:  space,
			MaxInodes: inodes,
		}
	}

	vals, err = m.rdb.HGetAll(ctx, m.dirQuotaUsedInodesKey()).Result()
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

	vals, err = m.rdb.HGetAll(ctx, m.dirQuotaUsedSpaceKey()).Result()
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

	qs := make([]*pb.Quota, 0, len(quotas))
	for _, q := range quotas {
		qs = append(qs, q)
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Quotas: qs}})
}

func (m *redisMeta) dumpDirStat(ctx Context, opt *DumpOption, ch chan<- *dumpedResult) error {
	stats := make(map[Ino]*pb.Stat)
	vals, err := m.rdb.HGetAll(ctx, m.dirDataLengthKey()).Result()
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

	vals, err = m.rdb.HGetAll(ctx, m.dirUsedInodesKey()).Result()
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

	vals, err = m.rdb.HGetAll(ctx, m.dirUsedSpaceKey()).Result()
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

	ss := make([]*pb.Stat, 0, utils.Min(len(stats), redisBatchSize))
	cnt := 0
	for _, s := range stats {
		cnt++
		ss = append(ss, s)
		if len(ss) >= redisBatchSize {
			if err := dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Dirstats: ss}}); err != nil {
				return err
			}
			ss = make([]*pb.Stat, 0, utils.Min(len(stats)-cnt, redisBatchSize))
		}
	}
	return dumpResult(ctx, ch, &dumpedResult{msg: &pb.Batch{Dirstats: ss}})
}

func (m *redisMeta) dumpNodes(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error {
	vals, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	nodes := make([]*pb.Node, 0, len(vals))
	var inode uint64
	for idx, v := range vals {
		if v == nil {
			continue
		}
		inode, _ = strconv.ParseUint(keys[idx][len(m.prefix)+1:], 10, 64)
		node := pools[0].Get().(*pb.Node)
		node.Inode = inode
		node.Data = []byte(v.(string))
		nodes = append(nodes, node)
	}
	sum.Add(uint64(len(nodes)))
	return dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Nodes: nodes}, rel})
}

func (m *redisMeta) dumpEdges(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error {
	edges := make([]*pb.Edge, 0, redisBatchSize)
	for _, key := range keys {
		parent, _ := strconv.ParseUint(key[len(m.prefix)+1:], 10, 64)
		var pe *pb.Edge
		if err := m.hscan(ctx, m.entryKey(Ino(parent)), func(keys []string) error {
			for i := 0; i < len(keys); i += 2 {
				pe = pools[0].Get().(*pb.Edge)
				pe.Parent = parent
				pe.Name = []byte(keys[i])
				typ, ino := m.parseEntry([]byte(keys[i+1]))
				pe.Type, pe.Inode = uint32(typ), uint64(ino)
				edges = append(edges, pe)

				if len(edges) >= redisBatchSize {
					if err := dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Edges: edges}, rel}); err != nil {
						return err
					}
					sum.Add(uint64(len(edges)))
					edges = make([]*pb.Edge, 0, redisBatchSize)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	sum.Add(uint64(len(edges)))
	return dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Edges: edges}, rel})
}

func (m *redisMeta) dumpChunks(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error {
	pipe := m.rdb.Pipeline()
	inos := make([]uint64, 0, len(keys))
	idxs := make([]uint32, 0, len(keys))
	for _, key := range keys {
		ps := strings.Split(key, "_")
		if len(ps) != 2 {
			logger.Warnf("invalid chunk key: %s", key)
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

	chunks := make([]*pb.Chunk, 0, len(cmds))
	for k, cmd := range cmds {
		vals, err := cmd.(*redis.StringSliceCmd).Result()
		if err != nil {
			return fmt.Errorf("get chunk result err: %w", err)
		}
		if len(vals) == 0 {
			continue
		}

		pc := pools[0].Get().(*pb.Chunk)
		pc.Inode = inos[k]
		pc.Index = idxs[k]

		pc.Slices = pools[1].Get().([]byte)
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
		chunks = append(chunks, pc)
	}
	sum.Add(uint64(len(chunks)))
	return dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Chunks: chunks}, rel})
}

func (m *redisMeta) dumpSymlinks(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error {
	vals, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	syms := make([]*pb.Symlink, 0, len(vals))
	var ps *pb.Symlink
	for idx, v := range vals {
		if v == nil {
			continue
		}
		ps = pools[0].Get().(*pb.Symlink)
		ps.Inode, err = strconv.ParseUint(keys[idx][len(m.prefix)+1:], 10, 64)
		if err != nil {
			continue // key "setting"
		}
		ps.Target = unescape(v.(string))
		syms = append(syms, ps)
	}

	sum.Add(uint64(len(syms)))
	return dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Symlinks: syms}, rel})
}

func (m *redisMeta) dumpXattrs(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error {
	xattrs := make([]*pb.Xattr, 0, len(keys))
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

		for k, v := range res {
			xattr = pools[0].Get().(*pb.Xattr)
			xattr.Inode = inode
			xattr.Name = k
			xattr.Value = []byte(v)
			xattrs = append(xattrs, xattr)
		}
	}
	sum.Add(uint64(len(xattrs)))
	return dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Xattrs: xattrs}, rel})
}

func (m *redisMeta) dumpParents(ctx context.Context, ch chan<- *dumpedResult, keys []string, pools []*sync.Pool, rel func(p proto.Message), sum *atomic.Uint64) error {
	parents := make([]*pb.Parent, 0, len(keys))
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

		for k, v := range res {
			pp = pools[0].Get().(*pb.Parent)
			parent, _ := strconv.ParseUint(k, 10, 64)
			cnt, _ := strconv.ParseInt(v, 10, 64)

			pp.Inode = inode
			pp.Parent = parent
			pp.Cnt = cnt
			parents = append(parents, pp)
		}
	}
	sum.Add(uint64(len(parents)))
	return dumpResult(ctx, ch, &dumpedResult{&pb.Batch{Parents: parents}, rel})
}

func (m *redisMeta) load(ctx Context, typ int, opt *LoadOption, val proto.Message) error {
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

func (m *redisMeta) loadFormat(ctx Context, msg proto.Message) error {
	return m.rdb.Set(ctx, m.setting(), msg.(*pb.Format).Data, 0).Err()
}

func (m *redisMeta) loadCounters(ctx Context, msg proto.Message) error {
	cs := make(map[string]interface{})

	for _, c := range msg.(*pb.Batch).Counters {
		if c.Key == "nextInode" || c.Key == "nextChunk" {
			cs[m.counterKey(c.Key)] = c.Value - 1
		} else {
			cs[m.counterKey(c.Key)] = c.Value
		}
	}
	return m.rdb.MSet(ctx, cs).Err()
}

func (m *redisMeta) loadNodes(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	pipe := m.rdb.Pipeline()
	for _, pn := range batch.Nodes {
		pipe.Set(ctx, m.inodeKey(Ino(pn.Inode)), pn.Data, 0)
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadEdges(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	pipe := m.rdb.Pipeline()
	for _, edge := range batch.Edges {
		buff := utils.NewBuffer(9)
		buff.Put8(uint8(edge.Type))
		buff.Put64(edge.Inode)
		pipe.HSet(ctx, m.entryKey(Ino(edge.Parent)), edge.Name, buff.Bytes())
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadChunks(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	pipe := m.rdb.Pipeline()
	for _, chk := range batch.Chunks {
		slices := make([]string, 0, len(chk.Slices))
		for off := 0; off < len(chk.Slices); off += sliceBytes {
			slices = append(slices, string(chk.Slices[off:off+sliceBytes]))
		}
		pipe.RPush(ctx, m.chunkKey(Ino(chk.Inode), chk.Index), slices)

		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadSymlinks(ctx Context, msg proto.Message) error {
	syms := make(map[string]interface{}, redisBatchSize)
	for _, ps := range msg.(*pb.Batch).Symlinks {
		syms[m.symKey(Ino(ps.Inode))] = ps.Target

		if len(syms) >= redisBatchSize {
			if err := m.rdb.MSet(ctx, syms).Err(); err != nil {
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
	return m.rdb.MSet(ctx, syms).Err()
}

func (m *redisMeta) loadSustained(ctx Context, msg proto.Message) error {
	pipe := m.rdb.Pipeline()
	for _, ps := range msg.(*pb.Batch).Sustained {
		inodes := make([]interface{}, len(ps.Inodes))
		for i, inode := range ps.Inodes {
			inodes[i] = inode
		}
		pipe.SAdd(ctx, m.sustained(ps.Sid), inodes...)
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadDelFiles(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	mbs := make([]redis.Z, 0, len(batch.Delfiles))
	for _, pd := range batch.Delfiles {
		mbs = append(mbs, redis.Z{
			Score:  float64(pd.Expire),
			Member: m.toDelete(Ino(pd.Inode), pd.Length),
		})
	}
	if len(mbs) == 0 {
		return nil
	}
	return m.rdb.ZAdd(ctx, m.delfiles(), mbs...).Err()
}

func (m *redisMeta) loadSliceRefs(ctx Context, msg proto.Message) error {
	slices := make(map[string]interface{})
	for _, p := range msg.(*pb.Batch).SliceRefs {
		slices[m.sliceKey(p.Id, p.Size)] = strconv.Itoa(int(p.Refs - 1))
	}
	if len(slices) == 0 {
		return nil
	}
	return m.rdb.HSet(ctx, m.sliceRefs(), slices).Err()
}

var loadLock sync.Mutex
var maxAclId uint32

func (m *redisMeta) loadAcl(ctx Context, msg proto.Message) error {
	batch := msg.(*pb.Batch)
	acls := make(map[string]interface{}, len(batch.Acls))
	for _, pa := range batch.Acls {
		loadLock.Lock()
		if pa.Id > maxAclId {
			maxAclId = pa.Id
		}
		loadLock.Unlock()
		acls[strconv.FormatUint(uint64(pa.Id), 10)] = pa.Data
	}
	if len(acls) == 0 {
		return nil
	}

	if err := m.rdb.HSet(ctx, m.aclKey(), acls).Err(); err != nil {
		return err
	}
	return m.rdb.Set(ctx, m.counterKey(aclCounter), maxAclId, 0).Err()
}

func (m *redisMeta) loadXattrs(ctx Context, msg proto.Message) error {
	pipe := m.rdb.Pipeline()
	xm := make(map[uint64]map[string]interface{}) // {inode: {name: value}}
	for _, px := range msg.(*pb.Batch).Xattrs {
		if _, ok := xm[px.Inode]; !ok {
			xm[px.Inode] = make(map[string]interface{})
		}
		xm[px.Inode][px.Name] = px.Value
	}

	for inode, xattrs := range xm {
		pipe.HSet(ctx, m.xattrKey(Ino(inode)), xattrs)
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadQuota(ctx Context, msg proto.Message) error {
	pipe := m.rdb.Pipeline()
	var inodeKey string
	for _, pq := range msg.(*pb.Batch).Quotas {
		inodeKey = Ino(pq.Inode).String()
		pipe.HSet(ctx, m.dirQuotaKey(), inodeKey, m.packQuota(pq.MaxSpace, pq.MaxInodes))
		pipe.HSet(ctx, m.dirQuotaUsedInodesKey(), inodeKey, pq.UsedInodes)
		pipe.HSet(ctx, m.dirQuotaUsedSpaceKey(), inodeKey, pq.UsedSpace)
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadDirStats(ctx Context, msg proto.Message) error {
	pipe := m.rdb.Pipeline()
	var inodeKey string
	for _, ps := range msg.(*pb.Batch).Dirstats {
		inodeKey = Ino(ps.Inode).String()
		pipe.HSet(ctx, m.dirDataLengthKey(), inodeKey, ps.DataLength)
		pipe.HSet(ctx, m.dirUsedInodesKey(), inodeKey, ps.UsedInodes)
		pipe.HSet(ctx, m.dirUsedSpaceKey(), inodeKey, ps.UsedSpace)
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
}

func (m *redisMeta) loadParents(ctx Context, msg proto.Message) error {
	pipe := m.rdb.Pipeline()
	for _, p := range msg.(*pb.Batch).Parents {
		pipe.HIncrBy(ctx, m.parentKey(Ino(p.Inode)), Ino(p.Parent).String(), p.Cnt)
		if pipe.Len() >= redisPipeLimit {
			if err := execPipe(ctx, pipe); err != nil {
				return err
			}
		}
	}
	return execPipe(ctx, pipe)
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
