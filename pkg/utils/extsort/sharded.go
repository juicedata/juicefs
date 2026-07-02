/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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
	"path/filepath"
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
	WorkDir string
	Name    string
	Shards  int
	Threads int
}

const defaultShards = 16

// Sharded manages multiple external sort instances, one per shard.
// Records are distributed to shards via InputFor(shardKey), and sorted
// output for each shard is read from Outputs().
type Sharded[T any] struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	inputs   []chan T
	outputs  []<-chan T
	errChans []<-chan error

	wg      sync.WaitGroup
	workDir string
}

// NewSharded creates a sharded external sorter that distributes records
// to independent sort instances by shardKey % Shards.
func NewSharded[T any](ctx context.Context, cfg Config, codec Codec[T]) (*Sharded[T], error) {
	ctx, cancel := context.WithCancel(ctx)
	if cfg.Shards <= 0 {
		cfg.Shards = defaultShards
	}

	workDir, err := os.MkdirTemp(cfg.WorkDir, cfg.Name+"-")
	if err != nil {
		cancel()
		return nil, err
	}

	s := &Sharded[T]{
		cfg:      cfg,
		ctx:      ctx,
		cancel:   cancel,
		workDir:  workDir,
		inputs:   make([]chan T, cfg.Shards),
		outputs:  make([]<-chan T, cfg.Shards),
		errChans: make([]<-chan error, cfg.Shards),
	}

	for i := 0; i < cfg.Shards; i++ {
		if err := s.startShard(i, codec); err != nil {
			s.abortShards(i + 1)
			cancel()
			os.RemoveAll(workDir)
			return nil, err
		}
	}

	return s, nil
}

func (s *Sharded[T]) startShard(i int, codec Codec[T]) error {
	dir := filepath.Join(s.workDir, s.shardName(i))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	workers := s.cfg.Threads / s.cfg.Shards
	if workers < 1 {
		workers = 1
	}
	input := make(chan T, s.cfg.Threads*128)
	sorter, output, errCh := lanratextsort.Generic(
		input,
		codec.FromBytes,
		codec.ToBytes,
		codec.Compare,
		&lanratextsort.Config{
			ChunkSize:          1 << 20,
			NumWorkers:         workers,
			ChanBuffSize:       s.cfg.Threads,
			SortedChanBuffSize: s.cfg.Threads * 32,
			TempFilesDir:       dir,
		},
	)

	s.inputs[i] = input
	s.outputs[i] = output
	s.errChans[i] = errCh
	if sorter == nil {
		return fmt.Errorf("external sort sorter %d: %w", i, <-errCh)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sorter.Sort(s.ctx)
	}()
	return nil
}

// abortShards closes inputs and drains outputs for shards 0..n-1,
// then waits for all started sort goroutines to finish.
// It tolerates nil channels for uninitialized shards.
func (s *Sharded[T]) abortShards(n int) {
	for j := 0; j < n; j++ {
		if s.inputs[j] != nil {
			close(s.inputs[j])
		}
	}
	for j := 0; j < n; j++ {
		if s.outputs[j] != nil {
			for range s.outputs[j] {
			}
		}
	}
	s.wg.Wait()
}

// InputFor returns the input channel for the shard corresponding to shardKey.
// The shard is determined by shardKey % Shards.
func (s *Sharded[T]) InputFor(shardKey uint64) chan<- T {
	return s.inputs[shardKey%uint64(s.cfg.Shards)]
}

// Outputs returns the output channels for all shards in order.
func (s *Sharded[T]) Outputs() []<-chan T {
	return s.outputs
}

// CloseInputs closes all input channels, signaling that no more data will be sent.
func (s *Sharded[T]) CloseInputs() {
	for _, ch := range s.inputs {
		close(ch)
	}
}

// Wait waits for all sort goroutines to finish and returns the first error
// encountered, if any. It does not drain outputs — outputs remain readable
// after Wait returns. Callers should drain outputs before calling Wait,
// otherwise sort goroutines may block on unbuffered output channels.
func (s *Sharded[T]) Wait() error {
	s.wg.Wait()
	var firstErr error
	for _, ch := range s.errChans {
		if ch != nil {
			if err := <-ch; err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Done drains any remaining output, waits for all sort goroutines, cancels the
// context, removes the temporary working directory, and returns the first sorter
// error encountered (if any).
func (s *Sharded[T]) Done() error {
	for _, ch := range s.outputs {
		if ch != nil {
			for range ch {
			}
		}
	}
	err := s.Wait()
	s.cancel()
	if s.workDir != "" {
		_ = os.RemoveAll(s.workDir)
	}
	return err
}

func (s *Sharded[T]) shardName(i int) string {
	return fmt.Sprintf("%s-%02d", s.cfg.Name, i)
}
