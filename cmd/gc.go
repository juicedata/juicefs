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
	"cmp"
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	extsort "github.com/juicedata/juicefs/pkg/utils/extsort"
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
$ juicefs gc redis://localhost --work-dir /tmp/gc-work`,
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
				Name:  "work-dir",
				Usage: "working directory for external sort temporary files (enables external sort mode, does not support --delete)",
			},
		},
	}
}

const (
	gcStateUsed    uint8 = 0
	gcStatePending uint8 = 1
	gcStateTrash   uint8 = 2
)

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
	workDir := ctx.String("work-dir")
	if workDir != "" && delFlag {
		logger.Fatal("external sort mode does not support --delete")
	}
	threads := ctx.Int("threads")
	compact := ctx.Bool("compact")
	if (delFlag || compact) && threads <= 0 {
		logger.Fatal("threads should be greater than 0 to delete or compact objects")
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

	var wg sync.WaitGroup
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

	if workDir != "" {
		return gcExternalSort(ctx.Context, m, &chunkConf, blob, progress, c, delSpin, cleanedFileSpin, workDir, threads, compact, maxMtime)
	}

	// put it above delete count spinner
	sliceCSpin := progress.AddCountSpinner("Listed slices")

	// List all slices in metadata engine
	slices := make(map[meta.Ino][]meta.Slice)
	r := m.ListSlices(c, slices, true, delFlag, sliceCSpin.Increment)
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}

	delayedSliceSpin := progress.AddDoubleSpinnerTwo("Trash slices", "Trash data")
	cleanedSliceSpin := progress.AddDoubleSpinnerTwo("Cleaned trash slices", "Cleaned trash data")

	err = m.ScanDeletedObject(
		c,
		func(ss []meta.Slice, ts int64) (bool, error) {
			for _, s := range ss {
				delayedSliceSpin.IncrInt64(int64(s.Size))
				if delFlag && ts < edge.Unix() {
					cleanedSliceSpin.IncrInt64(int64(s.Size))
				}
			}
			if delFlag && ts < edge.Unix() {
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
	objs, err := object.ListAll(ctx.Context, blob, "", "", true, false)
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}
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
	if progress.Quiet {
		logger.Infof("using %d slices (%d bytes)", len(vkeys)+len(ckeys), totalBytes)
	}

	bar := progress.AddCountBar("Scanned objects", total)
	valid := progress.AddDoubleSpinnerTwo("Valid objects", "Valid data")
	pending := progress.AddDoubleSpinnerTwo("Pending delete objects", "Pending delete data")
	compacted := progress.AddDoubleSpinnerTwo("Compacted objects", "Compacted data")
	leaked := progress.AddDoubleSpinnerTwo("Leaked objects", "Leaked data")
	skipped := progress.AddDoubleSpinnerTwo("Skipped objects", "Skipped data")
	var stats gcMergeStats

	var leakedObj = make(chan string, 10240)
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range leakedObj {
				if err := blob.Delete(ctx.Context, key); err != nil {
					logger.Warnf("delete %s: %s", key, err)
				}
			}
		}()
	}

	foundLeaked := func(obj object.Object) {
		bar.IncrTotal(1)
		addGcObjectStat(&stats.leaked, leaked, obj.Size())
		if delFlag {
			leakedObj <- obj.Key()
		}
	}

	for obj := range objs {
		if obj == nil {
			break // failed listing
		}
		var ok bool
		obj, ok = prepareGcObject(ctx.Context, blob, obj, maxMtime, bar, skipped)
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
		bar.Increment()
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
		if isLeakedBlock(indx, csize, int(size), chunkConf.BlockSize) {
			if csize == chunkConf.BlockSize {
				logger.Warnf("size of slice %d is larger than expected: %d > %d", cid, indx*chunkConf.BlockSize+csize, size)
			} else {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
			}
			foundLeaked(obj)
		} else if pobj {
			addGcObjectStat(&stats.pending, pending, obj.Size())
		} else if cobj {
			addGcObjectStat(&stats.compacted, compacted, obj.Size())
		} else {
			addGcObjectStat(&stats.valid, valid, obj.Size())
		}
	}
	m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
		return errors.New("stop deleting slice")
	})
	close(leakedObj)
	wg.Wait()
	if delFlag || compact {
		delSpin.Done()
		if progress.Quiet {
			logger.Infof("Deleted %d pending slices", delSpin.Current())
		}
	}
	sliceCSpin.Done()
	progress.Done()

	logGcSummary(bar.Current(), stats, cleanedSliceSpin, cleanedFileSpin, skipped)
	if stats.leaked.count > 0 && !delFlag {
		logger.Infof("Please add `--delete` to clean leaked objects")
	}
	return nil
}

// gcExternalSort runs GC using external sort to avoid loading all slices into memory.
func gcExternalSort(
	ctx context.Context,
	m meta.Meta,
	chunkConf *chunk.Config,
	blob object.ObjectStorage,
	progress *utils.Progress,
	c meta.Context,
	delSpin *utils.Bar,
	cleanedFileSpin *utils.DoubleSpinner,
	workDir string,
	threads int,
	compact bool,
	maxMtime time.Time,
) error {
	logger.Infof("Using external sort mode, work dir: %s", workDir)

	blobWithPrefix := object.WithPrefix(blob, "chunks/")
	metaSorter, objSorter, err := newGcExternalSorters(ctx, workDir, threads)
	if err != nil {
		logger.Fatalf("%s", err)
	}

	metaSliceSpin := progress.AddCountSpinner("Listed slices")
	delayedSliceSpin := progress.AddDoubleSpinnerTwo("Trash slices", "Trash data")
	cleanedSliceSpin := progress.AddDoubleSpinnerTwo("Cleaned trash slices", "Cleaned trash data")
	objScanSpin := progress.AddCountSpinner("Scanned objects")
	valid := progress.AddDoubleSpinnerTwo("Valid objects", "Valid data")
	pending := progress.AddDoubleSpinnerTwo("Pending delete objects", "Pending delete data")
	compacted := progress.AddDoubleSpinnerTwo("Compacted objects", "Compacted data")
	leaked := progress.AddDoubleSpinnerTwo("Leaked objects", "Leaked data")
	skipped := progress.AddDoubleSpinnerTwo("Skipped objects", "Skipped data")

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		defer metaSorter.CloseInputs()
		return scanGcMetaRecords(egCtx, m, c, metaSorter, metaSliceSpin)
	})

	eg.Go(func() error {
		defer objSorter.CloseInputs()
		return scanGcObjectRecords(egCtx, blobWithPrefix, objSorter, threads, maxMtime, objScanSpin, skipped)
	})

	if err := eg.Wait(); err != nil {
		_ = metaSorter.Done()
		_ = objSorter.Done()
		return errors.Errorf("producer error: %s", err)
	}

	stats := mergeGcSortedRecords(metaSorter.Outputs(), objSorter.Outputs(), chunkConf.BlockSize, valid, pending, compacted, leaked)

	metaSortErr := metaSorter.Done()
	objSortErr := objSorter.Done()
	if metaSortErr != nil {
		return errors.Errorf("sort meta records: %s", metaSortErr)
	}
	if objSortErr != nil {
		return errors.Errorf("sort object records: %s", objSortErr)
	}

	err = m.ScanDeletedObject(
		c,
		func(ss []meta.Slice, _ int64) (bool, error) {
			for _, s := range ss {
				delayedSliceSpin.IncrInt64(int64(s.Size))
			}
			return false, nil
		},
		nil, nil, nil,
	)
	if err != nil {
		logger.Fatalf("trash slice scan: %s", err)
	}
	delayedSliceSpin.Done()
	cleanedSliceSpin.Done()

	if compact {
		delSpin.Done()
		if progress.Quiet {
			logger.Infof("Deleted %d pending slices", delSpin.Current())
		}
	}
	progress.Done()

	logGcSummary(objScanSpin.Current(), stats, cleanedSliceSpin, cleanedFileSpin, skipped)
	if stats.leaked.count > 0 {
		logger.Infof("Please rerun without `--work-dir` and add `--delete` to clean leaked objects")
	}
	return nil
}

func newGcExternalSorters(ctx context.Context, workDir string, threads int) (*extsort.Sharded[gcMetaRecord], *extsort.Sharded[gcObjectRecord], error) {
	metaSorter, err := extsort.NewSharded(ctx, extsort.Config{
		WorkDir: workDir,
		Name:    "gc-meta",
		Threads: threads,
	}, extsort.Codec[gcMetaRecord]{
		FromBytes: gcMetaRecordFromBytes,
		ToBytes:   gcMetaRecordToBytes,
		Compare:   compareGcMetaRecord,
	})
	if err != nil {
		return nil, nil, errors.Errorf("create meta sorter: %s", err)
	}

	objSorter, err := extsort.NewSharded(ctx, extsort.Config{
		WorkDir: workDir,
		Name:    "gc-object",
		Threads: threads,
	}, extsort.Codec[gcObjectRecord]{
		FromBytes: gcObjectRecordFromBytes,
		ToBytes:   gcObjectRecordToBytes,
		Compare:   compareGcObjectRecord,
	})
	if err != nil {
		metaSorter.CloseInputs()
		_ = metaSorter.Done()
		return nil, nil, errors.Errorf("create object sorter: %s", err)
	}
	return metaSorter, objSorter, nil
}

func scanGcMetaRecords(ctx context.Context, m meta.Meta, c meta.Context, sorter *extsort.Sharded[gcMetaRecord], metaSliceSpin *utils.Bar) error {
	st := m.ScanSlices(c, true, false, nil, func(ino meta.Ino, s meta.Slice) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		metaSliceSpin.Increment()
		var state uint8
		switch ino {
		case 0:
			state = gcStatePending
		case 1:
			state = gcStateTrash
		default:
			state = gcStateUsed
		}
		select {
		case sorter.InputFor(s.Id) <- gcMetaRecord{sliceID: s.Id, size: s.Size, state: state}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if st != 0 {
		return errors.Errorf("scan slices: %s", st)
	}
	return nil
}

func scanGcObjectRecords(ctx context.Context, blob object.ObjectStorage, sorter *extsort.Sharded[gcObjectRecord], threads int, maxMtime time.Time, objScanSpin *utils.Bar, skipped *utils.DoubleSpinner) error {
	return scanGcChunkObjects(ctx, blob, threads, func(obj object.Object) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var ok bool
		obj, ok = prepareGcObject(ctx, blob, obj, maxMtime, objScanSpin, skipped)
		if !ok {
			return nil
		}
		record, ok := parseGcObjectRecord(obj)
		if !ok {
			return nil
		}
		select {
		case sorter.InputFor(record.sliceID) <- record:
		case <-ctx.Done():
			return ctx.Err()
		}
		objScanSpin.Increment()
		return nil
	})
}

const gcObjectListBatch = 10000

func scanGcChunkObjects(ctx context.Context, blob object.ObjectStorage, threads int, handle func(object.Object) error) error {
	prefixes, err := listGcChunkObjectPrefixes(ctx, blob)
	if err != nil {
		if err != nil {
			logger.Warnf("can't find chunk prefixes: %s, list chunks using single thread", err)
		}
		return scanGcChunkObjectsPrefix(ctx, blob, "", handle)
	}
	if len(prefixes) == 0 {
		return nil
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i] > prefixes[j] })
	control := make(chan bool, threads)
	var wg sync.WaitGroup

	for _, prefix := range prefixes {
		control <- true
		wg.Add(1)
		go func(prefix string) {
			defer wg.Done()
			e := scanGcChunkObjectsPrefix(ctx, blob, prefix, handle)
			<-control
			if e != nil {
				logger.Errorf("list chunks from %s: %s", blob, e)
				err = errors.Errorf("list chunks from %s: %s", blob, e)
			}
		}(prefix)
	}
	wg.Wait()
	return err
}

func listGcChunkObjectPrefixes(ctx context.Context, blob object.ObjectStorage) ([]string, error) {
	var prefixes []string
	seenPrefixes := make(map[string]struct{})
	var marker, token string
	for {
		objs, hasMore, nextToken, err := blob.List(ctx, "", marker, token, "/", gcObjectListBatch, true)
		if err != nil {
			return nil, err
		}
		if len(objs) > 0 && marker != "" && objs[0].Key() == marker {
			objs = objs[1:]
		}
		if hasMore && len(objs) == 0 && nextToken == token {
			return nil, errors.New("list chunk prefixes made no progress")
		}
		for _, obj := range objs {
			if obj == nil || obj.Key() == "" {
				continue
			}
			key := obj.Key()
			if obj.IsDir() {
				if _, ok := seenPrefixes[key]; !ok {
					prefixes = append(prefixes, key)
					seenPrefixes[key] = struct{}{}
				}
			} else {
				if strings.Contains(key, "/") {
					return nil, errors.Errorf("delimiter list returned nested object %s", key)
				}
			}
			marker = key
		}
		if !hasMore {
			break
		}
		token = nextToken
	}
	return prefixes, nil
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

func prepareGcObject(ctx context.Context, blob object.ObjectStorage, obj object.Object, maxMtime time.Time, scanned *utils.Bar, skipped *utils.DoubleSpinner) (object.Object, bool) {
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

func parseGcObjectRecord(obj object.Object) (gcObjectRecord, bool) {
	parts := strings.Split(obj.Key(), "/")
	if len(parts) != 3 {
		return gcObjectRecord{}, false
	}
	nameParts := strings.Split(parts[2], "_")
	if len(nameParts) != 3 {
		return gcObjectRecord{}, false
	}
	sliceID, err := strconv.ParseUint(nameParts[0], 10, 64)
	if err != nil {
		return gcObjectRecord{}, false
	}
	index, err := strconv.Atoi(nameParts[1])
	if err != nil {
		return gcObjectRecord{}, false
	}
	blockSize, err := strconv.Atoi(nameParts[2])
	if err != nil {
		return gcObjectRecord{}, false
	}
	return gcObjectRecord{sliceID: sliceID, index: index, blockSize: blockSize, objectSize: obj.Size()}, true
}

type gcMetaRecord struct {
	sliceID uint64
	size    uint32
	state   uint8
}

const (
	gcMetaRecordSize   = 13
	gcObjectRecordSize = 24
)

func gcMetaRecordFromBytes(data []byte) (gcMetaRecord, error) {
	if len(data) != gcMetaRecordSize {
		return gcMetaRecord{}, errors.Errorf("invalid gc meta record size: %d", len(data))
	}
	rb := utils.FromBuffer(data)
	return gcMetaRecord{
		sliceID: rb.Get64(),
		size:    rb.Get32(),
		state:   rb.Get8(),
	}, nil
}

func gcMetaRecordToBytes(r gcMetaRecord) ([]byte, error) {
	wb := utils.NewBuffer(gcMetaRecordSize)
	wb.Put64(r.sliceID)
	wb.Put32(r.size)
	wb.Put8(r.state)
	return wb.Bytes(), nil
}

func compareGcMetaRecord(a, b gcMetaRecord) int {
	if c := cmp.Compare(a.sliceID, b.sliceID); c != 0 {
		return c
	}
	return cmp.Compare(a.state, b.state)
}

type gcObjectRecord struct {
	sliceID    uint64
	index      int
	blockSize  int
	objectSize int64
}

func gcObjectRecordFromBytes(data []byte) (gcObjectRecord, error) {
	if len(data) != gcObjectRecordSize {
		return gcObjectRecord{}, errors.Errorf("invalid gc object record size: %d", len(data))
	}
	rb := utils.FromBuffer(data)
	return gcObjectRecord{
		sliceID:    rb.Get64(),
		index:      int(rb.Get32()),
		blockSize:  int(rb.Get32()),
		objectSize: int64(rb.Get64()),
	}, nil
}

func gcObjectRecordToBytes(r gcObjectRecord) ([]byte, error) {
	wb := utils.NewBuffer(gcObjectRecordSize)
	wb.Put64(r.sliceID)
	wb.Put32(uint32(r.index))
	wb.Put32(uint32(r.blockSize))
	wb.Put64(uint64(r.objectSize))
	return wb.Bytes(), nil
}

func compareGcObjectRecord(a, b gcObjectRecord) int {
	if c := cmp.Compare(a.sliceID, b.sliceID); c != 0 {
		return c
	}
	if c := cmp.Compare(a.index, b.index); c != 0 {
		return c
	}
	return cmp.Compare(a.blockSize, b.blockSize)
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

func (s *gcMergeStats) add(other gcMergeStats) {
	s.valid.count += other.valid.count
	s.valid.bytes += other.valid.bytes
	s.pending.count += other.pending.count
	s.pending.bytes += other.pending.bytes
	s.compacted.count += other.compacted.count
	s.compacted.bytes += other.compacted.bytes
	s.leaked.count += other.leaked.count
	s.leaked.bytes += other.leaked.bytes
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

func mergeGcSortedRecords(
	metaStreams []<-chan gcMetaRecord,
	objStreams []<-chan gcObjectRecord,
	blockSize int,
	valid, pending, compacted, leaked *utils.DoubleSpinner,
) gcMergeStats {
	statsByShard := make([]gcMergeStats, len(metaStreams))
	var wg sync.WaitGroup
	for i := range metaStreams {
		wg.Add(1)
		go func(shard int) {
			defer wg.Done()
			statsByShard[shard] = mergeShard(metaStreams[shard], objStreams[shard], blockSize, valid, pending, compacted, leaked)
		}(i)
	}
	wg.Wait()

	var stats gcMergeStats
	for _, shardStats := range statsByShard {
		stats.add(shardStats)
	}
	return stats
}

func mergeShard(
	metaStream <-chan gcMetaRecord,
	objStream <-chan gcObjectRecord,
	blockSize int,
	valid, pending, compacted, leaked *utils.DoubleSpinner,
) gcMergeStats {
	var stats gcMergeStats

	type metaGroup struct {
		sliceID uint64
		size    uint32
		state   uint8
	}

	var peeked gcMetaRecord
	var hasPeeked bool

	nextMetaGroup := func() (metaGroup, bool) {
		var r gcMetaRecord
		var ok bool
		if hasPeeked {
			r = peeked
			hasPeeked = false
		} else {
			r, ok = <-metaStream
			if !ok {
				return metaGroup{}, false
			}
		}
		group := metaGroup{sliceID: r.sliceID, size: r.size, state: r.state}
		for {
			r, ok = <-metaStream
			if !ok {
				return group, true
			}
			if r.sliceID != group.sliceID {
				peeked = r
				hasPeeked = true
				return group, true
			}
			if r.state < group.state {
				group.state = r.state
				group.size = r.size
			}
		}
	}

	meta, ok := nextMetaGroup()
	markLeaked := func(obj gcObjectRecord) {
		addGcObjectStat(&stats.leaked, leaked, obj.objectSize)
	}

	for obj := range objStream {
		for ok && meta.sliceID < obj.sliceID {
			meta, ok = nextMetaGroup()
		}

		if !ok || meta.sliceID > obj.sliceID {
			markLeaked(obj)
			continue
		}

		if isLeakedBlock(obj.index, obj.blockSize, int(meta.size), blockSize) {
			markLeaked(obj)
		} else {
			switch meta.state {
			case gcStatePending:
				addGcObjectStat(&stats.pending, pending, obj.objectSize)
			case gcStateTrash:
				addGcObjectStat(&stats.compacted, compacted, obj.objectSize)
			default:
				addGcObjectStat(&stats.valid, valid, obj.objectSize)
			}
		}
	}
	return stats
}

func isLeakedBlock(index, blockSize, sliceSize, configuredBlockSize int) bool {
	if blockSize == configuredBlockSize {
		return (index+1)*blockSize > sliceSize
	}
	return index*configuredBlockSize+blockSize != sliceSize
}
