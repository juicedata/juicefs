/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
