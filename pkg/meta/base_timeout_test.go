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
	"sync"
	"syscall"
	"testing"
	"time"
)

type timeoutBlockingEngine struct {
	engine
	counterStarted chan struct{}
	counterRelease <-chan struct{}
	counterDone    *sync.WaitGroup
	attrStarted    chan struct{}
	attrRelease    <-chan struct{}
	attrReturned   chan struct{}
}

func (e *timeoutBlockingEngine) getCounter(string) (int64, error) {
	e.counterStarted <- struct{}{}
	<-e.counterRelease
	e.counterDone.Done()
	return 1, nil
}

func (e *timeoutBlockingEngine) doGetAttr(Context, Ino, *Attr) syscall.Errno {
	close(e.attrStarted)
	<-e.attrRelease
	close(e.attrReturned)
	return syscall.EIO
}

func TestStatRootFsTimeoutCallbacksDoNotRace(t *testing.T) {
	m := newBaseMeta("", testConfig())
	t.Cleanup(m.of.close)
	m.setFormat(&Format{})

	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var callbacks sync.WaitGroup
	callbacks.Add(2)
	m.en = &timeoutBlockingEngine{
		counterStarted: started,
		counterRelease: release,
		counterDone:    &callbacks,
	}

	done := make(chan syscall.Errno, 1)
	go func() {
		var totalspace, availspace, iused, iavail uint64
		done <- m.statRootFs(Background(), &totalspace, &availspace, &iused, &iavail)
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for counter callback")
		}
	}
	close(release)
	callbacks.Wait()

	select {
	case st := <-done:
		if st != 0 {
			t.Fatalf("statRootFs: %s", st)
		}
	case <-time.After(time.Second):
		t.Fatal("statRootFs did not return")
	}
}

func TestGetAttrTimeoutCallbackCannotOverwriteFallback(t *testing.T) {
	m := newBaseMeta("", testConfig())
	t.Cleanup(m.of.close)

	release := make(chan struct{})
	returned := make(chan struct{})
	m.en = &timeoutBlockingEngine{
		attrStarted:  make(chan struct{}),
		attrRelease:  release,
		attrReturned: returned,
	}

	m.of.Lock()
	done := make(chan syscall.Errno, 1)
	go func() {
		done <- m.GetAttr(Background(), RootInode, &Attr{})
	}()
	<-m.en.(*timeoutBlockingEngine).attrStarted
	time.Sleep(500 * time.Millisecond)
	close(release)
	<-returned
	time.Sleep(10 * time.Millisecond)
	m.of.Unlock()

	select {
	case st := <-done:
		if st != 0 {
			t.Fatalf("GetAttr returned late callback error after timeout: %s", st)
		}
	case <-time.After(time.Second):
		t.Fatal("GetAttr did not return")
	}
}
