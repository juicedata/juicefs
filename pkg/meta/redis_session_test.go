//go:build !noredis

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

package meta

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type sessionPreloadHook struct {
	key     string
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (h *sessionPreloadHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *sessionPreloadHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *sessionPreloadHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "get" && len(cmd.Args()) > 1 && cmd.Args()[1] == h.key {
			h.once.Do(func() {
				close(h.entered)
				<-h.resume
			})
		}
		return err
	}
}

func TestRedisSessionLeaseCacheShutdown(t *testing.T) {
	client, err := newRedisMeta("redis", "127.0.0.1:6379/10?client-cache=true", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*redisMeta)
	defer m.Shutdown()
	if err := m.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatal(err)
	}
	m.cache.preload = 1
	m.cache.inodeCache.Purge()
	hook := &sessionPreloadHook{key: m.inodeKey(m.root), entered: make(chan struct{}), resume: make(chan struct{})}
	m.rdb.AddHook(hook)
	var release sync.Once
	resume := func() { release.Do(func() { close(hook.resume) }) }
	defer resume()
	done := make(chan struct{})
	go func() { m.preloadCache(); close(done) }()
	select {
	case <-hook.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("preload did not reach the metadata read")
	}
	m.loseSession()
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("preload did not stop after session shutdown")
	}
}

func TestRedisSessionLeaseBeforeCommit(t *testing.T) {
	for _, tc := range []struct{ mode, expireAt string }{
		{"transaction", "after read"},
		{"transaction", "after queue"},
		{"plain", "after read"},
		{"plain", "after queue"},
		{"manual", "after read"},
		{"manual", "after queue"},
	} {
		t.Run(tc.mode+"/"+tc.expireAt, func(t *testing.T) {
			client, err := newRedisMeta("redis", "127.0.0.1:6379/10", testConfig())
			if err != nil {
				t.Fatal(err)
			}
			m := client.(*redisMeta)
			defer m.Shutdown()
			m.lease.Store(&sessionLeaseState{refreshed: time.Now(), heartbeat: m.conf.Heartbeat})
			expire := func() {
				m.lease.Store(&sessionLeaseState{refreshed: time.Now().Add(-time.Hour), heartbeat: m.conf.Heartbeat})
			}
			ctx := Background()
			key := m.prefix + t.Name()
			if err := m.rdb.Set(ctx, key, "original", time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			defer m.rdb.Del(ctx, key)
			err = m.txn(ctx, func(tx *redis.Tx) error {
				if err := tx.Get(ctx, key).Err(); err != nil {
					return err
				}
				if tc.expireAt == "after read" {
					expire()
				}
				queue := func(pipe redis.Pipeliner) error {
					pipe.Set(ctx, key, "changed", time.Minute)
					if tc.expireAt == "after queue" {
						expire()
					}
					return nil
				}
				var err error
				switch tc.mode {
				case "transaction":
					_, err = m.pipelined(ctx, tx.TxPipelined, queue)
				case "plain":
					_, err = m.pipelined(ctx, tx.Pipelined, queue)
				case "manual":
					pipe := tx.Pipeline()
					_ = queue(pipe)
					_, err = m.execPipeline(ctx, pipe)
				}
				return err
			}, key)
			if !errors.Is(err, syscall.EIO) {
				t.Errorf("expired pipeline: %v", err)
			}
			if value, err := m.rdb.Get(ctx, key).Result(); err != nil || value != "original" {
				t.Fatalf("expired pipeline committed: value %q, error %v", value, err)
			}
		})
	}
}
