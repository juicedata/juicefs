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
	"sync"

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
}

const (
	defaultThreads   = 10
	defaultChunkSize = 1 << 20
)

type Sorter[T any] struct {
	input  chan T
	output <-chan T
	errCh  <-chan error

	waitOnce sync.Once
	err      error
	workDir  string
}

func New[T any](ctx context.Context, cfg Config, codec Codec[T]) (*Sorter[T], error) {
	if cfg.Threads <= 0 {
		cfg.Threads = defaultThreads
	}
	workDir, err := os.MkdirTemp(cfg.WorkDir, cfg.Name+"-")
	if err != nil {
		return nil, err
	}
	input := make(chan T, cfg.Threads*128)
	sorter, output, errCh := lanratextsort.Generic(
		input,
		codec.FromBytes,
		codec.ToBytes,
		codec.Compare,
		&lanratextsort.Config{
			ChunkSize:          defaultChunkSize,
			NumWorkers:         cfg.Threads,
			ChanBuffSize:       cfg.Threads,
			SortedChanBuffSize: cfg.Threads * 32,
			TempFilesDir:       workDir,
			Checksum:           cfg.Checksum,
		},
	)
	if sorter == nil {
		err := <-errCh
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("create external sorter: %w", err)
	}
	s := &Sorter[T]{input: input, output: output, errCh: errCh, workDir: workDir}
	go sorter.Sort(ctx)
	return s, nil
}

func (s *Sorter[T]) Input() chan<- T {
	return s.input
}

func (s *Sorter[T]) Output() <-chan T {
	return s.output
}

func (s *Sorter[T]) CloseInput() {
	close(s.input)
}

func (s *Sorter[T]) Wait() error {
	s.waitOnce.Do(func() {
		s.err = <-s.errCh
	})
	return s.err
}

func (s *Sorter[T]) Done() error {
	for range s.output {
	}
	err := s.Wait()
	_ = os.RemoveAll(s.workDir)
	return err
}
