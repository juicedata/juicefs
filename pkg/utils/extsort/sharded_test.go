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
	"reflect"
	"strings"
	"testing"
)

func TestSorterSortsRecords(t *testing.T) {
	s, err := New(context.Background(), Config{
		WorkDir: t.TempDir(),
		Threads: 2,
	}, Codec[testRecord]{
		FromBytes: decodeTestRecord,
		ToBytes:   encodeTestRecord,
		Compare:   compareTestRecord,
	})
	if err != nil {
		t.Fatalf("new sorter: %s", err)
	}

	for _, id := range []uint64{34, 17, 2, 33, 18, 1} {
		s.Input() <- testRecord{id: id}
	}
	s.CloseInput()

	got, err := readOutput(context.Background(), s)
	if err != nil {
		t.Fatalf("read output: %s", err)
	}
	if want := []uint64{1, 2, 17, 18, 33, 34}; !reflect.DeepEqual(got, want) {
		t.Fatalf("output = %v, want %v", got, want)
	}
}

func TestSorterDoneReturnsSortError(t *testing.T) {
	s, err := New(context.Background(), Config{
		WorkDir: t.TempDir(),
		Threads: 2,
	}, Codec[testRecord]{
		FromBytes: decodeTestRecord,
		ToBytes:   encodeTestRecord,
		Compare: func(_, _ testRecord) int {
			panic("compare failed")
		},
	})
	if err != nil {
		t.Fatalf("new sorter: %s", err)
	}

	s.Input() <- testRecord{id: 2}
	s.Input() <- testRecord{id: 1}
	s.CloseInput()

	err = s.Done()
	if err == nil || !strings.Contains(err.Error(), "compare failed") {
		t.Fatalf("done error = %v, want comparison error", err)
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

func readOutput(ctx context.Context, s *Sorter[testRecord]) ([]uint64, error) {
	var ids []uint64
	for {
		r, ok, err := s.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return ids, nil
		}
		ids = append(ids, r.id)
	}
}
