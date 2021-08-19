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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

func gcFlags() *cli.Command {
	return &cli.Command{
		Name:      "gc",
		Usage:     "collect any leaked objects",
		ArgsUsage: "META-URL",
		Action:    gc,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "delete",
				Usage: "deleted leaked objects",
			},
			&cli.BoolFlag{
				Name:  "compact",
				Usage: "compact small slices into bigger ones",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number threads to delete leaked objects",
			},
		},
	}
}

type objCounter struct {
	count *mpb.Bar
	bytes *mpb.Bar
}

func (c *objCounter) add(size int64) {
	c.count.Increment()
	c.bytes.IncrInt64(size)
}

func (c *objCounter) done() {
	c.count.SetTotal(0, true)
	c.bytes.SetTotal(0, true)
}

func gc(ctx *cli.Context) error {
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
		CacheSize:  300,
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
	if ctx.Bool("compact") {
		var nc, ns, nb int
		var lastLog time.Time
		m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			chunkid := args[1].(uint64)
			err = vfs.Compact(chunkConf, store, slices, chunkid)
			nc++
			for _, s := range slices {
				ns++
				nb += int(s.Len)
			}
			if time.Since(lastLog) > time.Second && isatty.IsTerminal(os.Stdout.Fd()) {
				fmt.Printf("Compacted %d chunks (%d slices, %d bytes).\r", nc, ns, nb)
				lastLog = time.Now()
			}
			return err
		}))
		logger.Infof("start to compact chunks ...")
		err := m.CompactAll(meta.Background)
		if err != 0 {
			logger.Errorf("compact all chunks: %s", err)
		} else {
			logger.Infof("Compacted %d chunks (%d slices, %d bytes).", nc, ns, nb)
		}
	}

	blob = object.WithPrefix(blob, "chunks/")
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}

	progress, bar := utils.NewProgressCounter("listed slices counter: ")
	var c = meta.NewContext(0, 0, []uint32{0})
	var slices []meta.Slice
	r := m.ListSlices(c, &slices, ctx.Bool("delete"), bar.Increment)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}
	bar.SetTotal(0, true)
	progress.Wait()

	keys := make(map[uint64]uint32)
	var total int64
	var totalBytes uint64
	for _, s := range slices {
		keys[s.Chunkid] = s.Size
		total += int64(int(s.Size-1)/chunkConf.BlockSize) + 1 // s.Size should be > 0
		totalBytes += uint64(s.Size)
	}
	logger.Infof("using %d slices (%d bytes)", len(keys), totalBytes)

	progress, bar = utils.NewDynProgressBar("scanning objects: ", false)
	bar.SetTotal(total, false)
	addSpinner := func(name string) *objCounter {
		count := progress.Add(0,
			utils.NewSpinner(),
			mpb.PrependDecorators(
				decor.Name(name+" count: ", decor.WCSyncWidth),
				decor.CurrentNoUnit("%d", decor.WCSyncWidthR),
			),
			mpb.BarFillerClearOnComplete(),
		)
		bytes := progress.Add(0,
			utils.NewSpinner(),
			mpb.PrependDecorators(
				decor.Name(name+" bytes: ", decor.WCSyncWidth),
				decor.CurrentKibiByte("% d", decor.WCSyncWidthR),
			),
			mpb.BarFillerClearOnComplete(),
		)
		return &objCounter{count, bytes}
	}
	valid, leaked, skipped := addSpinner("valid"), addSpinner("leaked"), addSpinner("skipped")

	maxMtime := time.Now().Add(time.Hour * -1)
	var leakedObj = make(chan string, 10240)
	var wg sync.WaitGroup
	for i := 0; i < ctx.Int("threads"); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range leakedObj {
				if err := blob.Delete(key); err != nil {
					logger.Warnf("delete %s: %s", key, err)
				}
			}
		}()
	}

	foundLeaked := func(obj object.Object) {
		total++
		bar.SetTotal(total, false)
		leaked.add(obj.Size())
		if ctx.Bool("delete") {
			leakedObj <- obj.Key()
		}
	}
	for obj := range objs {
		if obj == nil {
			break // failed listing
		}
		if obj.IsDir() {
			continue
		}
		if obj.Mtime().After(maxMtime) || obj.Mtime().Unix() == 0 {
			logger.Debugf("ignore new block: %s %s", obj.Key(), obj.Mtime())
			bar.Increment()
			skipped.add(obj.Size())
			continue
		}

		logger.Debugf("found block %s", obj.Key())
		parts := strings.Split(obj.Key(), "/")
		if len(parts) != 3 {
			continue
		}
		name := parts[2]
		parts = strings.Split(name, "_")
		if len(parts) != 3 {
			continue
		}
		bar.Increment()
		cid, _ := strconv.Atoi(parts[0])
		size := keys[uint64(cid)]
		if size == 0 {
			logger.Debugf("find leaked object: %s, size: %d", obj.Key(), obj.Size())
			foundLeaked(obj)
			continue
		}
		indx, _ := strconv.Atoi(parts[1])
		csize, _ := strconv.Atoi(parts[2])
		if csize == chunkConf.BlockSize {
			if (indx+1)*csize > int(size) {
				logger.Warnf("size of slice %d is larger than expected: %d > %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else {
				valid.add(obj.Size())
			}
		} else {
			if indx*chunkConf.BlockSize+csize != int(size) {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else {
				valid.add(obj.Size())
			}
		}
	}
	close(leakedObj)
	wg.Wait()
	bar.SetTotal(0, true)
	valid.done()
	leaked.done()
	skipped.done()
	progress.Wait()

	logger.Infof("scanned %d objects, %d valid, %d leaked (%d bytes), %d skipped (%d bytes)",
		bar.Current(), valid.count.Current(), leaked.count.Current(), leaked.bytes.Current(), skipped.count.Current(), skipped.bytes.Current())
	if leaked.count.Current() > 0 && !ctx.Bool("delete") {
		logger.Infof("Please add `--delete` to clean leaked objects")
	}
	return nil
}
