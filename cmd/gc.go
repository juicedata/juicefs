/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func gcFlags() *cli.Command {
	return &cli.Command{
		Name:      "gc",
		Usage:     "collect any leaked objects",
		ArgsUsage: "REDIS-URL",
		Action:    gc,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "delete",
				Usage: "deleted leaked objects",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 50,
				Usage: "number threads to delete leaked objects",
			},
		},
	}
}

func gc(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("REDIS-URL is needed")
	}
	addr := ctx.Args().Get(0)
	if !strings.Contains(addr, "://") {
		addr = "redis://" + addr
	}

	logger.Infof("Meta address: %s", addr)
	var rc = meta.RedisConfig{Retries: 10, Strict: true}
	m, err := meta.NewRedisMeta(addr, &rc)
	if err != nil {
		logger.Fatalf("Meta: %s", err)
	}
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout: time.Second * 60,
		PutTimeout: time.Second * 60,
		MaxUpload:  20,
		Prefetch:   0,
		BufferSize: 300,
		CacheDir:   "memory",
		CacheSize:  0,
	}

	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	}))
	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	}))

	blob = object.WithPrefix(blob, "chunks/")
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}

	var c = meta.NewContext(0, 0, []uint32{0})
	var slices []meta.Slice
	r := m.ListSlices(c, &slices)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}
	keys := make(map[uint64]uint32)
	var totalBytes uint64
	for _, s := range slices {
		keys[s.Chunkid] = s.Size
		totalBytes += uint64(s.Size)
	}
	logger.Infof("using %d slices (%d bytes)", len(keys), totalBytes)

	var leaked, leakedBytes int64
	var skipped, skippedBytes int64
	maxMtime := time.Now().Add(time.Hour * -24)

	var leadObj = make(chan string, 10240)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range leadObj {
				if err := blob.Delete(key); err != nil {
					logger.Warnf("delete %s: %s", key, err)
				}
			}
		}()
	}

	foundLeaked := func(obj *object.Object) {
		leakedBytes += obj.Size
		leaked++
		if ctx.Bool("delete") {
			leadObj <- obj.Key
		}
	}
	for obj := range objs {
		if obj == nil {
			break // failed listing
		}
		if strings.HasSuffix(obj.Key, "/") || obj.IsDir {
			continue
		}
		if obj.Mtime.After(maxMtime) {
			logger.Debugf("ignore new block: %s %s", obj.Key, obj.Mtime)
			skippedBytes += obj.Size
			skipped++
			continue
		}
		logger.Debugf("found block %s", obj.Key)
		name := strings.Split(obj.Key, "/")[2]
		parts := strings.Split(name, "_")
		cid, _ := strconv.Atoi(parts[0])
		size := keys[uint64(cid)]
		if size == 0 {
			logger.Debugf("find leaked object: %s, size: %d", obj.Key, obj.Size)
			foundLeaked(obj)
			continue
		}
		indx, _ := strconv.Atoi(parts[1])
		csize, _ := strconv.Atoi(parts[2])
		if csize == chunkConf.BlockSize {
			if (indx+1)*csize > int(size) {
				logger.Warnf("size of slice %d is larger than expected: %d > %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			}
		} else {
			if indx*chunkConf.BlockSize+csize != int(size) {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			}
		}
	}
	close(leadObj)
	wg.Wait()
	logger.Infof("found %d leaked objects (%d bytes), skipped %d (%d bytes)", leaked, leakedBytes, skipped, skippedBytes)
	return nil
}
