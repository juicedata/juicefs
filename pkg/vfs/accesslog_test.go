/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package vfs

import (
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

func TestAccessLog(t *testing.T) {
	openAccessLog(1)
	defer closeAccessLog(1)

	ctx := NewLogContext(meta.NewContext(10, 1, []uint32{2}))
	logit(ctx, "method", 0, "test")

	n := readAccessLog(2, nil)
	if n != 0 {
		t.Fatalf("invalid fd")
	}

	now := time.Now()
	// partial read
	buf := make([]byte, 1024)
	n = readAccessLog(1, buf[:10])
	if n != 10 {
		t.Fatalf("partial read: %d", n)
	}
	if time.Since(now) > time.Millisecond*10 {
		t.Fatalf("should not block")
	}

	// read whole line, block for 1 second
	n = readAccessLog(1, buf[10:])
	if n != 66 {
		t.Fatalf("partial read: %d", n)
	}
	logs := string(buf[:10+n])

	// check format
	ts, err := time.Parse("2006.01.02 15:04:05.000000", logs[:26])
	if err != nil {
		t.Fatalf("invalid time %s: %s", logs, err)
	}
	if now.Sub(ts.Local()) > time.Millisecond*10 {
		t.Fatalf("stale time: %s now: %s", ts, time.Now())
	}
	if logs[26:len(logs)-4] != " [uid:1,gid:2,pid:10] method test - OK <0.0000" {
		t.Fatalf("unexpected log: %q", logs[26:])
	}

	// block read
	n = readAccessLog(1, buf)
	if n != 2 || string(buf[:2]) != "#\n" {
		t.Fatalf("expected line: %q", string(buf[:n]))
	}
}
