/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"syscall"
	"testing"
	"time"
)

func TestErrno(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want syscall.Errno
	}{
		{"nil", nil, 0},
		{"errno", syscall.ENOSPC, syscall.ENOSPC},
		{"canceled", context.Canceled, syscall.EINTR},
		{"wrapped canceled", fmt.Errorf("operation: %w", context.Canceled), syscall.EINTR},
		{"stringified canceled", errors.New("operation: context canceled"), syscall.EINTR},
		{"deadline", context.DeadlineExceeded, syscall.ETIMEDOUT},
		{"wrapped deadline", fmt.Errorf("operation: %w", context.DeadlineExceeded), syscall.ETIMEDOUT},
		{"unknown", errors.New("unknown error"), syscall.EIO},
		{"non-Redis OOM", errors.New("OOM from another engine"), syscall.EIO},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := errno(tc.err); got != tc.want {
				t.Fatalf("errno(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRelatimeNeedUpdate(t *testing.T) {
	attr := &Attr{
		Atime: 1000,
	}
	if !relatimeNeedUpdate(attr, time.Now()) {
		t.Fatal("atime not updated for 24 hours")
	}

	now := time.Now()
	attr.Atime = now.Unix()
	attr.Ctime = now.Unix() + 10
	if !relatimeNeedUpdate(attr, time.Now()) {
		t.Fatal("atime not updated for ctime")
	}

	now = time.Now()
	attr.Atime = now.Unix()
	attr.Mtime = now.Unix() + 10
	if !relatimeNeedUpdate(attr, time.Now()) {
		t.Fatal("atime not updated for mtime")
	}

	now = time.Now()
	attr.Atime = now.Unix()
	attr.Mtime = now.Unix()
	attr.Ctime = now.Unix()
	if relatimeNeedUpdate(attr, now) {
		t.Fatal("atime should not be updated")
	}
}

func TestAtimeNeedsUpdate(t *testing.T) {
	m := &baseMeta{
		conf: &Config{
			AtimeMode: NoAtime,
		},
	}
	attr := &Attr{
		Atime: 1000,
	}
	now := time.Now()
	if m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime updated for noatime")
	}

	m.conf.AtimeMode = RelAtime
	if !m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime not updated for relatime")
	}
	attr.Atime = now.Unix()
	if m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime updated for relatime")
	}

	m.conf.AtimeMode = StrictAtime
	attr.Atime = now.Unix() - 2
	if !m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime not updated for strictatime")
	}

	attr.Atime = now.Unix() - 1
	attr.Atimensec = uint32(now.Nanosecond())
	if m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime updated for strictatime when < 1s")
	}
}

func Test_getCallerName(t *testing.T) {
	ctx := context.WithValue(context.Background(), txMethodKey{}, "test")
	var method txMethod
	if method.name(ctx) != "test" {
		t.Fatalf("expected %q, got %q", "test", method)
	}
	func() {
		var method txMethod
		if method.name(context.Background()) != "Test_getCallerName" {
			t.Fatalf("expected %q, got %q", "Test_getCallerName", method)
		}
	}()
}
