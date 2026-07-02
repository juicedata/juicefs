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

	"github.com/lanrat/extsort"
)

// Compare compares two encoded byte records.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
type Compare func(a, b []byte) int

// Codec defines the comparison function for record sorting.
type Codec struct {
	Compare Compare
}

// Config configures the sharded external sort.
type Config struct {
	WorkDir      string
	Name         string
	Shards       int
	Threads      int
	ChunkSize    int
	InputBuffer  int
	OutputBuffer int
}

const (
	defaultShards  = 16
	defaultThreads = 4
	// defaultChunkSize    = 1000000
	defaultChunkSize    = 1
	defaultInputBuffer  = 512
	defaultOutputBuffer = 128
)

// Sharded manages multiple external sort instances, one per shard.
// Records are distributed to shards via InputFor(shardKey), and sorted
// output for each shard is read from Outputs().
type Sharded struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	inputs   []chan []byte
	outputs  []<-chan []byte
	errChans []<-chan error

	wg      sync.WaitGroup
	workDir string
}

// NewSharded creates a sharded external sorter that distributes records
// to independent sort instances by shardKey % Shards.
func NewSharded(ctx context.Context, cfg Config, codec Codec) (*Sharded, error) {
	ctx, cancel := context.WithCancel(ctx)

	if cfg.Shards <= 0 {
		cfg.Shards = defaultShards
	}
	if cfg.Threads <= 0 {
		cfg.Threads = defaultThreads
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = defaultChunkSize
	}
	if cfg.InputBuffer <= 0 {
		cfg.InputBuffer = defaultInputBuffer
	}
	if cfg.OutputBuffer <= 0 {
		cfg.OutputBuffer = defaultOutputBuffer
	}

	workDir, err := os.MkdirTemp(cfg.WorkDir, cfg.Name+"-")
	if err != nil {
		cancel()
		return nil, err
	}

	s := &Sharded{
		cfg:      cfg,
		ctx:      ctx,
		cancel:   cancel,
		workDir:  workDir,
		inputs:   make([]chan []byte, cfg.Shards),
		outputs:  make([]<-chan []byte, cfg.Shards),
		errChans: make([]<-chan error, cfg.Shards),
	}

	numWorkers := cfg.Threads / cfg.Shards
	if numWorkers < 1 {
		numWorkers = 1
	}

	compareFn := extsort.CompareGeneric[[]byte](codec.Compare)

	for i := 0; i < cfg.Shards; i++ {
		dir := filepath.Join(workDir, s.shardName(i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			s.abortShards(i)
			cancel()
			os.RemoveAll(workDir)
			return nil, err
		}

		extCfg := &extsort.Config{
			ChunkSize:          cfg.ChunkSize,
			NumWorkers:         numWorkers,
			ChanBuffSize:       cfg.InputBuffer,
			SortedChanBuffSize: cfg.OutputBuffer,
			TempFilesDir:       dir,
		}

		input := make(chan []byte, cfg.InputBuffer)
		s.inputs[i] = input

		fromBytes := func(b []byte) ([]byte, error) { return b, nil }
		toBytes := func(b []byte) ([]byte, error) { return b, nil }

		sorter, output, errCh := extsort.Generic[[]byte](
			input,
			extsort.FromBytesGeneric[[]byte](fromBytes),
			extsort.ToBytesGeneric[[]byte](toBytes),
			compareFn,
			extCfg,
		)

		s.outputs[i] = output
		s.errChans[i] = errCh

		if sorter == nil {
			sorterErr := <-errCh
			s.abortShards(i + 1)
			cancel()
			os.RemoveAll(workDir)
			return nil, fmt.Errorf("external sort sorter %d: %w", i, sorterErr)
		}

		s.wg.Add(1)
		go func(sorter extsort.Sorter) {
			defer s.wg.Done()
			sorter.Sort(s.ctx)
		}(sorter)
	}

	return s, nil
}

// abortShards closes inputs and drains outputs for shards 0..n-1,
// then waits for all started sort goroutines to finish.
// It tolerates nil channels for uninitialized shards.
func (s *Sharded) abortShards(n int) {
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
func (s *Sharded) InputFor(shardKey uint64) chan<- []byte {
	return s.inputs[shardKey%uint64(s.cfg.Shards)]
}

// Outputs returns the output channels for all shards in order.
func (s *Sharded) Outputs() []<-chan []byte {
	return s.outputs
}

// CloseInputs closes all input channels, signaling that no more data will be sent.
func (s *Sharded) CloseInputs() {
	for _, ch := range s.inputs {
		close(ch)
	}
}

// Wait waits for all sort goroutines to finish and returns the first error
// encountered, if any. It does not drain outputs — outputs remain readable
// after Wait returns. Callers should drain outputs before calling Wait,
// otherwise sort goroutines may block on unbuffered output channels.
func (s *Sharded) Wait() error {
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
func (s *Sharded) Done() error {
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

func (s *Sharded) shardName(i int) string {
	return fmt.Sprintf("%s-%02d", s.cfg.Name, i)
}
