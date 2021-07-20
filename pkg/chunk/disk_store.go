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
	"fmt"
	"os"
	"path/filepath"
)

type diskFile struct {
	id   uint64
	path string
}

func (c *diskFile) ID() uint64 {
	return c.id
}

func (c *diskFile) SetID(id uint64) {
	c.id = id
}

func (c *diskFile) ReadAt(ctx context.Context, p *Page, off int) (n int, err error) {
	f, err := os.Open(c.path)
	if err != nil {
		return 0, err
	}
	st, _ := f.Stat()
	defer f.Close()
	if len(p.Data) > int(st.Size())-off {
		return f.ReadAt(p.Data[:st.Size()-int64(off)], int64(off))
	}
	return f.ReadAt(p.Data, int64(off))
}

func (c *diskFile) WriteAt(p []byte, off int64) (n int, err error) {
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_WRONLY, os.FileMode(0644))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.WriteAt(p, off)
}

func (c *diskFile) FlushTo(offset int) error {
	return nil
}

func (c *diskFile) Len() int {
	fi, err := os.Stat(c.path)
	if err != nil {
		return 0
	}
	return int(fi.Size())
}

func (c *diskFile) Finish(length int) error {
	if c.Len() < length {
		return fmt.Errorf("data length mismatch: %v != %v", c.Len(), length)
	}
	return nil
}

func (c *diskFile) Abort() {
	os.Remove(c.path)
}

type diskStore struct {
	root string
}

func (s *diskStore) chunkPath(chunkid uint64) string {
	name := fmt.Sprintf("%v.chunk", chunkid)
	return filepath.Join(s.root, name)
}

func NewDiskStore(dir string) ChunkStore {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.Mkdir(dir, 0755)
	}
	return &diskStore{dir}
}

func (s *diskStore) NewReader(chunkid uint64, length int) Reader {
	return &diskFile{chunkid, s.chunkPath(chunkid)}
}

func (s *diskStore) NewWriter(chunkid uint64) Writer {
	return &diskFile{chunkid, s.chunkPath(chunkid)}
}

func (s *diskStore) Remove(chunkid uint64, length int) error {
	return os.Remove(s.chunkPath(chunkid))
}

func (s *diskStore) FillCache(chunkid uint64, length uint32) error {
	return fmt.Errorf("Not Supported")
}

func (s *diskStore) UsedMemory() int64 { return 0 }

var _ ChunkStore = &diskStore{}
