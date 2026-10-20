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
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type testRedisError string

func (e testRedisError) Error() string { return string(e) }
func (testRedisError) RedisError()     {}

func TestRedisErrno(t *testing.T) {
	oom := testRedisError("OOM command not allowed when used memory > 'maxmemory'.")
	for _, tc := range []struct {
		name string
		err  error
		want syscall.Errno
	}{
		{"nil reply", redis.Nil, syscall.ENOENT},
		{"OOM", oom, syscall.ENOSPC},
		{"wrapped OOM", fmt.Errorf("operation: %w", oom), syscall.ENOSPC},
		{"ERR OOM", testRedisError("ERR " + oom), syscall.ENOSPC},
		{"other error", testRedisError("ERR unknown command"), syscall.EIO},
		{"different error code", testRedisError("OOMOTHER error"), syscall.EIO},
		{"EXECABORT", testRedisError("EXECABORT Transaction discarded because of previous errors."), syscall.EIO},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := errno(tc.err); got != tc.want {
				t.Fatalf("errno(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRedisOOM(t *testing.T) {
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	socket := filepath.Join(t.TempDir(), "redis.sock")
	cmd := exec.Command(server, "--port", "0", "--unixsocket", socket,
		"--save", "", "--appendonly", "no", "--maxmemory-policy", "noeviction")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	ctx := Background()
	rdb := redis.NewClient(&redis.Options{
		Network: "unix", Addr: socket, MaxRetries: -1,
		DialTimeout: 100 * time.Millisecond,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for rdb.Ping(ctx).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("redis-server did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	client, err := newRedisMeta("unix", socket, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*redisMeta)
	t.Cleanup(func() { _ = m.Shutdown() })
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatal(err)
	}
	var ino Ino
	if st := m.Mknod(ctx, RootInode, "file", TypeFile, 0644, 0, 0, "", &ino, nil); st != 0 {
		t.Fatal(st)
	}
	// Only this private server is affected; one byte is below its startup usage.
	if err := rdb.ConfigSet(ctx, "maxmemory", "1").Err(); err != nil {
		t.Fatal(err)
	}
	err = rdb.Set(ctx, "oom-probe", "value", 0).Err()
	if !redis.HasErrorPrefix(err, "OOM ") {
		t.Fatalf("expected a real Redis OOM, got %v", err)
	}
	t.Logf("Redis SET: %v; errno: %d (%s)", err, errno(err), errno(err))
	if got := errno(err); got != syscall.ENOSPC {
		t.Fatalf("errno(OOM) = %v, want ENOSPC", got)
	}
	var id uint64
	if st := m.NewSlice(ctx, &id); st != syscall.ENOSPC {
		t.Fatalf("NewSlice = %v, want ENOSPC", st)
	} else {
		t.Logf("NewSlice: %d (%s)", st, st)
	}
	// EXECABORT hides the underlying OOM and must not be treated as an OOM itself.
	txnCtx := ctx.WithValue(txMaxRetryKey{}, 1)
	if st := m.Truncate(txnCtx, ino, 0, 4096, nil, false); st != syscall.EIO {
		t.Fatalf("Truncate with EXECABORT = %v, want EIO", st)
	} else {
		t.Logf("Truncate with EXECABORT: %d (%s)", st, st)
	}
	if err := rdb.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if st := m.NewSlice(ctx, &id); st != 0 {
		t.Fatalf("NewSlice after recovery: %v", st)
	}
	if st := m.Truncate(ctx, ino, 0, 4096, nil, false); st != 0 {
		t.Fatalf("Truncate after recovery: %v", st)
	}
	t.Log("NewSlice and Truncate succeeded after restoring maxmemory")
}
