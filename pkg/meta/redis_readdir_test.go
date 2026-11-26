// JuiceFS, Copyright 2026 Juicedata, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !noredis

package meta

import (
	"context"
	"strconv"
	"syscall"
	"testing"
	"testing/synctest"

	"github.com/redis/go-redis/v9"
)

type redisReaddirClient struct {
	redis.UniversalClient
	fields      []string
	mgetStarted chan struct{}
}

func (c *redisReaddirClient) HScan(ctx context.Context, _ string, _ uint64, _ string, _ int64) *redis.ScanCmd {
	cmd := redis.NewScanCmd(ctx, nil)
	cmd.SetVal(c.fields, 0)
	return cmd
}

func (c *redisReaddirClient) MGet(ctx context.Context, _ ...string) *redis.SliceCmd {
	select {
	case c.mgetStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	cmd := redis.NewSliceCmd(ctx)
	cmd.SetErr(ctx.Err())
	return cmd
}

func TestRedisReaddirCanceledWhileFillingAttrs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Thirteen batches exceed the ten queued batches and two active workers.
		client := &redisReaddirClient{
			fields:      make([]string, 2*13*4096),
			mgetStarted: make(chan struct{}, 1),
		}
		m := &redisMeta{rdb: client}
		entry := string(m.packEntry(TypeFile, 2))
		for i := 0; i < len(client.fields); i += 2 {
			client.fields[i], client.fields[i+1] = strconv.Itoa(i/2), entry
		}
		ctx := NewContext(1, 0, []uint32{0})
		defer ctx.Cancel()
		go func() {
			select {
			case <-client.mgetStarted:
				ctx.Cancel()
			case <-ctx.Done():
			}
		}()
		var entries []*Entry
		if st := m.doReaddir(ctx, RootInode, 1, &entries, -1); st != syscall.EINTR {
			t.Fatalf("doReaddir returned %v after cancellation, want EINTR", st)
		}
	})
}
