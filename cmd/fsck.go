/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"github.com/sirupsen/logrus"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/urfave/cli/v2"
)

func checkFlags() *cli.Command {
	return &cli.Command{
		Name:      "fsck",
		Usage:     "Check consistency of file system",
		ArgsUsage: "META-URL",
		Action:    fsck,
	}
}

func fsck(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
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

	blob = object.WithPrefix(blob, "chunks/")
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}
	var blocks = make(map[string]int64)
	var totalBlockBytes int64

	var total int64
	dynamicPros := mpb.New(mpb.WithWidth(64), mpb.WithOutput(logger.WriterLevel(logrus.InfoLevel)))
	dynamicProsName := "Listing all blocks"
	// new bar with 'trigger complete event' disabled, because total is zero
	dynamicBar := dynamicPros.Add(total,
		mpb.NewBarFiller(mpb.BarStyle().Tip(`-`, `\`, `|`, `/`)),
		mpb.PrependDecorators(decor.Name(dynamicProsName, decor.WC{W: len(dynamicProsName) + 1, C: decor.DidentRight}), decor.CountersNoUnit("%d / %d")),
		mpb.AppendDecorators(decor.Percentage()),
	)

	for obj := range objs {
		total += 1
		dynamicBar.SetTotal(total, false)
		if obj == nil {
			break // failed listing
		}
		if obj.IsDir() {
			continue
		}

		logger.Debugf("found block %s", obj.Key())
		parts := strings.Split(obj.Key(), "/")
		if len(parts) != 3 {
			continue
		}
		name := parts[2]
		blocks[name] = obj.Size()
		totalBlockBytes += obj.Size()
		dynamicBar.Increment()
	}
	dynamicBar.SetTotal(total, true)
	dynamicPros.Wait()
	logger.Infof("Found %d blocks (%d bytes)", len(blocks), totalBlockBytes)

	var c = meta.NewContext(0, 0, []uint32{0})
	var slices []meta.Slice
	r := m.ListSlices(c, &slices, false)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}
	keys := make(map[uint64]uint32)
	var totalBytes uint64
	var lost, lostBytes int

	singlePros := mpb.New(mpb.WithWidth(64), mpb.WithOutput(logger.WriterLevel(logrus.InfoLevel)))
	singleBarName := "Listing all slices"
	// adding a single bar, which will inherit container's width
	singleBar := singlePros.Add(int64(len(slices)),
		// progress bar filler with customized style
		mpb.NewBarFiller(mpb.BarStyle().Lbound("╢").Filler("▌").Tip("▌").Padding("░").Rbound("╟")),
		mpb.PrependDecorators(
			// display our name with one space on the right
			decor.Name(singleBarName, decor.WC{W: len(singleBarName) + 1, C: decor.DidentRight}),
			// replace ETA decorator with "done" message, OnComplete event
			decor.OnComplete(
				decor.AverageETA(decor.ET_STYLE_GO, decor.WC{W: 4}), "done",
			),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	for _, s := range slices {
		keys[s.Chunkid] = s.Size
		totalBytes += uint64(s.Size)
		n := (s.Size - 1) / uint32(chunkConf.BlockSize)
		for i := uint32(0); i <= n; i++ {
			sz := chunkConf.BlockSize
			if i == n {
				sz = int(s.Size) - int(i)*chunkConf.BlockSize
			}
			key := fmt.Sprintf("%d_%d_%d", s.Chunkid, i, sz)
			if _, ok := blocks[key]; !ok {
				if _, err := blob.Head(key); err != nil {
					logger.Errorf("can't find block %s: %s", key, err)
					lost++
					lostBytes += sz
				}
			}
		}
		singleBar.Increment()
	}
	dynamicBar.SetTotal(-1, true)
	singlePros.Wait()
	logger.Infof("Used by %d slices (%d bytes)", len(keys), totalBytes)
	if lost > 0 {
		logger.Fatalf("%d object is lost (%d bytes)", lost, lostBytes)
	}

	return nil
}
