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

import (
	"context"
	"io"
)

type Reader interface {
	ReadAt(ctx context.Context, p *Page, off int) (int, error)
}

type Writer interface {
	io.WriterAt
	ID() uint64
	SetID(chunkid uint64)
	FlushTo(offset int) error
	Finish(length int) error
	Abort()
}

type ChunkStore interface {
	NewReader(chunkid uint64, length int) Reader
	NewWriter(chunkid uint64) Writer
	Remove(chunkid uint64, length int) error
	FillCache(chunkid uint64, length uint32) error
	UsedMemory() int64
}
