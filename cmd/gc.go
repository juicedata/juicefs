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

	"github.com/urfave/cli/v2"
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

type dChunk struct {
	chunkid uint64
	length  uint32
}

func gc(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	removePassword(ctx.Args().Get(0))
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
		BufferSize: 300 << 20,
		CacheDir:   "memory",
	}

	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	store := chunk.NewCachedStore(blob, chunkConf)

	// Scan all chunks first and do compaction if necessary
	progress := utils.NewProgress(false, false)
	if ctx.Bool("compact") {
		bar := progress.AddCountBar("Scanned chunks", 0)
		spin := progress.AddDoubleSpinner("Compacted slices")
		m.OnMsg(meta.DeleteChunk, func(args ...interface{}) error {
			return store.Remove(args[0].(uint64), int(args[1].(uint32)))
		})
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			err := vfs.Compact(chunkConf, store, slices, args[1].(uint64))
			for _, s := range slices {
				spin.IncrInt64(int64(s.Len))
			}
			return err
		})
		if st := m.CompactAll(meta.Background, bar); st == 0 {
			bar.Done()
			spin.Done()
			if progress.Quiet {
				c, b := spin.Current()
				logger.Infof("Compacted %d chunks (%d slices, %d bytes).", bar.Current(), c, b)
			}
		} else {
			logger.Errorf("compact all chunks: %s", st)
		}
	} else {
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			return nil // ignore compaction
		})
	}

	// put it above delete count spinner
	sliceCSpin := progress.AddCountSpinner("Listed slices")

	// Delete pending chunks while listing slices
	delete := ctx.Bool("delete")
	var delSpin *utils.Bar
	var chunkChan chan *dChunk // pending delete chunks
	var wg sync.WaitGroup
	if delete {
		delSpin = progress.AddCountSpinner("Deleted pending")
		chunkChan = make(chan *dChunk, 10240)
		m.OnMsg(meta.DeleteChunk, func(args ...interface{}) error {
			delSpin.Increment()
			chunkChan <- &dChunk{args[0].(uint64), args[1].(uint32)}
			return nil
		})
		for i := 0; i < ctx.Int("threads"); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for c := range chunkChan {
					if err := store.Remove(c.chunkid, int(c.length)); err != nil {
						logger.Warnf("remove %d_%d: %s", c.chunkid, c.length, err)
					}
				}
			}()
		}
	}

	// List all slices in metadata engine
	var c = meta.NewContext(0, 0, []uint32{0})
	slices := make(map[meta.Ino][]meta.Slice)
	r := m.ListSlices(c, slices, delete, sliceCSpin.Increment)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}
	if delete {
		close(chunkChan)
		wg.Wait()
		delSpin.Done()
		if progress.Quiet {
			logger.Infof("Deleted %d pending chunks", delSpin.Current())
		}
	}
	sliceCSpin.Done()

	// Scan all objects to find leaked ones
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
	if progress.Quiet {
		logger.Infof("using %d slices (%d bytes)", len(keys), totalBytes)
	}

	bar := progress.AddCountBar("Scanned objects", total)
	valid := progress.AddDoubleSpinner("Valid objects")
	leaked := progress.AddDoubleSpinner("Leaked objects")
	skipped := progress.AddDoubleSpinner("Skipped objects")
	maxMtime := time.Now().Add(time.Hour * -1)
	strDuration := os.Getenv("JFS_GC_SKIPPEDTIME")
	if strDuration != "" {
		iDuration, err := strconv.Atoi(strDuration)
		if err == nil {
			maxMtime = time.Now().Add(time.Second * -1 * time.Duration(iDuration))
		} else {
			logger.Errorf("parse JFS_GC_SKIPPEDTIME=%s: %s", strDuration, err)
		}
	}

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
		bar.IncrTotal(1)
		leaked.IncrInt64(obj.Size())
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
			skipped.IncrInt64(obj.Size())
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
				valid.IncrInt64(obj.Size())
			}
		} else {
			if indx*chunkConf.BlockSize+csize != int(size) {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else {
				valid.IncrInt64(obj.Size())
			}
		}
	}
	close(leakedObj)
	wg.Wait()
	progress.Done()

	vc, _ := valid.Current()
	lc, lb := leaked.Current()
	sc, sb := skipped.Current()
	logger.Infof("scanned %d objects, %d valid, %d leaked (%d bytes), %d skipped (%d bytes)",
		bar.Current(), vc, lc, lb, sc, sb)
	if lc > 0 && !delete {
		logger.Infof("Please add `--delete` to clean leaked objects")
	}
	return nil
}
