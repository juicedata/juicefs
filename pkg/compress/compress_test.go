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

package compress

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
)

func testCompress(t *testing.T, c Compressor) {
	src := []byte(c.Name())
	testIt := func(src []byte) {
		if len(src) > 1 {
			_, err := c.Compress(make([]byte, 1), src)
			if err == nil {
				t.Fatal("expect short buffer error, but got nil ")
			}
		}
		dst := make([]byte, c.CompressBound(len(src)))
		n, err := c.Compress(dst, src)
		if err != nil {
			t.Fatalf("compress: %s", err)
		}
		if len(src) > 1 {
			_, err = c.Decompress(make([]byte, 1), dst[:n])
			if err == nil {
				t.Fatalf("expect short buffer error, but got nil")
			}
		}
		src2 := make([]byte, len(src))
		n, err = c.Decompress(src2, dst[:n])
		if err != nil {
			t.Fatalf("decompress: %s", err)
		}
		if string(src2[:n]) != string(src) {
			t.Fatalf("expect %s but got %s", string(src), string(src2))
		}
	}

	testIt(src)
	testIt(nil)

	if c.CompressBound(0) > 0 {
		n, err := c.Decompress(make([]byte, 100), src[:0])
		if err == nil || n > 0 {
			t.Fatalf("decompress should fail, but got %d", n)
		}
	}
}

func TestUncompressed(t *testing.T) {
	testCompress(t, NewCompressor("none"))
}

func TestZstd(t *testing.T) {
	testCompress(t, NewCompressor("zstd"))
}

func TestLZ4(t *testing.T) {
	testCompress(t, NewCompressor("lz4"))
}

func benchmarkDecompress(b *testing.B, comp Compressor) {
	f, _ := os.Open(os.Getenv("PAYLOAD"))
	var c = make([]byte, 5<<20)
	var d = make([]byte, 4<<20)
	n, err := io.ReadFull(f, d)
	f.Close()
	if err != nil {
		b.Skip()
		return
	}
	d = d[:n]
	n, err = comp.Compress(c[:4<<20], d)
	if err != nil {
		b.Errorf("compress: %s", err)
		b.FailNow()
	}
	c = c[:n]
	// println("compres", comp.Name(), len(c), len(d))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := comp.Decompress(d, c)
		if err != nil {
			b.Errorf("decompress %d %s", n, err)
			b.FailNow()
		}
		b.SetBytes(int64(len(d)))
	}
}

func BenchmarkDecompressZstd(b *testing.B) {
	benchmarkDecompress(b, NewCompressor("zstd"))
}

func BenchmarkDecompressLZ4(b *testing.B) {
	benchmarkDecompress(b, LZ4{})
}

func BenchmarkDecompressNone(b *testing.B) {
	benchmarkDecompress(b, NewCompressor("none"))
}

func benchmarkCompress(b *testing.B, comp Compressor) {
	f, _ := os.Open(os.Getenv("PAYLOAD"))
	var d = make([]byte, 4<<20)
	n, err := io.ReadFull(f, d)
	f.Close()
	if err != nil {
		b.Skip()
		return
	}
	d = d[:n]
	var c = make([]byte, 5<<20)
	// println("compres", comp.Name(), len(c), len(d))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := comp.Compress(c, d)
		if err != nil {
			b.Errorf("compress %d %s", n, err)
			b.FailNow()
		}
		b.SetBytes(int64(len(d)))
	}
}

func BenchmarkCompressZstd(b *testing.B) {
	benchmarkCompress(b, NewCompressor("Zstd"))
}

func BenchmarkCompressCLZ4(b *testing.B) {
	benchmarkCompress(b, LZ4{})
}
func BenchmarkCompressNone(b *testing.B) {
	benchmarkCompress(b, NewCompressor("none"))
}

// A block is often decompressed into a sub-slice of a larger buffer: the
// reader serves one read-ahead buffer to several slices of a chunk at once,
// handing each of them a Page.Slice of it. Decompression must stay inside the
// requested output, or it corrupts the data of whatever shares that buffer.
func TestDecompressStaysInsideDst(t *testing.T) {
	// > 128 KiB of literal-heavy but compressible data (random hex with a
	// repeating prefix), so zstd stages a whole block of literals inside the
	// spare room after the output
	r := rand.New(rand.NewSource(1))
	var b bytes.Buffer
	for b.Len() < 262005 {
		fmt.Fprintf(&b, "%016x%016x\t%d\tsome/shared/prefix/dir/file-%d\n",
			r.Uint64(), r.Uint64(), r.Intn(1<<20), b.Len())
	}
	data := b.Bytes()[:262005]

	// a slice in the middle of a chunk is decompressed at a non-zero offset,
	// with neighbours on both sides
	for _, off := range []int{0, 4096} {
		for _, algr := range []string{"zstd", "lz4"} {
			c := NewCompressor(algr)
			buf := make([]byte, c.CompressBound(len(data)))
			n, err := c.Compress(buf, data)
			if err != nil {
				t.Fatalf("%s compress: %s", algr, err)
			}
			const guard = 1 << 20
			big := make([]byte, off+len(data)+guard)
			for i := range big {
				big[i] = 0xA5
			}
			dst := big[off : off+len(data)]
			if _, err = c.Decompress(dst, buf[:n]); err != nil {
				t.Fatalf("%s decompress at offset %d: %s", algr, off, err)
			}
			if !bytes.Equal(dst, data) {
				t.Fatalf("%s at offset %d: decompressed data differs", algr, off)
			}
			checkUntouched(t, algr, off, "before", big[:off])
			checkUntouched(t, algr, off, "after", big[off+len(data):])
		}
	}
}

func checkUntouched(t *testing.T, algr string, off int, where string, neighbour []byte) {
	t.Helper()
	dirty, first := 0, -1
	for i, b := range neighbour {
		if b != 0xA5 {
			if first < 0 {
				first = i
			}
			dirty++
		}
	}
	if dirty > 0 {
		t.Fatalf("%s at offset %d: wrote %d bytes %s the requested output, first at %d",
			algr, off, dirty, where, first)
	}
}
