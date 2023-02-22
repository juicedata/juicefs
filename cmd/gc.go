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

package cmd

import (
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

func cmdGC() *cli.Command {
	return &cli.Command{
		Name:      "gc",
		Action:    gc,
		Category:  "ADMIN",
		Usage:     "Garbage collector of objects in data storage",
		ArgsUsage: "META-URL",
		Description: `
It scans all objects in data storage and slices in metadata, comparing them to see if there is any
leaked object. It can also actively trigger compaction of slices and the cleanup of delayed deleted slices or files.
Use this command if you find that data storage takes more than expected.

Examples:
# Check only, no writable change
$ juicefs gc redis://localhost

# Trigger compaction of all slices
$ juicefs gc redis://localhost --compact

# Delete leaked objects and delayed deleted slices or files
$ juicefs gc redis://localhost --delete`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "compact",
				Usage: "compact small slices into bigger ones",
			},
			&cli.BoolFlag{
				Name:  "delete",
				Usage: "delete leaked objects and delayed deleted slices or files",
			},
			&cli.IntFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   10,
				Usage:   "number threads to delete leaked objects",
			},
		},
	}
}

func gc(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	metaConf := meta.DefaultConf()
	metaConf.MaxDeletes = ctx.Int("threads")
	m := meta.NewClient(ctx.Args().Get(0), metaConf)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize:  format.BlockSize * 1024,
		Compress:   format.Compression,
		GetTimeout: time.Second * 60,
		PutTimeout: time.Second * 60,
		MaxUpload:  20,
		BufferSize: 300 << 20,
		CacheDir:   "memory",
	}

	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	store := chunk.NewCachedStore(blob, chunkConf, nil)

	// Scan all chunks first and do compaction if necessary
	progress := utils.NewProgress(false)
	// Delete pending slices while listing all slices
	delete := ctx.Bool("delete")
	threads := ctx.Int("threads")
	compact := ctx.Bool("compact")
	if (delete || compact) && threads <= 0 {
		logger.Fatal("threads should be greater than 0 to delete or compact objects")
	}

	var wg sync.WaitGroup
	var delSpin *utils.Bar
	var sliceChan chan meta.Slice // pending delete slices

	if delete || compact {
		delSpin = progress.AddCountSpinner("Deleted pending")
		sliceChan = make(chan meta.Slice, 10240)
		m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
			delSpin.Increment()
			sliceChan <- meta.Slice{Id: args[0].(uint64), Size: args[1].(uint32)}
			return nil
		})
		for i := 0; i < threads; i++ {
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
	}

	c := meta.WrapContext(ctx.Context)
	delayedFileSpin := progress.AddDoubleSpinner("Delfiles")
	cleanedFileSpin := progress.AddDoubleSpinner("Cleaned delfiles")
	edge := time.Now().Add(-time.Duration(format.TrashDays) * 24 * time.Hour)
	if delete {
		cleanTrashSpin := progress.AddCountSpinner("Cleaned trash")
		m.CleanupTrashBefore(c, edge, cleanTrashSpin.Increment)
		cleanTrashSpin.Done()
	}

	err = m.ScanDeletedObject(
		c,
		nil, nil, nil,
		func(_ meta.Ino, size uint64, ts int64) (bool, error) {
			delayedFileSpin.IncrInt64(int64(size))
			if delete {
				cleanedFileSpin.IncrInt64(int64(size))
				return true, nil
			}
			return false, nil
		},
	)
	if err != nil {
		logger.Fatalf("scan deleted object: %s", err)
	}
	delayedFileSpin.Done()
	cleanedFileSpin.Done()

	if compact {
		bar := progress.AddCountBar("Compacted chunks", 0)
		spin := progress.AddDoubleSpinner("Compacted slices")
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			err := vfs.Compact(chunkConf, store, slices, args[1].(uint64))
			for _, s := range slices {
				spin.IncrInt64(int64(s.Len))
			}
			return err
		})
		if st := m.CompactAll(meta.Background, ctx.Int("threads"), bar); st == 0 {
			if progress.Quiet {
				c, b := spin.Current()
				logger.Infof("Compacted %d chunks (%d slices, %d bytes).", bar.Current(), c, b)
			}
		} else {
			logger.Errorf("compact all chunks: %s", st)
		}
		bar.Done()
		spin.Done()
	} else {
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			return nil // ignore compaction
		})
	}

	// put it above delete count spinner
	sliceCSpin := progress.AddCountSpinner("Listed slices")

	// List all slices in metadata engine
	slices := make(map[meta.Ino][]meta.Slice)
	r := m.ListSlices(c, slices, delete, sliceCSpin.Increment)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}

	delayedSliceSpin := progress.AddDoubleSpinner("Delslices")
	cleanedSliceSpin := progress.AddDoubleSpinner("Cleaned delslices")

	err = m.ScanDeletedObject(
		c,
		func(ss []meta.Slice, ts int64) (bool, error) {
			for _, s := range ss {
				delayedSliceSpin.IncrInt64(int64(s.Size))
				if delete && ts < edge.Unix() {
					cleanedSliceSpin.IncrInt64(int64(s.Size))
				}
			}
			if delete && ts < edge.Unix() {
				return true, nil
			}
			return false, nil
		},
		nil, nil, nil,
	)
	if err != nil {
		logger.Fatalf("statistic: %s", err)
	}
	delayedSliceSpin.Done()
	cleanedSliceSpin.Done()

	// Scan all objects to find leaked ones
	blob = object.WithPrefix(blob, "chunks/")
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}
	vkeys := make(map[uint64]uint32)
	ckeys := make(map[uint64]uint32)
	var total int64
	var totalBytes uint64
	for _, s := range slices[1] {
		ckeys[s.Id] = s.Size
		total += int64(int(s.Size-1)/chunkConf.BlockSize) + 1
		totalBytes += uint64(s.Size)
	}
	slices[1] = nil
	for _, ss := range slices {
		for _, s := range ss {
			vkeys[s.Id] = s.Size
			total += int64(int(s.Size-1)/chunkConf.BlockSize) + 1 // s.Size should be > 0
			totalBytes += uint64(s.Size)
		}
	}
	if progress.Quiet {
		logger.Infof("using %d slices (%d bytes)", len(vkeys)+len(ckeys), totalBytes)
	}

	bar := progress.AddCountBar("Scanned objects", total)
	valid := progress.AddDoubleSpinner("Valid objects")
	compacted := progress.AddDoubleSpinner("Compacted objects")
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
	for i := 0; i < threads; i++ {
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
		size := vkeys[uint64(cid)]
		var cobj bool
		if size == 0 {
			size = ckeys[uint64(cid)]
			cobj = true
		}
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
			} else if cobj {
				compacted.IncrInt64(obj.Size())
			} else {
				valid.IncrInt64(obj.Size())
			}
		} else {
			if indx*chunkConf.BlockSize+csize != int(size) {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else if cobj {
				compacted.IncrInt64(obj.Size())
			} else {
				valid.IncrInt64(obj.Size())
			}
		}
	}
	m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
		return nil
	})
	if sliceChan != nil {
		close(sliceChan)
	}
	close(leakedObj)
	wg.Wait()
	if delete || compact {
		delSpin.Done()
		if progress.Quiet {
			logger.Infof("Deleted %d pending slices", delSpin.Current())
		}
	}
	sliceCSpin.Done()
	progress.Done()

	vc, _ := valid.Current()
	cc, cb := compacted.Current()
	lc, lb := leaked.Current()
	sc, sb := skipped.Current()
	dsc, dsb := cleanedSliceSpin.Current()
	fc, fb := cleanedFileSpin.Current()
	logger.Infof("scanned %d objects, %d valid, %d compacted (%d bytes), %d leaked (%d bytes), %d delslices (%d bytes), %d delfiles (%d bytes), %d skipped (%d bytes)",
		bar.Current(), vc, cc, cb, lc, lb, dsc, dsb, fc, fb, sc, sb)
	if lc > 0 && !delete {
		logger.Infof("Please add `--delete` to clean leaked objects")
	}
	return nil
}
