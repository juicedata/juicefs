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
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func checkFlags() *cli.Command {
	return &cli.Command{
		Name:      "fsck",
		Usage:     "Check consistency of file system",
		ArgsUsage: "REDIS-URL",
		Action:    fsck,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "threads",
				Value: 50,
				Usage: "number threads to delete leaked objects",
			},
		},
	}
}

func fsck(ctx *cli.Context) error {
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
	var blocks = make(map[string]int64)
	var totalBlockBytes int64
	for obj := range objs {
		if obj == nil {
			break // failed listing
		}
		if obj.IsDir {
			continue
		}

		logger.Debugf("found block %s", obj.Key)
		parts := strings.Split(obj.Key, "/")
		if len(parts) != 3 {
			continue
		}
		name := parts[2]
		blocks[name] = obj.Size
		totalBlockBytes += obj.Size
	}
	logger.Infof("Found %d blocks (%d bytes)", len(blocks), totalBlockBytes)

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
		n := (s.Size - 1) / uint32(chunkConf.BlockSize)
		for i := uint32(0); i <= n; i++ {
			sz := chunkConf.BlockSize
			if i == n {
				sz = int(s.Size) - int(i) * chunkConf.BlockSize
			}
			key := fmt.Sprintf("%d_%d_%d", s.Chunkid, i, sz)
			if _, ok := blocks[key]; !ok {
				if _, err := blob.Head(key); err != nil {
					logger.Errorf("can't find block %s: %s", key, err)
				}
			}
		}
	}
	logger.Infof("Used by %d slices (%d bytes)", len(keys), totalBytes)

	return nil
}
