/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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

package object

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// badShard returns its keys out of order. ListAll reacts to that the same way
// it reacts to a List that keeps failing: it logs and sends a nil object to
// tell the consumer the listing is incomplete.
type badShard struct {
	ObjectStorage // only String, ListAll and List are reached
}

func (s *badShard) String() string { return "bad://" }

func (s *badShard) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan Object, error) {
	return nil, notSupported
}

func (s *badShard) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if marker != "" {
		return nil, false, "", nil
	}
	return []Object{
		&obj{key: "b", mtime: time.Unix(0, 0)},
		&obj{key: "a", mtime: time.Unix(0, 0)},
	}, false, "", nil
}

func TestShardedListAllReportsAFailedShard(t *testing.T) {
	ctx := context.Background()

	good, err := newMem("good", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := good.Put(ctx, "c", bytes.NewReader([]byte("c"))); err != nil {
		t.Fatal(err)
	}

	s := &sharded{stores: []ObjectStorage{&badShard{}, good}}
	ch, err := s.ListAll(ctx, "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	var keys []string
	sawNil := false
	for o := range ch {
		if o == nil {
			sawNil = true
			break
		}
		keys = append(keys, o.Key())
	}

	// pkg/sync reads a nil as "listing failed, stop syncing". Without it a
	// sync against a sharded source treats the missing keys as deleted.
	if !sawNil {
		t.Errorf("a failed shard was not reported, got keys %v and a clean close", keys)
	}
}
