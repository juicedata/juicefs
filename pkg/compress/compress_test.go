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

package compress

import (
	"io"
	"os"
	"testing"
)

func testCompress(t *testing.T, c Compressor) {
	src := []byte("hello")
	dst := make([]byte, c.CompressBound(len(src)))
	n, err := c.Compress(dst, src)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	src2 := make([]byte, len(src))
	n, err = c.Decompress(src2, dst[:n])
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	if string(src2[:n]) != string(src) {
		t.Error("not matched", string(src2))
		t.FailNow()
	}
}

func TestUncompressed(t *testing.T) {
	testCompress(t, NewCompressor("none"))
}

func TestZstd(t *testing.T) {
	testCompress(t, NewCompressor("zstd"))
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
