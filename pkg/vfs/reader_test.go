/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package vfs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func newTestDataReader(t *testing.T) DataReader {
	t.Helper()
	format := &meta.Format{
		Name:      "test",
		UUID:      uuid.New().String(),
		Storage:   "mem",
		BlockSize: 4096,
	}
	m := meta.NewClient("memkv://", meta.DefaultConf())
	if err := m.Init(format, true); err != nil {
		t.Fatalf("meta init: %v", err)
	}
	chunkConf := chunk.Config{
		BlockSize:   format.BlockSize * 1024,
		MaxUpload:   2,
		MaxDownload: 10,
		BufferSize:  30 << 20,
		CacheSize:   0,
		CacheDir:    "memory",
	}
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	store := chunk.NewCachedStore(blob, chunkConf, prometheus.NewRegistry())
	conf := &Config{
		Meta:   meta.DefaultConf(),
		Format: *format,
		Chunk:  &chunkConf,
	}
	return NewDataReader(conf, m, store)
}

// TestOpenReadOnly_SharedInstance verifies that two OpenReadOnly calls for the
// same inode return the same underlying fileReader (i.e. the same pointer),
// while Open always returns a fresh independent instance.
func TestOpenReadOnly_SharedInstance(t *testing.T) {
	r := newTestDataReader(t)
	dr := r.(*dataReader)

	const inode Ino = 42
	const length uint64 = 1024

	f1 := r.OpenReadOnly(inode, length)
	f2 := r.OpenReadOnly(inode, length)

	assert.Same(t, f1, f2, "OpenReadOnly should return the same fileReader for the same inode")

	dr.Lock()
	refs := dr.roFiles[inode].refs
	dr.Unlock()
	assert.Equal(t, uint16(2), refs, "ref count should be 2 after two OpenReadOnly calls")

	// Open (write path) must still return a fresh, independent instance.
	fw := r.Open(inode, length)
	assert.NotSame(t, f1, fw, "Open should return an independent fileReader")
	fw.Close(meta.Background())
}

// TestOpenReadOnly_CloseRefCounting verifies that the shared fileReader is kept
// alive until the last opener closes it, and removed from roFiles afterwards.
func TestOpenReadOnly_CloseRefCounting(t *testing.T) {
	r := newTestDataReader(t)
	dr := r.(*dataReader)

	const inode Ino = 7
	const length uint64 = 512

	f1 := r.OpenReadOnly(inode, length)
	f2 := r.OpenReadOnly(inode, length)

	// First close: reader should still be in roFiles (refs drops to 1).
	f1.Close(meta.Background())
	dr.Lock()
	_, stillPresent := dr.roFiles[inode]
	dr.Unlock()
	assert.True(t, stillPresent, "shared fileReader should survive after first Close")

	// Second close: refs reaches 0, reader must be removed from roFiles.
	f2.Close(meta.Background())
	dr.Lock()
	_, stillPresent = dr.roFiles[inode]
	dr.Unlock()
	assert.False(t, stillPresent, "shared fileReader should be removed after last Close")
}

// TestOpenReadOnly_IndependentInodes verifies that different inodes get
// separate shared readers.
func TestOpenReadOnly_IndependentInodes(t *testing.T) {
	r := newTestDataReader(t)

	fa := r.OpenReadOnly(Ino(1), 100)
	fb := r.OpenReadOnly(Ino(2), 100)

	assert.NotSame(t, fa, fb, "different inodes must have independent shared readers")

	fa.Close(meta.Background())
	fb.Close(meta.Background())
}

// TestOpenReadOnly_LengthUpdate verifies that if a second opener provides a
// larger length, the shared fileReader's length is updated.
func TestOpenReadOnly_LengthUpdate(t *testing.T) {
	r := newTestDataReader(t)
	dr := r.(*dataReader)

	const inode Ino = 99
	f1 := r.OpenReadOnly(inode, 1024)
	f2 := r.OpenReadOnly(inode, 2048) // larger length

	assert.Same(t, f1, f2)

	dr.Lock()
	fr := dr.roFiles[inode]
	dr.Unlock()

	fr.Lock()
	got := fr.length
	fr.Unlock()
	assert.Equal(t, uint64(2048), got, "shared fileReader length should be updated to the larger value")

	f1.Close(meta.Background())
	f2.Close(meta.Background())
}
