/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

type dChunk struct {
	chunkid uint64
	length  uint32
}

func gc(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{
		Retries:    10,
		Strict:     true,
		MaxDeletes: ctx.Int("threads"),
	})
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
		BufferSize: 300,
		CacheDir:   "memory",
	}

	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	delete := ctx.Bool("delete")
	store := chunk.NewCachedStore(blob, chunkConf)
	var pendingProgress *mpb.Progress
	var pending *mpb.Bar
	var pendingObj chan *dChunk
	var wg sync.WaitGroup
	if delete {
		pendingProgress, pending = utils.NewProgressCounter("pending deletes counter: ")
		pendingObj = make(chan *dChunk, 10240)
		m.OnMsg(meta.DeleteChunk, func(args ...interface{}) error {
			pending.Increment()
			pendingObj <- &dChunk{args[0].(uint64), args[1].(uint32)}
			return nil
		})
		for i := 0; i < ctx.Int("threads"); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for c := range pendingObj {
					if err := store.Remove(c.chunkid, int(c.length)); err != nil {
						logger.Warnf("remove %d_%d: %s", c.chunkid, c.length, err)
					}
				}
			}()
		}
	} else {
		m.OnMsg(meta.DeleteChunk, func(args ...interface{}) error {
			return store.Remove(args[0].(uint64), int(args[1].(uint32)))
		})
	}
	if ctx.Bool("compact") {
		var nc, ns, nb int
		var lastLog time.Time
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
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
		})
		logger.Infof("start to compact chunks ...")
		err := m.CompactAll(meta.Background)
		if err != 0 {
			logger.Errorf("compact all chunks: %s", err)
		} else {
			logger.Infof("Compacted %d chunks (%d slices, %d bytes).", nc, ns, nb)
		}
	} else {
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			return nil // ignore compaction
		})
	}

	progress, bar := utils.NewProgressCounter("listed slices counter: ")
	var c = meta.NewContext(0, 0, []uint32{0})
	slices := make(map[meta.Ino][]meta.Slice)
	r := m.ListSlices(c, slices, delete, bar.Increment)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}
	if delete {
		close(pendingObj)
		wg.Wait()
		pending.SetTotal(0, true)
		pendingProgress.Wait()
		logger.Infof("deleted %d pending objects", pending.Current())
	}
	bar.SetTotal(0, true)
	progress.Wait()

	blob = object.WithPrefix(blob, "chunks/")
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}
	keys := make(map[uint64]uint32)
	var total int64
	var totalBytes uint64
	for _, ss := range slices {
		for _, s := range ss {
			keys[s.Chunkid] = s.Size
			total += int64(int(s.Size-1)/chunkConf.BlockSize) + 1 // s.Size should be > 0
			totalBytes += uint64(s.Size)
		}
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
		if delete {
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
	if leaked.count.Current() > 0 && !delete {
		logger.Infof("Please add `--delete` to clean leaked objects")
	}
	return nil
}
