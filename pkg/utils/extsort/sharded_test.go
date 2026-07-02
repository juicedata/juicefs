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

package extsort

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestShardedSortsRecordsByShard(t *testing.T) {
	s, err := NewSharded(context.Background(), Config{
		WorkDir: t.TempDir(),
		Name:    "records",
		Threads: 2,
	}, Codec[testRecord]{
		FromBytes: decodeTestRecord,
		ToBytes:   encodeTestRecord,
		Compare:   compareTestRecord,
	})
	if err != nil {
		t.Fatalf("new sharded sorter: %s", err)
	}

	workDir := s.workDir
	for _, id := range []uint64{34, 17, 2, 33, 18, 1} {
		s.InputFor(id) <- testRecord{id: id}
	}
	s.CloseInputs()

	got := readOutputs(t, s.Outputs())
	if err := s.Done(); err != nil {
		t.Fatalf("done: %s", err)
	}
	if _, err := os.Stat(workDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("work dir was not removed: %v", err)
	}

	if want := []uint64{1, 17, 33}; !reflect.DeepEqual(got[1], want) {
		t.Fatalf("shard 1 = %v, want %v", got[1], want)
	}
	if want := []uint64{2, 18, 34}; !reflect.DeepEqual(got[2], want) {
		t.Fatalf("shard 2 = %v, want %v", got[2], want)
	}
	for shard, ids := range got {
		if shard == 1 || shard == 2 {
			continue
		}
		if len(ids) != 0 {
			t.Fatalf("shard %d = %v, want empty", shard, ids)
		}
	}
}

func TestShardedDoneReturnsSortError(t *testing.T) {
	s, err := NewSharded(context.Background(), Config{
		WorkDir: t.TempDir(),
		Name:    "records",
		Threads: 2,
	}, Codec[testRecord]{
		FromBytes: decodeTestRecord,
		ToBytes:   encodeTestRecord,
		Compare: func(_, _ testRecord) int {
			panic("compare failed")
		},
	})
	if err != nil {
		t.Fatalf("new sharded sorter: %s", err)
	}

	workDir := s.workDir
	s.InputFor(0) <- testRecord{id: 2}
	s.InputFor(0) <- testRecord{id: 1}
	s.CloseInputs()

	err = s.Done()
	if err == nil || !strings.Contains(err.Error(), "compare failed") {
		t.Fatalf("done error = %v, want comparison error", err)
	}
	if _, err := os.Stat(workDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("work dir was not removed: %v", err)
	}
}

type testRecord struct {
	id uint64
}

func compareTestRecord(a, b testRecord) int {
	if a.id < b.id {
		return -1
	}
	if a.id > b.id {
		return 1
	}
	return 0
}

func encodeTestRecord(r testRecord) ([]byte, error) {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, r.id)
	return b, nil
}

func decodeTestRecord(b []byte) (testRecord, error) {
	if len(b) != 8 {
		return testRecord{}, errors.New("invalid record size")
	}
	return testRecord{id: binary.BigEndian.Uint64(b)}, nil
}

func readOutputs(t *testing.T, outputs []<-chan testRecord) [][]uint64 {
	t.Helper()

	type result struct {
		shard int
		ids   []uint64
		err   error
	}
	results := make(chan result, len(outputs))
	for shard, output := range outputs {
		go func(shard int, output <-chan testRecord) {
			var ids []uint64
			for r := range output {
				ids = append(ids, r.id)
			}
			results <- result{shard: shard, ids: ids}
		}(shard, output)
	}

	got := make([][]uint64, len(outputs))
	for range outputs {
		res := <-results
		if res.err != nil {
			t.Fatalf("read shard %d: %s", res.shard, res.err)
		}
		got[res.shard] = res.ids
	}
	return got
}
