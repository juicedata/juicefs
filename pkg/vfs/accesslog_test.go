/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
	logit(ctx, "test")

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
	if n != 54 {
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
	if logs[26:len(logs)-4] != " [uid:1,gid:2,pid:10] test <0.0000" {
		t.Fatalf("unexpected log: %q", logs[26:])
	}

	// block read
	n = readAccessLog(1, buf)
	if n != 2 || string(buf[:2]) != "#\n" {
		t.Fatalf("expected line: %q", string(buf[:n]))
	}
}
