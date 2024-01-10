//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package cmd

import (
	"fmt"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
	"sync"
	"time"
)

func cmdCompact() *cli.Command {
	return &cli.Command{
		Name:      "compact",
		Action:    compact,
		Category:  "ADMIN",
		Usage:     "Trigger compaction of slices",
		ArgsUsage: "META-URL",
		Description: `
 Examples:
 # compact with path
 $ juicefs compact --path /mnt/jfs/foo redis://localhost

 # max depth of 5
 $ juicefs summary --path /mnt/jfs/foo --depth 5 /mnt/jfs/foo redis://localhost
 `,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "path",
				Required: true,
				Usage:    "path to be compact",
			},
			&cli.UintFlag{
				Name:    "depth",
				Aliases: []string{"d"},
				Value:   2,
				Usage:   "dir depth to be scan",
			},
			&cli.IntFlag{
				Name:    "compact-concurrency",
				Aliases: []string{"cc"},
				Value:   10,
				Usage:   "compact concurrency",
			},
			&cli.IntFlag{
				Name:    "delete-concurrency",
				Aliases: []string{"dc"},
				Value:   10,
				Usage:   "delete concurrency",
			},
		},
	}
}

func compact(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))

	// parse flags
	metaUri := ctx.Args().Get(0)
	paths := ctx.StringSlice("path")

	depth := ctx.Uint("depth")
	if depth > 10 {
		logger.Warn("depth should be <= 10")
		depth = 10
	}

	deleteConcurrency := ctx.Int("delete-concurrency")
	if deleteConcurrency <= 0 {
		logger.Warn("thread number should be > 0")
		deleteConcurrency = 1
	}

	compactConcurrency := ctx.Int("compact-concurrency")
	if compactConcurrency <= 0 {
		logger.Warn("thread number should be > 0")
		compactConcurrency = 1
	}

	// new meta client
	metaConf := meta.DefaultConf()
	metaConf.MaxDeletes = int(deleteConcurrency)
	client := meta.NewClient(metaUri, metaConf)
	metaFormat, err := client.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize:  metaFormat.BlockSize * 1024,
		Compress:   metaFormat.Compression,
		GetTimeout: time.Second * 60,
		PutTimeout: time.Second * 60,
		MaxUpload:  20,
		BufferSize: 300 << 20,
		CacheDir:   "memory",
	}
	blob, err := createStorage(*metaFormat)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	store := chunk.NewCachedStore(blob, chunkConf, nil)

	progress := utils.NewProgress(false)

	// delete slice handle
	var wg sync.WaitGroup
	delSpin := progress.AddCountSpinner("Cleaned pending slices")
	sliceChan := make(chan meta.Slice, 10240)
	client.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
		delSpin.Increment()
		sliceChan <- meta.Slice{Id: args[0].(uint64), Size: args[1].(uint32)}
		return nil
	})
	for i := 0; i < deleteConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range sliceChan {
				if err := store.Remove(s.Id, int(s.Size)); err != nil {
					logger.Warnf("remove %d_%d: %s", s.Id, s.Size, err)
				}
			}
		}()
	}

	for i := 0; i < len(paths); i++ {
		path := paths[i]

		// path to inode
		inodeNo, err := utils.GetInode(path)
		if err != nil {
			logger.Fatal(err)
		}
		inode := meta.Ino(inodeNo)

		if !inode.IsValid() {
			logger.Fatalf("inode numbe %d not valid", inode)
		}

		logger.Debugf("compact path: %v, inode %v", path, inode)

		// do meta.Compact
		bar := progress.AddCountBar(fmt.Sprintf("compacted chunks for %s", path), 0)
		spin := progress.AddDoubleSpinnerTwo(fmt.Sprintf("compacted slices for %s", path), "compacted data")
		client.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			err := vfs.Compact(chunkConf, store, slices, args[1].(uint64))
			for _, s := range slices {
				spin.IncrInt64(int64(s.Len))
			}
			return err
		})

		compactErr := client.Compact(meta.Background, inode, int(depth), compactConcurrency,
			func() {
				bar.IncrTotal(1)
			},
			func() {
				bar.Increment()
			})
		if compactErr == 0 {
			if progress.Quiet {
				c, b := spin.Current()
				logger.Infof("compacted [%s] %d chunks (%d slices, %d bytes).", path, bar.Current(), c, b)
			}
		} else {
			logger.Errorf("compact [%s] chunks: %s", path, compactErr)
		}
		bar.Done()
		spin.Done()
	}

	progress.Done()
	return nil
}
