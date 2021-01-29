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
	"fmt"
	"strings"

	"github.com/DataDog/zstd"
	"github.com/hungys/go-lz4"
)

// ZSTD_LEVEL compression level used by Zstd
const ZSTD_LEVEL = 1 // fastest

// Compressor interface to be implemented by a compression algo
type Compressor interface {
	Name() string
	CompressBound(int) int
	Compress(dst, src []byte) (int, error)
	Decompress(dst, src []byte) (int, error)
}

// NewCompressor returns a struct implementing Compressor interface
func NewCompressor(algr string) Compressor {
	algr = strings.ToLower(algr)
	if algr == "zstd" {
		return ZStandard{ZSTD_LEVEL}
	} else if algr == "lz4" {
		return LZ4{}
	} else if algr == "none" || algr == "" {
		return noOp{}
	}
	return nil
}

type noOp struct{}

func (n noOp) Name() string            { return "Noop" }
func (n noOp) CompressBound(l int) int { return l }
func (n noOp) Compress(dst, src []byte) (int, error) {
	if len(dst) < len(src) {
		return 0, fmt.Errorf("buffer too short: %d < %d", len(dst), len(src))
	}
	copy(dst, src)
	return len(src), nil
}
func (n noOp) Decompress(dst, src []byte) (int, error) {
	if len(dst) < len(src) {
		return 0, fmt.Errorf("buffer too short: %d < %d", len(dst), len(src))
	}
	copy(dst, src)
	return len(src), nil
}

// ZStandard implements Compressor interface using zstd library
type ZStandard struct {
	level int
}

// Name returns name of the algorithm Zstd
func (n ZStandard) Name() string { return "Zstd" }

// CompressBound max size of compressed data
func (n ZStandard) CompressBound(l int) int { return zstd.CompressBound(l) }

// Compress using Zstd
func (n ZStandard) Compress(dst, src []byte) (int, error) {
	d, err := zstd.CompressLevel(dst, src, n.level)
	if err != nil {
		return 0, err
	}
	if len(d) > 0 && len(dst) > 0 && &d[0] != &dst[0] {
		return 0, fmt.Errorf("buffer too short: %d < %d", cap(dst), cap(d))
	}
	return len(d), err
}

// Decompress using Zstd
func (n ZStandard) Decompress(dst, src []byte) (int, error) {
	d, err := zstd.Decompress(dst, src)
	if err != nil {
		return 0, err
	}
	if len(d) > 0 && len(dst) > 0 && &d[0] != &dst[0] {
		return 0, fmt.Errorf("buffer too short: %d < %d", len(dst), len(d))
	}
	return len(d), err
}

// LZ4 implements Compressor using LZ4 library
type LZ4 struct{}

// Name returns name of the algorithm LZ4
func (l LZ4) Name() string { return "LZ4" }

// CompressBound max size of compressed data
func (l LZ4) CompressBound(size int) int { return lz4.CompressBound(size) }

// Compress using LZ4 algorithm
func (l LZ4) Compress(dst, src []byte) (int, error) {
	return lz4.CompressDefault(src, dst)
}

// Decompress using LZ4 algorithm
func (l LZ4) Decompress(dst, src []byte) (int, error) {
	return lz4.DecompressSafe(src, dst)
}
