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
	"hash/crc64"
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
	// Checksum verifies records written to and read from disk in Done.
	Checksum bool
}

const (
	defaultShards    = 16
	defaultChunkSize = 1 << 20
)

var checksumTable = crc64.MakeTable(crc64.ECMA)

// Records are reordered by sorting, so the checksum represents a multiset.
type recordChecksum struct {
	count      uint64
	sum        uint64
	sumSquares uint64
}

func (c *recordChecksum) add(hash uint64) {
	c.count++
	c.sum += hash
	c.sumSquares += hash * hash
}

func (c recordChecksum) equal(other recordChecksum) bool {
	return c.count == other.count && c.sum == other.sum && c.sumSquares == other.sumSquares
}

func (c recordChecksum) String() string {
	return fmt.Sprintf("count=%d sum=%016x sumSquares=%016x", c.count, c.sum, c.sumSquares)
}

type shardChecksum struct {
	mu      sync.Mutex
	written recordChecksum
	read    recordChecksum
}

func (c *shardChecksum) addWritten(data []byte) {
	hash := crc64.Checksum(data, checksumTable)
	c.mu.Lock()
	c.written.add(hash)
	c.mu.Unlock()
}

func (c *shardChecksum) addRead(hash uint64) {
	c.mu.Lock()
	c.read.add(hash)
	c.mu.Unlock()
}

// Sharded manages multiple external sort instances, one per shard.
// Records are distributed to shards via InputFor(shardKey), and sorted
// output for each shard is read from Outputs().
type Sharded[T any] struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	inputs    []chan T
	outputs   []<-chan T
	errChans  []<-chan error
	checksums []*shardChecksum

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
	if cfg.Checksum {
		s.checksums = make([]*shardChecksum, cfg.Shards)
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
	fromBytes := codec.FromBytes
	toBytes := codec.ToBytes
	var checksum *shardChecksum
	if s.cfg.Checksum {
		checksum = &shardChecksum{}
		fromBytes = func(data []byte) (T, error) {
			hash := crc64.Checksum(data, checksumTable)
			record, err := codec.FromBytes(data)
			if err == nil {
				checksum.addRead(hash)
			}
			return record, err
		}
		toBytes = func(record T) ([]byte, error) {
			data, err := codec.ToBytes(record)
			if err == nil {
				checksum.addWritten(data)
			}
			return data, err
		}
		s.checksums[i] = checksum
	}
	sorter, output, errCh := lanratextsort.Generic(
		input,
		fromBytes,
		toBytes,
		codec.Compare,
		&lanratextsort.Config{
			ChunkSize:          defaultChunkSize,
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

func (s *Sharded[T]) wait() error {
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
// or integrity error encountered (if any).
func (s *Sharded[T]) Done() error {
	for _, ch := range s.outputs {
		if ch != nil {
			for range ch {
			}
		}
	}
	err := s.wait()
	if err == nil && s.cfg.Checksum {
		for shard, checksum := range s.checksums {
			checksum.mu.Lock()
			written, read := checksum.written, checksum.read
			checksum.mu.Unlock()
			if !written.equal(read) {
				err = fmt.Errorf("external sort %s checksum mismatch: written %s, read %s", s.shardName(shard), written, read)
				break
			}
		}
	}
	s.cancel()
	if s.workDir != "" {
		_ = os.RemoveAll(s.workDir)
	}
	return err
}

func (s *Sharded[T]) shardName(i int) string {
	return fmt.Sprintf("%s-%02d", s.cfg.Name, i)
}
