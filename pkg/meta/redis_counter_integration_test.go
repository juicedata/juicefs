//go:build !noredis
// +build !noredis

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
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Start a private Unix-socket Redis; never use a shared metadata database.
func counterTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("private Redis fixture requires Unix")
	}
	binary := os.Getenv("OWNER_REDIS_SERVER")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("redis-server")
		if err != nil {
			t.Skip("redis-server is not installed")
		}
	}
	dir, err := os.MkdirTemp("", "jfs-redis-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--maxmemory", "64mb", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, Protocol: 2, DisableIdentity: true, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("private Redis did not become ready")
	return nil
}

// Reject the atomic group before transport, but allow ordinary INCRBY through.
// This exposes a counter command issued outside its transaction callback's pipe.
type counterCommitBarrier struct {
	blocked bool
	err     error
}

func (h *counterCommitBarrier) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *counterCommitBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.blocked && (cmd.Name() == "eval" || cmd.Name() == "evalsha") {
			cmd.SetErr(h.err)
			return h.err
		}
		return next(ctx, cmd)
	}
}
func (h *counterCommitBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h.blocked {
			for _, cmd := range cmds {
				cmd.SetErr(h.err)
			}
			return h.err
		}
		return next(ctx, cmds)
	}
}
func counterTestMeta(client *redis.Client, prefix string, enabled, readonly bool) *redisMeta {
	m := &redisMeta{baseMeta: &baseMeta{conf: &Config{ReadOnly: readonly}}, rdb: client, prefix: prefix}
	m.setFormat(&Format{ChangeLog: enabled})
	return m
}

func TestRedisCounterChangelog(t *testing.T) {
	client := counterTestRedis(t)
	ctx := Background()
	for _, name := range []string{"nextSession", "nextInode", "nextChunk"} {
		cases := []struct {
			name, initial string
			delta, want   int64
		}{
			{"enabled", "100", 7, 107}, {"negative", "100", -7, 93}, {"zero", "100", 0, 100},
			{"large", "9007199254740992", 7, 9007199254740999}, {"negative-large", "-9007199254740992", -7, -9007199254740999},
			{"missing", "", 7, 7}, {"rejected", "100", 7, 100}, {"disabled", "100", 7, 107}, {"readonly", "100", 7, 100},
			{"overflow", fmt.Sprint(int64(math.MaxInt64)), 7, math.MaxInt64}, {"wrong-type", "not-an-integer", 7, 0},
		}
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				local := redis.NewClient(client.Options())
				t.Cleanup(func() { _ = local.Close() })
				barrier := &counterCommitBarrier{err: errors.New("atomic group rejected before transport")}
				local.AddHook(barrier)
				m := counterTestMeta(local, t.Name()+":", tc.name != "disabled", tc.name == "readonly")
				if tc.initial != "" {
					if err := client.Set(ctx, m.counterKey(name), tc.initial, 0).Err(); err != nil {
						t.Fatal(err)
					}
				}
				barrier.blocked = tc.name == "rejected"
				value, err := m.incrCounter(name, tc.delta)
				wantLogs := int64(1)
				switch tc.name {
				case "rejected":
					if !errors.Is(err, barrier.err) {
						t.Errorf("error=%v, want rejection", err)
					}
					wantLogs = 0
				case "readonly":
					if !errors.Is(err, syscall.EROFS) {
						t.Errorf("error=%v, want EROFS", err)
					}
					wantLogs = 0
				case "overflow", "wrong-type":
					if err == nil {
						t.Error("invalid counter succeeded")
					}
					wantLogs = 0
				default:
					want := tc.want
					if name == "nextInode" || name == "nextChunk" {
						want++
					}
					if err != nil || value != want {
						t.Errorf("got (%d,%v), want (%d,nil)", value, err, want)
					}
					if tc.name == "disabled" {
						wantLogs = 0
					}
				}
				stored, getErr := client.Get(ctx, m.counterKey(name)).Result()
				wantStored := fmt.Sprint(tc.want)
				if tc.name == "wrong-type" {
					wantStored = tc.initial
				}
				if getErr != nil || stored != wantStored {
					t.Errorf("counter=(%q,%v), want %q", stored, getErr, wantStored)
				}
				logs, logErr := client.LRange(ctx, m.txnLogKey(), 0, -1).Result()
				version, versionErr := client.Get(ctx, m.txnLastLog()).Int64()
				if versionErr == redis.Nil {
					versionErr = nil
				}
				if logErr != nil || int64(len(logs)) != wantLogs || versionErr != nil || version != wantLogs {
					t.Errorf("logs=%v version=%d errors=(%v,%v), want %d", logs, version, logErr, versionErr, wantLogs)
				}
				if wantLogs == 1 && len(logs) == 1 && !strings.Contains(logs[0], fmt.Sprintf("|INCR_COUNTER(%s,%d)|", name, tc.delta)) {
					t.Errorf("unexpected log: %s", logs[0])
				}
			})
		}
	}
}

func TestRedisCounterChangelogConcurrent(t *testing.T) {
	client := counterTestRedis(t)
	ctx := Background()
	for _, name := range []string{"nextSession", "nextInode", "nextChunk"} {
		t.Run(name, func(t *testing.T) {
			// Safe only because this fixture owns the entire Redis server.
			if err := client.ScriptFlush(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			m := counterTestMeta(client, t.Name()+":", true, false)
			const workers, perWorker = 8, 16
			values := make(chan int64, workers*perWorker)
			errs := make(chan error, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < perWorker; j++ {
						value, err := m.incrCounter(name, 1)
						if err != nil {
							errs <- err
							return
						}
						values <- value
					}
				}()
			}
			wg.Wait()
			close(errs)
			close(values)
			for err := range errs {
				t.Error(err)
			}
			seen := make(map[int64]bool)
			for value := range values {
				if seen[value] {
					t.Errorf("duplicate allocation %d", value)
				}
				seen[value] = true
			}
			const total = workers * perWorker
			if len(seen) != total {
				t.Errorf("unique allocations=%d, want %d", len(seen), total)
			}
			offset := int64(0)
			if name != "nextSession" {
				offset = 1
			}
			for v := int64(1); v <= total; v++ {
				if !seen[v+offset] {
					t.Errorf("missing allocation %d", v+offset)
				}
			}
			counter, err := client.Get(ctx, m.counterKey(name)).Int64()
			logs, logErr := client.LRange(ctx, m.txnLogKey(), 0, -1).Result()
			version, versionErr := client.Get(ctx, m.txnLastLog()).Int64()
			if err != nil || logErr != nil || versionErr != nil || counter != total || len(logs) != total || version != total {
				t.Errorf("counter=%d logs=%d version=%d errors=(%v,%v,%v), want %d", counter, len(logs), version, err, logErr, versionErr, total)
			}
		})
	}
}

func TestRedisCounterChangelogPublicAPI(t *testing.T) {
	client := counterTestRedis(t)
	m, err := newRedisMeta("unix", client.Options().Addr, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.CloseSession(); _ = m.Shutdown() })
	if err := m.Init(&Format{Name: "counter-test", DirStats: true, ChangeLog: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.NewSession(true); err != nil {
		t.Fatal(err)
	}
	ctx := Background()
	var inode Ino
	if st := m.Create(ctx, RootInode, "counter-file", 0644, 022, 0, &inode, &Attr{}); st != 0 {
		t.Fatal(st)
	}
	var slice uint64
	if st := m.NewSlice(ctx, &slice); st != 0 {
		t.Fatal(st)
	}
	if inode == 0 || slice == 0 || m.getBase().sid == 0 {
		t.Fatalf("invalid allocations: inode=%d slice=%d sid=%d", inode, slice, m.getBase().sid)
	}
	r := m.(*redisMeta)
	logs, err := client.LRange(ctx, r.txnLogKey(), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Get(ctx, r.txnLastLog()).Int64()
	if err != nil {
		t.Fatal(err)
	}
	if version != int64(len(logs)) {
		t.Fatalf("log version=%d, entries=%d", version, len(logs))
	}
	for _, name := range []string{"nextSession", "nextInode", "nextChunk"} {
		found := false
		for _, entry := range logs {
			if strings.Contains(entry, "|INCR_COUNTER("+name+",") {
				found = true
			}
		}
		if !found {
			t.Errorf("missing public allocator log for %s", name)
		}
	}
	stop := errors.New("snapshot consumed")
	var scanned int64
	// Zero means tail from the current version; -1 reads the retained history.
	err = m.ScanChangelog(ctx, -1, func(ver int64, entry string) error {
		scanned++
		if ver != scanned || entry != logs[scanned-1] {
			t.Errorf("changed log order/content at %d", ver)
		}
		if ver == version {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || scanned != version {
		t.Fatalf("scan=(%d,%v), want (%d,stop)", scanned, err, version)
	}
}
