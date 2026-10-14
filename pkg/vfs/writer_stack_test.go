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

package vfs

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type flushStackHook struct {
	count atomic.Int32
	stack atomic.Bool
}

func (h *flushStackHook) Levels() []logrus.Level { return []logrus.Level{logrus.WarnLevel} }

func (h *flushStackHook) Fire(e *logrus.Entry) error {
	if strings.HasPrefix(e.Message, "All goroutines (") {
		h.count.Add(1)
		if strings.Contains(e.Message, "\ngoroutine ") {
			h.stack.Store(true)
		}
	}
	return nil
}

func observeFlushStacks(t *testing.T) *flushStackHook {
	t.Helper()
	flushStackMu.Lock()
	previous := lastFlushStackDump
	lastFlushStackDump = time.Time{}
	flushStackMu.Unlock()
	output, level := logger.Out, logger.GetLevel()
	hooks := logger.ReplaceHooks(make(logrus.LevelHooks))
	hook := &flushStackHook{}
	logger.SetOutput(io.Discard)
	logger.SetLevel(logrus.WarnLevel)
	logger.AddHook(hook)
	t.Cleanup(func() {
		logger.ReplaceHooks(hooks)
		logger.SetOutput(output)
		logger.SetLevel(level)
		flushStackMu.Lock()
		lastFlushStackDump = previous
		flushStackMu.Unlock()
	})
	return hook
}

func TestFlushTimeoutStackRateLimit(t *testing.T) {
	hook := observeFlushStacks(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			dumpFlushTimeoutStacks()
		}()
	}
	close(start)
	wg.Wait()
	dumpFlushTimeoutStacks()
	if n := hook.count.Load(); n != 1 {
		t.Fatalf("concurrent timeout diagnostics produced %d dumps, want 1", n)
	}
	if !hook.stack.Load() {
		t.Fatal("the admitted diagnostic did not contain a goroutine stack")
	}

	// Expire only this test's gate state instead of sleeping for a minute.
	flushStackMu.Lock()
	lastFlushStackDump = time.Now().Add(-2 * time.Minute)
	flushStackMu.Unlock()
	dumpFlushTimeoutStacks()
	if n := hook.count.Load(); n != 2 {
		t.Fatalf("expired diagnostic window produced %d total dumps, want 2", n)
	}
}

func TestFlushTimeoutStackBusy(t *testing.T) {
	hook := observeFlushStacks(t)
	flushStackMu.Lock()
	done := make(chan struct{})
	go func() {
		dumpFlushTimeoutStacks()
		close(done)
	}()
	blocked := false
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		blocked = true
	}
	flushStackMu.Unlock()
	<-done
	if blocked {
		t.Fatal("a busy stack diagnostic blocked another caller")
	}
	if n := hook.count.Load(); n != 0 {
		t.Fatalf("busy diagnostic produced %d dumps, want 0", n)
	}
	dumpFlushTimeoutStacks()
	if n := hook.count.Load(); n != 1 {
		t.Fatalf("busy attempt consumed the next diagnostic slot: %d dumps", n)
	}
}
