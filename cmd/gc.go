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
	"encoding/binary"
	"os"
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
	maxMtime := time.Now()
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
		return gcExternalSort(ctx.Context, m, &chunkConf, blob, progress, c, delSpin, cleanedFileSpin, workDir, compact, maxMtime)
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
		leaked.IncrInt64(obj.Size())
		if delFlag {
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
		if obj.Size() == 0 || obj.Mtime().Unix() == 0 {
			headObj, err := blob.Head(ctx.Context, obj.Key())
			if err != nil {
				logger.Warnf("head %s: %s", obj.Key(), err)
				bar.Increment()
				skipped.IncrInt64(obj.Size())
				continue
			}
			obj = headObj
		}
		if obj.Mtime().After(maxMtime) {
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
		if csize == chunkConf.BlockSize {
			if (indx+1)*csize > int(size) {
				logger.Warnf("size of slice %d is larger than expected: %d > %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else if pobj {
				pending.IncrInt64(obj.Size())
			} else if cobj {
				compacted.IncrInt64(obj.Size())
			} else {
				valid.IncrInt64(obj.Size())
			}
		} else {
			if indx*chunkConf.BlockSize+csize != int(size) {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else if pobj {
				pending.IncrInt64(obj.Size())
			} else if cobj {
				compacted.IncrInt64(obj.Size())
			} else {
				valid.IncrInt64(obj.Size())
			}
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

	vc, _ := valid.Current()
	pc, pb := pending.Current()
	cc, cb := compacted.Current()
	lc, lb := leaked.Current()
	sc, sb := skipped.Current()
	dsc, dsb := cleanedSliceSpin.Current()
	fc, fb := cleanedFileSpin.Current()
	logger.Infof("scanned %d objects, %d valid, %d pending delete (%d bytes), %d compacted (%d bytes), %d leaked (%d bytes), %d delslices (%d bytes), %d delfiles (%d bytes), %d skipped (%d bytes)",
		bar.Current(), vc, pc, pb, cc, cb, lc, lb, dsc, dsb, fc, fb, sc, sb)
	if lc > 0 && !delFlag {
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
	compact bool,
	maxMtime time.Time,
) error {
	logger.Infof("Using external sort mode, work dir: %s", workDir)

	metaCodec := newGCMetaCodec()
	objCodec := newGCObjectCodec()

	blobWithPrefix := object.WithPrefix(blob, "chunks/")

	metaSorter, err := extsort.NewSharded(ctx, extsort.Config{
		WorkDir: workDir,
		Name:    "gc-meta",
	}, extsort.Codec{Compare: metaCodec.compare})
	if err != nil {
		logger.Fatalf("create meta sorter: %s", err)
	}

	objSorter, err := extsort.NewSharded(ctx, extsort.Config{
		WorkDir: workDir,
		Name:    "gc-object",
	}, extsort.Codec{Compare: objCodec.compare})
	if err != nil {
		metaSorter.CloseInputs()
		_ = metaSorter.Done()
		logger.Fatalf("create object sorter: %s", err)
	}

	metaSliceSpin := progress.AddCountSpinner("Scanned slices (meta)")
	objScanSpin := progress.AddCountSpinner("Scanned objects")
	valid := progress.AddDoubleSpinnerTwo("Valid objects", "Valid data")
	pending := progress.AddDoubleSpinnerTwo("Pending delete objects", "Pending delete data")
	compacted := progress.AddDoubleSpinnerTwo("Compacted objects", "Compacted data")
	leaked := progress.AddDoubleSpinnerTwo("Leaked objects", "Leaked data")
	skipped := progress.AddDoubleSpinnerTwo("Skipped objects", "Skipped data")

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		defer metaSorter.CloseInputs()
		st := m.ScanSlices(c, true, false, metaSliceSpin.Increment, func(ino meta.Ino, s meta.Slice) error {
			select {
			case <-egCtx.Done():
				return egCtx.Err()
			default:
			}
			var state uint8
			switch ino {
			case 0:
				state = gcStatePending
			case 1:
				state = gcStateTrash
			default:
				state = gcStateUsed
			}
			data := metaCodec.encode(s.Id, s.Size, state)
			metaSorter.InputFor(s.Id) <- data
			return nil
		})
		if st != 0 {
			return errors.Errorf("scan slices: %s", st)
		}
		return nil
	})

	eg.Go(func() error {
		defer objSorter.CloseInputs()
		objs, listErr := object.ListAll(egCtx, blobWithPrefix, "", "", true, false)
		if listErr != nil {
			return errors.Errorf("list all blocks: %s", listErr)
		}
		for obj := range objs {
			if obj == nil {
				break
			}
			select {
			case <-egCtx.Done():
				return egCtx.Err()
			default:
			}
			if obj.IsDir() {
				continue
			}
			if obj.Size() == 0 || obj.Mtime().Unix() == 0 {
				headObj, err := blobWithPrefix.Head(egCtx, obj.Key())
				if err != nil {
					logger.Warnf("head %s: %s", obj.Key(), err)
					objScanSpin.Increment()
					skipped.IncrInt64(obj.Size())
					continue
				}
				obj = headObj
			}
			if obj.Mtime().After(maxMtime) {
				logger.Debugf("ignore new block: %s %s", obj.Key(), obj.Mtime())
				objScanSpin.Increment()
				skipped.IncrInt64(obj.Size())
				continue
			}

			// Parse object key: dir/dir/cid_index_size
			parts := strings.Split(obj.Key(), "/")
			if len(parts) != 3 {
				continue
			}
			nameParts := strings.Split(parts[2], "_")
			if len(nameParts) != 3 {
				continue
			}
			cid, err := strconv.ParseUint(nameParts[0], 10, 64)
			if err != nil {
				continue
			}
			index, err := strconv.Atoi(nameParts[1])
			if err != nil {
				continue
			}
			bsize, err := strconv.Atoi(nameParts[2])
			if err != nil {
				continue
			}

			data := objCodec.encode(cid, index, bsize, obj.Size(), obj.Key())
			objSorter.InputFor(cid) <- data
			objScanSpin.Increment()
		}
		return nil
	})

	// Wait for both producers; errors from either will cancel the context
	if err := eg.Wait(); err != nil {
		_ = metaSorter.Done()
		_ = objSorter.Done()
		return errors.Errorf("producer error: %s", err)
	}

	var lock sync.Mutex
	var validCount, validBytes int64
	var pendingCount, pendingBytes int64
	var compactedCount, compactedBytes int64
	var leakedCount, leakedBytes int64

	metaOutputs := metaSorter.Outputs()
	objOutputs := objSorter.Outputs()

	var mergeWg sync.WaitGroup
	blockSize := chunkConf.BlockSize

	for i := 0; i < len(metaOutputs); i++ {
		mergeWg.Add(1)
		go func(shard int) {
			defer mergeWg.Done()
			mergeShard(metaOutputs[shard], objOutputs[shard], blockSize,
				&lock, &validCount, &validBytes,
				&pendingCount, &pendingBytes,
				&compactedCount, &compactedBytes,
				&leakedCount, &leakedBytes,
				valid, pending, compacted, leaked)
		}(i)
	}

	mergeWg.Wait()

	metaSortErr := metaSorter.Done()
	objSortErr := objSorter.Done()
	if metaSortErr != nil {
		return errors.Errorf("sort meta records: %s", metaSortErr)
	}
	if objSortErr != nil {
		return errors.Errorf("sort object records: %s", objSortErr)
	}

	delayedSliceSpin := progress.AddDoubleSpinnerTwo("Trash slices", "Trash data")
	cleanedSliceSpin := progress.AddDoubleSpinnerTwo("Cleaned trash slices", "Cleaned trash data")

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

	vc, _ := valid.Current()
	dsc, dsb := cleanedSliceSpin.Current()
	fc, fb := cleanedFileSpin.Current()
	sc, sb := skipped.Current()
	logger.Infof("scanned %d objects, %d valid, %d pending delete (%d bytes), %d compacted (%d bytes), %d leaked (%d bytes), %d delslices (%d bytes), %d delfiles (%d bytes), %d skipped (%d bytes)",
		objScanSpin.Current(), vc, pendingCount, pendingBytes, compactedCount, compactedBytes,
		leakedCount, leakedBytes, dsc, dsb, fc, fb, sc, sb)
	if leakedCount > 0 {
		logger.Infof("Please rerun without `--work-dir` and add `--delete` to clean leaked objects")
	}
	return nil
}

type gcMetaCodec struct{}

func newGCMetaCodec() *gcMetaCodec { return &gcMetaCodec{} }

func (c *gcMetaCodec) encode(sliceID uint64, size uint32, state uint8) []byte {
	buf := make([]byte, 13)
	binary.BigEndian.PutUint64(buf[0:8], sliceID)
	binary.BigEndian.PutUint32(buf[8:12], size)
	buf[12] = state
	return buf
}

func (c *gcMetaCodec) decode(b []byte) (sliceID uint64, size uint32, state uint8) {
	return binary.BigEndian.Uint64(b[0:8]), binary.BigEndian.Uint32(b[8:12]), b[12]
}

func (c *gcMetaCodec) compare(a, b []byte) int {
	aID := binary.BigEndian.Uint64(a[0:8])
	bID := binary.BigEndian.Uint64(b[0:8])
	if aID < bID {
		return -1
	}
	if aID > bID {
		return 1
	}
	// Same SliceID: sort by state (used < pending < trash)
	aState := a[12]
	bState := b[12]
	if aState < bState {
		return -1
	}
	if aState > bState {
		return 1
	}
	return 0
}

type gcObjectCodec struct{}

func newGCObjectCodec() *gcObjectCodec { return &gcObjectCodec{} }

func (c *gcObjectCodec) encode(sliceID uint64, index int, bsize int, objSize int64, key string) []byte {
	keyLen := len(key)
	buf := make([]byte, 8+4+4+8+2+keyLen)
	binary.BigEndian.PutUint64(buf[0:8], sliceID)
	binary.BigEndian.PutUint32(buf[8:12], uint32(index))
	binary.BigEndian.PutUint32(buf[12:16], uint32(bsize))
	binary.BigEndian.PutUint64(buf[16:24], uint64(objSize))
	binary.BigEndian.PutUint16(buf[24:26], uint16(keyLen))
	copy(buf[26:], key)
	return buf
}

func (c *gcObjectCodec) decode(b []byte) (sliceID uint64, index int32, bsize int32, objSize int64, key string) {
	sliceID = binary.BigEndian.Uint64(b[0:8])
	index = int32(binary.BigEndian.Uint32(b[8:12]))
	bsize = int32(binary.BigEndian.Uint32(b[12:16]))
	objSize = int64(binary.BigEndian.Uint64(b[16:24]))
	keyLen := binary.BigEndian.Uint16(b[24:26])
	key = string(b[26 : 26+keyLen])
	return
}

func (c *gcObjectCodec) compare(a, b []byte) int {
	aID := binary.BigEndian.Uint64(a[0:8])
	bID := binary.BigEndian.Uint64(b[0:8])
	if aID < bID {
		return -1
	}
	if aID > bID {
		return 1
	}
	// tie-break on index, block size, then key
	aIdx := binary.BigEndian.Uint32(a[8:12])
	bIdx := binary.BigEndian.Uint32(b[8:12])
	if aIdx < bIdx {
		return -1
	}
	if aIdx > bIdx {
		return 1
	}
	aSz := binary.BigEndian.Uint32(a[12:16])
	bSz := binary.BigEndian.Uint32(b[12:16])
	if aSz < bSz {
		return -1
	}
	if aSz > bSz {
		return 1
	}
	aKeyLen := binary.BigEndian.Uint16(a[24:26])
	bKeyLen := binary.BigEndian.Uint16(b[24:26])
	if string(a[26:26+aKeyLen]) < string(b[26:26+bKeyLen]) {
		return -1
	}
	if string(a[26:26+aKeyLen]) > string(b[26:26+bKeyLen]) {
		return 1
	}
	return 0
}

func mergeShard(
	metaStream, objStream <-chan []byte,
	blockSize int,
	lock *sync.Mutex,
	validCount, validBytes *int64,
	pendingCount, pendingBytes *int64,
	compactedCount, compactedBytes *int64,
	leakedCount, leakedBytes *int64,
	valid, pending, compacted, leaked *utils.DoubleSpinner,
) {
	metaCodec := newGCMetaCodec()
	objCodec := newGCObjectCodec()

	var curSliceID uint64
	var curSize uint32
	var curState uint8
	var metaEOF bool
	var peeked []byte

	readMetaGroup := func() {
		if metaEOF {
			return
		}
		var data []byte
		var ok bool
		if peeked != nil {
			data = peeked
			peeked = nil
		} else {
			data, ok = <-metaStream
			if !ok {
				metaEOF = true
				return
			}
		}
		curSliceID, curSize, curState = metaCodec.decode(data)
		for {
			data, ok = <-metaStream
			if !ok {
				return
			}
			sid, sz, st := metaCodec.decode(data)
			if sid != curSliceID {
				peeked = data
				return
			}
			if st < curState {
				curState = st
				curSize = sz
			}
		}
	}

	readMetaGroup()

	for objData := range objStream {
		objSliceID, objIndex, objBsize, objObjSize, _ := objCodec.decode(objData)

		for !metaEOF && curSliceID < objSliceID {
			readMetaGroup()
		}

		if metaEOF || curSliceID > objSliceID {
			lock.Lock()
			*leakedCount++
			*leakedBytes += objObjSize
			leaked.IncrInt64(objObjSize)
			lock.Unlock()
			continue
		}

		if isLeakedBlock(int(objIndex), int(objBsize), int(curSize), blockSize) {
			lock.Lock()
			*leakedCount++
			*leakedBytes += objObjSize
			leaked.IncrInt64(objObjSize)
			lock.Unlock()
		} else {
			switch curState {
			case gcStatePending:
				lock.Lock()
				*pendingCount++
				*pendingBytes += objObjSize
				pending.IncrInt64(objObjSize)
				lock.Unlock()
			case gcStateTrash:
				lock.Lock()
				*compactedCount++
				*compactedBytes += objObjSize
				compacted.IncrInt64(objObjSize)
				lock.Unlock()
			default:
				lock.Lock()
				*validCount++
				*validBytes += objObjSize
				valid.IncrInt64(objObjSize)
				lock.Unlock()
			}
		}
	}
}

func isLeakedBlock(index, blockSize, sliceSize, configuredBlockSize int) bool {
	if blockSize == configuredBlockSize {
		return (index+1)*blockSize > sliceSize
	}
	return index*configuredBlockSize+blockSize != sliceSize
}
