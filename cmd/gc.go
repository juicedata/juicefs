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
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

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

# Delete leaked objects or metadata and delayed deleted slices or files
$ juicefs gc redis://localhost --delete

# Use external sort to reduce memory usage on large volumes
$ juicefs gc redis://localhost --external-sort-dir /tmp/gc-work`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "compact",
				Usage: "compact small slices into bigger ones",
			},
			&cli.BoolFlag{
				Name:  "delete",
				Usage: "delete leaked objects or metadata and delayed deleted slices or files",
			},
			&cli.IntFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   10,
				Usage:   "number threads to delete leaked objects",
			},
			&cli.StringFlag{
				Name:  "external-sort-dir",
				Usage: "working directory for external sort temporary files (enables external sort mode)",
			},
		},
	}
}

func gc(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	metaConf := meta.DefaultConf()
	metaConf.MaxDeletes = ctx.Int("threads")
	metaConf.NoBGJob = true
	m := meta.NewClient(ctx.Args().Get(0), metaConf)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	if err = m.NewSession(false); err == nil { // To sync all stats periodically
		defer m.CloseSession() //nolint:errcheck
	} else {
		logger.Fatalf("create session: %v", err)
	}

	chunkConf := *getDefaultChunkConf(format)
	chunkConf.CacheDir = "memory"

	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	store := chunk.NewCachedStore(blob, chunkConf, nil)

	// Scan all chunks first and do compaction if necessary
	progress := utils.NewProgress(false)
	// Delete pending slices while listing all slices
	delFlag := ctx.Bool("delete")
	extSortDir := ctx.String("external-sort-dir")
	threads := ctx.Int("threads")
	compact := ctx.Bool("compact")
	if (delFlag || compact || extSortDir != "") && threads <= 0 {
		logger.Fatal("threads should be greater than 0 to delete, compact, or externally sort objects")
	}
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

	var delSpin *utils.Bar

	if delFlag || compact {
		delSpin = progress.AddCountSpinner("Cleaned pending slices")
		m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
			delSpin.Increment()
			return store.Remove(args[0].(uint64), int(args[1].(uint32)))
		})
	}

	c := meta.WrapContext(ctx.Context)
	delayedFileSpin := progress.AddDoubleSpinnerTwo("Pending deleted files", "Pending deleted data")
	cleanedFileSpin := progress.AddDoubleSpinnerTwo("Cleaned pending files", "Cleaned pending data")
	edge := time.Now().Add(-time.Duration(format.TrashDays) * 24 * time.Hour)
	if delFlag {
		cleanTrashSpin := progress.AddCountSpinner("Cleaned trash")
		_ = m.CleanupTrashBefore(c, edge, cleanTrashSpin.IncrBy, nil)
		cleanTrashSpin.Done()

		cleanDetachedNodeSpin := progress.AddCountSpinner("Cleaned detached nodes")
		m.CleanupDetachedNodesBefore(c, time.Now().Add(-time.Hour*24), cleanDetachedNodeSpin.Increment)
		cleanDetachedNodeSpin.Done()
	}

	err = m.ScanDeletedObject(
		c,
		nil, nil, nil,
		func(_ meta.Ino, size uint64, ts int64) (bool, error) {
			delayedFileSpin.IncrInt64(int64(size))
			if delFlag {
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
		spin := progress.AddDoubleSpinnerTwo("Compacted slices", "Compacted data")
		m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			err := vfs.Compact(chunkConf, store, slices, args[1].(uint64), args[2].(uint8))
			for _, s := range slices {
				spin.IncrInt64(int64(s.Len))
			}
			return err
		})
		if st := m.CompactAll(meta.Background(), ctx.Int("threads"), bar); st == 0 {
			if progress.Quiet {
				cn, b := spin.Current()
				logger.Infof("Compacted %d chunks (%d slices, %d bytes).", bar.Current(), cn, b)
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

	stats := newGcStats(progress, extSortDir != "", delSpin, cleanedFileSpin)
	var gcErr error
	if extSortDir != "" {
		gcErr = gcExternalSort(c, m, &chunkConf, blob, stats, extSortDir, threads, delFlag, compact, edge, maxMtime)
	} else {
		gcErr = gcInMemory(c, m, &chunkConf, blob, stats, threads, delFlag, compact, edge, maxMtime)
	}
	m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
		return errors.New("stop deleting slice")
	})
	return gcErr
}

func gcInMemory(
	c meta.Context,
	m meta.Meta,
	chunkConf *chunk.Config,
	blob object.ObjectStorage,
	stats *gcStats,
	threads int,
	delFlag bool,
	compact bool,
	edge time.Time,
	maxMtime time.Time,
) error {
	// List all slices in metadata engine
	slices := make(map[meta.Ino][]meta.Slice)
	r := m.ScanSlices(c, &meta.ScanSlicesOption{ScanPending: true, Delete: delFlag, Progress: stats.slices.Increment}, func(ino meta.Ino, s meta.Slice) error {
		slices[ino] = append(slices[ino], s)
		return nil
	})
	if r != 0 {
		logger.Fatalf("scan all slices: %s", r)
	}

	err := scanGcTrashSlices(m, c, stats.delayedSlices, stats.cleanedSlices, delFlag, edge)
	if err != nil {
		logger.Fatalf("statistic: %s", err)
	}

	// Scan all objects to find leaked ones
	blob = object.WithPrefix(blob, "chunks/")
	vkeys := make(map[uint64]uint32)
	pkeys := make(map[uint64]uint32)
	ckeys := make(map[uint64]uint32)
	var total int64
	var totalBytes uint64
	for _, s := range slices[0] {
		pkeys[s.Id] = s.Size
		total += int64(int(s.Size-1)/chunkConf.BlockSize) + 1
		totalBytes += uint64(s.Size)
	}
	slices[0] = nil
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
	if stats.progress.Quiet {
		logger.Infof("using %d slices (%d bytes)", len(vkeys)+len(ckeys), totalBytes)
	}

	stats.scanned.SetTotal(total)

	leakedObj, waitLeakedObj := startGcObjectDeleters(c, blob, threads, delFlag)
	objs := make(chan object.Object, max(threads, 1))
	listErr := make(chan error, 1)
	go func() {
		defer close(objs)
		listErr <- scanGcChunkObjects(c, blob, max(threads, 1), chunkConf.HashPrefix, stats.prefixes, func(obj object.Object) error {
			select {
			case objs <- obj:
				return nil
			case <-c.Done():
				return c.Err()
			}
		})
	}()

	foundLeaked := func(obj object.Object) {
		stats.scanned.IncrTotal(1)
		stats.addLeaked(obj.Size())
		if leakedObj != nil {
			leakedObj <- obj.Key()
		}
	}

	for obj := range objs {
		if obj == nil {
			break // failed listing
		}
		var ok bool
		obj, ok = filterGcObject(c, blob, obj, maxMtime, stats.scanned, stats.skipped)
		if !ok {
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
		stats.scanned.Increment()
		cid, _ := strconv.Atoi(parts[0])
		size := vkeys[uint64(cid)]
		var pobj, cobj bool
		if size == 0 {
			size, pobj = pkeys[uint64(cid)]
		}
		if size == 0 {
			size, cobj = ckeys[uint64(cid)]
		}
		if size == 0 {
			logger.Debugf("find leaked object: %s, size: %d", obj.Key(), obj.Size())
			foundLeaked(obj)
			continue
		}
		indx, _ := strconv.Atoi(parts[1])
		csize, _ := strconv.Atoi(parts[2])
		if cobj {
			stats.addObject(gcStateTrash, obj.Size())
		} else if isLeakedBlock(indx, csize, int(size), chunkConf.BlockSize) {
			if csize == chunkConf.BlockSize {
				logger.Warnf("size of slice %d is larger than expected: %d > %d", cid, indx*chunkConf.BlockSize+csize, size)
			} else {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
			}
			foundLeaked(obj)
		} else if pobj {
			stats.addObject(gcStatePending, obj.Size())
		} else {
			stats.addObject(gcStateUsed, obj.Size())
		}
	}
	waitLeakedObj()
	if err := <-listErr; err != nil {
		return errors.Errorf("list all blocks: %s", err)
	}
	stats.slices.Done()
	stats.finish(delFlag, compact)
	return nil
}

const (
	gcStateUsed    uint8 = 0
	gcStatePending uint8 = 1
	gcStateTrash   uint8 = 2
)

type gcStats struct {
	progress      *utils.Progress
	deletedSlices *utils.Bar
	cleanedFiles  *utils.DoubleSpinner
	slices        *utils.Bar
	delayedSlices *utils.DoubleSpinner
	cleanedSlices *utils.DoubleSpinner
	prefixes      *utils.Bar
	scanned       *utils.Bar
	valid         *utils.DoubleSpinner
	pending       *utils.DoubleSpinner
	compacted     *utils.DoubleSpinner
	leaked        *utils.DoubleSpinner
	skipped       *utils.DoubleSpinner
	objects       gcMergeStats
}

func newGcStats(progress *utils.Progress, external bool, deletedSlices *utils.Bar, cleanedFiles *utils.DoubleSpinner) *gcStats {
	stats := &gcStats{
		progress:      progress,
		deletedSlices: deletedSlices,
		cleanedFiles:  cleanedFiles,
		slices:        progress.AddCountSpinner("Listed slices"),
		delayedSlices: progress.AddDoubleSpinnerTwo("Trash slices", "Trash data"),
		cleanedSlices: progress.AddDoubleSpinnerTwo("Cleaned trash slices", "Cleaned trash data"),
		prefixes:      progress.AddCountBar("Listed prefixes", 0),
	}
	if external {
		stats.scanned = progress.AddCountSpinner("Scanned objects")
	} else {
		stats.scanned = progress.AddCountBar("Scanned objects", 0)
	}
	stats.valid = progress.AddDoubleSpinnerTwo("Valid objects", "Valid data")
	stats.pending = progress.AddDoubleSpinnerTwo("Pending delete objects", "Pending delete data")
	stats.compacted = progress.AddDoubleSpinnerTwo("Compacted objects", "Compacted data")
	stats.leaked = progress.AddDoubleSpinnerTwo("Leaked objects", "Leaked data")
	stats.skipped = progress.AddDoubleSpinnerTwo("Skipped objects", "Skipped data")
	return stats
}

func scanGcTrashSlices(m meta.Meta, c meta.Context, delayed, cleaned *utils.DoubleSpinner, delFlag bool, edge time.Time) error {
	err := m.ScanDeletedObject(
		c,
		func(ss []meta.Slice, ts int64) (bool, error) {
			for _, slice := range ss {
				delayed.IncrInt64(int64(slice.Size))
				if delFlag && ts < edge.Unix() {
					cleaned.IncrInt64(int64(slice.Size))
				}
			}
			return delFlag && ts < edge.Unix(), nil
		},
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	delayed.Done()
	cleaned.Done()
	return nil
}

func (s *gcStats) addObject(state uint8, size int64) {
	switch state {
	case gcStatePending:
		addGcObjectStat(&s.objects.pending, s.pending, size)
	case gcStateTrash:
		addGcObjectStat(&s.objects.compacted, s.compacted, size)
	default:
		addGcObjectStat(&s.objects.valid, s.valid, size)
	}
}

func (s *gcStats) addLeaked(size int64) {
	addGcObjectStat(&s.objects.leaked, s.leaked, size)
}

func startGcObjectDeleters(ctx context.Context, blob object.ObjectStorage, threads int, enabled bool) (chan string, func()) {
	if !enabled {
		return nil, func() {}
	}
	leakedObj := make(chan string, 10240)
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range leakedObj {
				if err := blob.Delete(ctx, key); err != nil {
					logger.Warnf("delete %s: %s", key, err)
				}
			}
		}()
	}
	return leakedObj, func() {
		close(leakedObj)
		wg.Wait()
	}
}

func (s *gcStats) finish(delFlag, compact bool) {
	if delFlag || compact {
		s.deletedSlices.Done()
		if s.progress.Quiet {
			logger.Infof("Deleted %d pending slices", s.deletedSlices.Current())
		}
	}
	s.progress.Done()
	logGcSummary(s.scanned.Current(), s.objects, s.cleanedSlices, s.cleanedFiles, s.skipped)
	if s.objects.leaked.count > 0 && !delFlag {
		logger.Infof("Please add `--delete` to clean leaked objects")
	}
}

const gcObjectListBatch = 10000

func scanGcChunkObjects(ctx context.Context, blob object.ObjectStorage, threads int, hashPrefix bool, prefixSpin *utils.Bar, handle func(object.Object) error) error {
	defer prefixSpin.Done()
	prefixes := make(chan string, threads)
	eg, scanCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		defer close(prefixes)
		depth := 2
		if hashPrefix {
			depth = 1
		}
		return walkGcChunkObjectPrefixes(scanCtx, blob, "", depth, true, func(prefix string) error {
			prefixSpin.IncrTotal(1)
			select {
			case prefixes <- prefix:
				return nil
			case <-scanCtx.Done():
				return scanCtx.Err()
			}
		})
	})

	for range threads {
		eg.Go(func() error {
			for {
				select {
				case prefix, ok := <-prefixes:
					if !ok {
						return nil
					}
					if err := scanGcChunkObjectsPrefix(scanCtx, blob, prefix, handle); err != nil {
						return errors.Errorf("list chunks from %s: %s", blob, err)
					}
					prefixSpin.Increment()
				case <-scanCtx.Done():
					return scanCtx.Err()
				}
			}
		})
	}
	return eg.Wait()
}

func walkGcChunkObjectPrefixes(ctx context.Context, blob object.ObjectStorage, prefix string, depth int, allowFallback bool, fn func(string) error) error {
	seenPrefixes := make(map[string]struct{})
	var marker, token string
	firstPage := true
	for {
		objs, hasMore, nextToken, err := blob.List(ctx, prefix, marker, token, "/", gcObjectListBatch, true)
		if err != nil {
			if allowFallback && firstPage {
				logger.Warnf("can't find chunk prefixes: %s, list chunks using single thread", err)
				return fn("")
			}
			return err
		}
		firstPage = false
		if len(objs) > 0 && marker != "" && objs[0].Key() == marker {
			objs = objs[1:]
		}
		if hasMore && len(objs) == 0 && nextToken == token {
			return errors.New("list chunk prefixes made no progress")
		}
		for _, obj := range objs {
			if obj == nil || obj.Key() == "" {
				continue
			}
			key := obj.Key()
			if key == prefix {
				marker = key
				continue
			}
			if obj.IsDir() {
				if _, ok := seenPrefixes[key]; !ok {
					seenPrefixes[key] = struct{}{}
					if depth == 1 {
						if err := fn(key); err != nil {
							return err
						}
					} else {
						if err := walkGcChunkObjectPrefixes(ctx, blob, key, depth-1, false, fn); err != nil {
							return err
						}
					}
				}
			} else if strings.Contains(strings.TrimPrefix(key, prefix), "/") {
				return errors.Errorf("delimiter list returned nested object %s", key)
			}
			marker = key
		}
		if !hasMore {
			break
		}
		token = nextToken
	}
	return nil
}

func scanGcChunkObjectsPrefix(ctx context.Context, blob object.ObjectStorage, prefix string, handle func(object.Object) error) error {
	objs, err := object.ListAll(ctx, blob, prefix, "", true, false)
	if err != nil {
		return errors.Errorf("list chunk prefix %s: %s", prefix, err)
	}
	for obj := range objs {
		if obj == nil {
			return errors.Errorf("list chunk prefix %s failed", prefix)
		}
		if err := handle(obj); err != nil {
			return err
		}
	}
	return nil
}

func filterGcObject(ctx context.Context, blob object.ObjectStorage, obj object.Object, maxMtime time.Time, scanned *utils.Bar, skipped *utils.DoubleSpinner) (object.Object, bool) {
	if obj.IsDir() {
		return nil, false
	}
	if obj.Size() == 0 || obj.Mtime().Unix() == 0 {
		headObj, err := blob.Head(ctx, obj.Key())
		if err != nil {
			logger.Warnf("head %s: %s", obj.Key(), err)
			scanned.Increment()
			skipped.IncrInt64(obj.Size())
			return nil, false
		}
		obj = headObj
	}
	if obj.Mtime().After(maxMtime) {
		logger.Debugf("ignore new block: %s %s", obj.Key(), obj.Mtime())
		scanned.Increment()
		skipped.IncrInt64(obj.Size())
		return nil, false
	}
	return obj, true
}

type gcObjectCounter struct {
	count int64
	bytes int64
}

type gcMergeStats struct {
	valid     gcObjectCounter
	pending   gcObjectCounter
	compacted gcObjectCounter
	leaked    gcObjectCounter
}

func addGcObjectStat(counter *gcObjectCounter, spinner *utils.DoubleSpinner, size int64) {
	counter.count++
	counter.bytes += size
	spinner.IncrInt64(size)
}

func logGcSummary(scanned int64, stats gcMergeStats, cleanedSliceSpin, cleanedFileSpin, skipped *utils.DoubleSpinner) {
	dsc, dsb := cleanedSliceSpin.Current()
	fc, fb := cleanedFileSpin.Current()
	sc, sb := skipped.Current()
	logger.Infof("scanned %d objects, %d valid, %d pending delete (%d bytes), %d compacted (%d bytes), %d leaked (%d bytes), %d delslices (%d bytes), %d delfiles (%d bytes), %d skipped (%d bytes)",
		scanned, stats.valid.count, stats.pending.count, stats.pending.bytes, stats.compacted.count, stats.compacted.bytes,
		stats.leaked.count, stats.leaked.bytes, dsc, dsb, fc, fb, sc, sb)
}

func isLeakedBlock(index, blockSize, sliceSize, configuredBlockSize int) bool {
	if blockSize == configuredBlockSize {
		return (index+1)*blockSize > sliceSize
	}
	return index*configuredBlockSize+blockSize != sliceSize
}
