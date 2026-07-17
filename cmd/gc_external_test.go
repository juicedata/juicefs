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
	"bytes"
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/stretchr/testify/require"
)

func TestMergeGcSortedRecords(t *testing.T) {
	const externalSortChunkSize = 1 << 20

	ctx := context.Background()
	metaSorter, objSorter, err := newGcExternalSorters(ctx, t.TempDir(), 2)
	require.NoError(t, err)

	// Fill one chunk in each sorter so asserted records are merged from disk-backed second chunks.
	for i := 0; i < externalSortChunkSize; i++ {
		metaSorter.Input() <- gcMetaRecord{sliceID: uint64(i + 100), size: 4, state: gcStateUsed}
	}
	for _, record := range []gcMetaRecord{
		{sliceID: 3, size: 4, state: gcStateTrash},
		{sliceID: 1, size: 5, state: gcStateUsed},
		{sliceID: 2, size: 8, state: gcStatePending},
	} {
		metaSorter.Input() <- record
	}
	metaSorter.CloseInput()

	for i := 0; i < externalSortChunkSize; i++ {
		objSorter.Input() <- gcObjectRecord{sliceID: uint64(i + 100), blockSize: 4, objectSize: 4}
	}
	for _, record := range []gcObjectRecord{
		{sliceID: 9, index: 0, blockSize: 4, objectSize: 9, key: "9_0_4"},
		{sliceID: 1, index: 1, blockSize: 4, objectSize: 4, key: "1_1_4"},
		{sliceID: 3, index: 0, blockSize: 4, objectSize: 3, key: "3_0_4"},
		{sliceID: 2, index: 1, blockSize: 4, objectSize: 4, key: "2_1_4"},
		{sliceID: 1, index: 0, blockSize: 4, objectSize: 4, key: "1_0_4"},
		{sliceID: 1, index: 1, blockSize: 1, objectSize: 1, key: "1_1_1"},
	} {
		objSorter.Input() <- record
	}
	objSorter.CloseInput()

	progress := utils.NewProgress(true)
	t.Cleanup(progress.Done)
	valid := progress.AddDoubleSpinner("valid")
	pending := progress.AddDoubleSpinner("pending")
	compacted := progress.AddDoubleSpinner("compacted")
	leaked := progress.AddDoubleSpinner("leaked")
	leakedKeys := make(chan string, 6)

	stats, err := mergeGcSortedRecords(ctx, metaSorter, objSorter, 4, valid, pending, compacted, leaked, leakedKeys)
	require.NoError(t, err)
	require.Equal(t, gcMergeStats{
		valid:     gcObjectCounter{count: externalSortChunkSize + 2, bytes: externalSortChunkSize*4 + 5},
		pending:   gcObjectCounter{count: 1, bytes: 4},
		compacted: gcObjectCounter{count: 1, bytes: 3},
		leaked:    gcObjectCounter{count: 2, bytes: 13},
	}, stats)

	close(leakedKeys)
	var keys []string
	for key := range leakedKeys {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"1_1_4", "9_0_4"}, keys)
	require.NoError(t, metaSorter.Done())
	require.NoError(t, objSorter.Done())
}

func TestScanGcChunkObjects(t *testing.T) {
	tests := []struct {
		name           string
		hashPrefix     bool
		keys           []string
		listedPrefixes int64
	}{
		{
			name:       "hash prefix",
			hashPrefix: true,
			keys: []string{
				"01/0/1_0_4",
				"01/0/257_0_4",
				"02/0/2_0_4",
			},
			listedPrefixes: 2,
		},
		{
			name: "normal prefix",
			keys: []string{
				"0/0/1_0_4",
				"0/1/1000_0_4",
				"1/1000/1000000_0_4",
			},
			listedPrefixes: 3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, err := object.CreateStorage("mem", test.name, "", "", "")
			require.NoError(t, err)
			blob := object.WithPrefix(base, "chunks/")
			for _, key := range test.keys {
				require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("data"))))
			}

			progress := utils.NewProgress(true)
			t.Cleanup(progress.Done)
			prefixSpin := progress.AddCountBar("prefixes", 0)
			var mu sync.Mutex
			var got []string
			err = scanGcChunkObjects(context.Background(), blob, 2, test.hashPrefix, prefixSpin, func(obj object.Object) error {
				mu.Lock()
				got = append(got, obj.Key())
				mu.Unlock()
				return nil
			})
			require.NoError(t, err)

			sort.Strings(got)
			want := append([]string(nil), test.keys...)
			sort.Strings(want)
			require.Equal(t, want, got)
			require.Equal(t, test.listedPrefixes, prefixSpin.GetTotal())
			require.Equal(t, test.listedPrefixes, prefixSpin.Current())
		})
	}
}
