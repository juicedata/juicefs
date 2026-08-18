/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/utils/extsort"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

// gcExternalSort runs GC using external sort to avoid loading all slices into memory.
func gcExternalSort(
	c meta.Context,
	m meta.Meta,
	chunkConf *chunk.Config,
	blob object.ObjectStorage,
	stats *gcStats,
	extSortDir string,
	threads int,
	delFlag bool,
	compact bool,
	edge time.Time,
	maxMtime time.Time,
) error {
	logger.Infof("Using external sort mode, dir: %s", extSortDir)

	eg, sortCtx := errgroup.WithContext(c)
	sortMetaCtx := meta.WrapWithoutCancel(sortCtx, c.Pid(), c.Uid(), c.Gids())
	blobWithPrefix := object.WithPrefix(blob, "chunks/")
	metaSorter, objSorter, err := newGcExternalSorters(sortCtx, extSortDir, threads)
	if err != nil {
		return err
	}

	leakedObj, waitLeakedObj := startGcObjectDeleters(c, blobWithPrefix, threads, delFlag)

	eg.Go(func() error {
		defer stats.slices.Done()
		if err := scanGcMetaRecords(sortMetaCtx, m, metaSorter.Input(), stats.slices, delFlag); err != nil {
			return errors.Errorf("produce meta records: %s", err)
		}
		metaSorter.CloseInput()
		return nil
	})

	eg.Go(func() error {
		defer stats.scanned.Done()
		if err := scanGcObjectRecords(sortCtx, blobWithPrefix, objSorter.Input(), threads, chunkConf.HashPrefix, maxMtime, stats.prefixes, stats.scanned, stats.skipped); err != nil {
			return errors.Errorf("produce object records: %s", err)
		}
		objSorter.CloseInput()
		return nil
	})

	eg.Go(func() error {
		if err := mergeGcSortedRecords(sortCtx, metaSorter, objSorter, chunkConf.BlockSize, stats, leakedObj); err != nil {
			return errors.Errorf("merge sorted records: %s", err)
		}
		return nil
	})

	sortErr := eg.Wait()
	_ = metaSorter.Done()
	_ = objSorter.Done()
	waitLeakedObj()
	if sortErr != nil {
		return sortErr
	}
	if err := scanTrashSlices(c, m, stats, delFlag, edge); err != nil {
		return errors.Errorf("trash slice scan: %s", err)
	}
	stats.finish(delFlag, compact)
	return nil
}

func newGcExternalSorters(ctx context.Context, extSortDir string, threads int) (*extsort.Sorter[gcMetaRecord], *extsort.Sorter[gcObjectRecord], error) {
	metaSorter, err := extsort.New(ctx, extsort.Config{
		WorkDir:  extSortDir,
		Name:     "gc-meta",
		Threads:  threads,
		Checksum: true,
	}, extsort.Codec[gcMetaRecord]{
		FromBytes: gcMetaRecordFromBytes,
		ToBytes:   gcMetaRecordToBytes,
		Compare:   compareGcMetaRecord,
	})
	if err != nil {
		return nil, nil, errors.Errorf("create meta sorter: %s", err)
	}

	objSorter, err := extsort.New(ctx, extsort.Config{
		WorkDir:  extSortDir,
		Name:     "gc-object",
		Threads:  threads,
		Checksum: true,
	}, extsort.Codec[gcObjectRecord]{
		FromBytes: gcObjectRecordFromBytes,
		ToBytes:   gcObjectRecordToBytes,
		Compare:   compareGcObjectRecord,
	})
	if err != nil {
		metaSorter.CloseInput()
		_ = metaSorter.Done()
		return nil, nil, errors.Errorf("create object sorter: %s", err)
	}
	return metaSorter, objSorter, nil
}

func scanGcMetaRecords(c meta.Context, m meta.Meta, output chan<- gcMetaRecord, metaSliceSpin *utils.Bar, delFlag bool) error {
	st := m.ScanSlices(c, &meta.ScanSlicesOption{ScanPending: true, Delete: delFlag}, func(ino meta.Ino, s meta.Slice) error {
		select {
		case <-c.Done():
			return c.Err()
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
		case output <- gcMetaRecord{sliceID: s.Id, size: s.Size, state: state}:
			return nil
		case <-c.Done():
			return c.Err()
		}
	})
	if st != 0 {
		return errors.Errorf("scan slices: %s", st)
	}
	return nil
}

func scanGcObjectRecords(ctx context.Context, blob object.ObjectStorage, output chan<- gcObjectRecord, threads int, hashPrefix bool, maxMtime time.Time, prefixSpin, objScanSpin *utils.Bar, skipped *utils.DoubleSpinner) error {
	return scanGcChunkObjects(ctx, blob, threads, hashPrefix, prefixSpin, func(obj object.Object) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var ok bool
		obj, ok = filterGcObject(ctx, blob, obj, maxMtime, objScanSpin, skipped)
		if !ok {
			return nil
		}
		record, ok := parseGcObjectRecord(obj)
		if !ok {
			return nil
		}
		select {
		case output <- record:
		case <-ctx.Done():
			return ctx.Err()
		}
		objScanSpin.Increment()
		return nil
	})
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
	return gcObjectRecord{sliceID: sliceID, index: index, blockSize: blockSize, objectSize: obj.Size(), key: obj.Key()}, true
}

type gcMetaRecord struct {
	sliceID uint64
	size    uint32
	state   uint8
}

const (
	gcMetaRecordSize        = 13
	gcObjectRecordFixedSize = 24
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
	key        string
}

func gcObjectRecordFromBytes(data []byte) (gcObjectRecord, error) {
	if len(data) < gcObjectRecordFixedSize {
		return gcObjectRecord{}, errors.Errorf("invalid gc object record size: %d", len(data))
	}
	rb := utils.FromBuffer(data)
	return gcObjectRecord{
		sliceID:    rb.Get64(),
		index:      int(rb.Get32()),
		blockSize:  int(rb.Get32()),
		objectSize: int64(rb.Get64()),
		key:        string(rb.Get(len(data) - gcObjectRecordFixedSize)),
	}, nil
}

func gcObjectRecordToBytes(r gcObjectRecord) ([]byte, error) {
	wb := utils.NewBuffer(uint32(gcObjectRecordFixedSize + len(r.key)))
	wb.Put64(r.sliceID)
	wb.Put32(uint32(r.index))
	wb.Put32(uint32(r.blockSize))
	wb.Put64(uint64(r.objectSize))
	wb.Put([]byte(r.key))
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

func mergeGcSortedRecords(
	ctx context.Context,
	metaSorter *extsort.Sorter[gcMetaRecord],
	objSorter *extsort.Sorter[gcObjectRecord],
	blockSize int,
	stats *gcStats,
	leakedObj chan<- string,
) error {
	meta, metaOpen, err := metaSorter.Next(ctx)
	if err != nil {
		return err
	}
	markLeaked := func(obj gcObjectRecord) {
		stats.addLeaked(obj.objectSize)
		if leakedObj != nil {
			leakedObj <- obj.key
		}
	}

	for {
		obj, objOpen, err := objSorter.Next(ctx)
		if err != nil {
			return err
		}
		if !objOpen {
			break
		}

		for metaOpen && meta.sliceID < obj.sliceID {
			meta, metaOpen, err = metaSorter.Next(ctx)
			if err != nil {
				return err
			}
		}

		if !metaOpen || meta.sliceID > obj.sliceID {
			markLeaked(obj)
			continue
		}

		if meta.state == gcStateTrash {
			stats.addObject(meta.state, obj.objectSize)
		} else if isLeakedBlock(obj.index, obj.blockSize, int(meta.size), blockSize) {
			markLeaked(obj)
		} else {
			stats.addObject(meta.state, obj.objectSize)
		}
	}

	for metaOpen {
		_, metaOpen, err = metaSorter.Next(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}
