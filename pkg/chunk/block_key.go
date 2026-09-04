/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package chunk

import "fmt"

// BlockKey returns the object storage key for a single block.
// It mirrors the private rSlice.key method in cached_store.go so that
// external packages (e.g. pkg/p2p) can compute keys without accessing
// cachedStore internals.
//
//   - id         – slice ID
//   - blockIndex – zero-based index of the block within the slice
//   - blockSize  – size of this specific block in bytes
//   - hashPrefix – whether the store uses hash-prefixed key layout
func BlockKey(id uint64, blockIndex, blockSize int, hashPrefix bool) string {
	if hashPrefix {
		return fmt.Sprintf("chunks/%02X/%v/%v_%v_%v", id%256, id/1000/1000, id, blockIndex, blockSize)
	}
	return fmt.Sprintf("chunks/%v/%v/%v_%v_%v", id/1000/1000, id/1000, id, blockIndex, blockSize)
}

// SliceBlockKeys returns all object storage keys for a slice.
// It mirrors the private rSlice.keys method in cached_store.go.
//
//   - id         – slice ID
//   - length     – total byte length of the slice
//   - blockSize  – maximum block size in bytes (last block may be smaller)
//   - hashPrefix – whether the store uses hash-prefixed key layout
//
// Returns nil when length <= 0.
func SliceBlockKeys(id uint64, length, blockSize int, hashPrefix bool) []string {
	if length <= 0 {
		return nil
	}
	numBlocks := (length-1)/blockSize + 1
	keys := make([]string, numBlocks)
	for i := 0; i < numBlocks; i++ {
		bsize := blockSize
		if remaining := length - i*blockSize; remaining < bsize {
			bsize = remaining
		}
		keys[i] = BlockKey(id, i, bsize, hashPrefix)
	}
	return keys
}
