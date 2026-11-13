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
	"fmt"
	"os"

	lanratextsort "github.com/lanrat/extsort"
)

// Codec defines serialization and comparison for record sorting.
type Codec[T any] struct {
	FromBytes lanratextsort.FromBytesGeneric[T]
	ToBytes   lanratextsort.ToBytesGeneric[T]
	Compare   lanratextsort.CompareGeneric[T]
}

// Config configures the sharded external sort.
type Config struct {
	WorkDir  string
	Name     string
	Threads  int
	Checksum bool
	ChunkSize int
}

const (
	defaultThreads   = 10
	defaultChunkSize = 1 << 20
)

type Sorter[T any] struct {
	input  chan T
	sorter *lanratextsort.GenericSorter[T]
}

func New[T any](ctx context.Context, cfg Config, codec Codec[T]) (*Sorter[T], error) {
	if cfg.Threads <= 0 {
		cfg.Threads = defaultThreads
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = defaultChunkSize
	}
	if cfg.WorkDir != "" {
		if err := os.MkdirAll(cfg.WorkDir, 0755); err != nil {
			return nil, fmt.Errorf("external sort %s: %w", cfg.Name, err)
		}
	}
	input := make(chan T, cfg.Threads*128)
	sorter, _, errCh := lanratextsort.Generic(
		input,
		codec.FromBytes,
		codec.ToBytes,
		codec.Compare,
		&lanratextsort.Config{
			ChunkSize:          cfg.ChunkSize,
			NumWorkers:         cfg.Threads,
			ChanBuffSize:       cfg.Threads,
			SortedChanBuffSize: cfg.Threads * 32,
			TempFilesDir:       cfg.WorkDir,
			Checksum:           cfg.Checksum,
		},
	)
	if sorter == nil {
		return nil, fmt.Errorf("create external sorter: %w", <-errCh)
	}
	s := &Sorter[T]{input: input, sorter: sorter}
	go sorter.Sort(ctx)
	return s, nil
}

func (s *Sorter[T]) Input() chan<- T {
	return s.input
}

func (s *Sorter[T]) CloseInput() {
	close(s.input)
}

func (s *Sorter[T]) Next(ctx context.Context) (T, bool, error) {
	return s.sorter.Next(ctx)
}
