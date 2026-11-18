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

package credentials

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	creds Credentials
	err   error
	calls int32
}

func (f *fakeProvider) Get(context.Context) (Credentials, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.creds, f.err
}

func TestStaticProvider(t *testing.T) {
	p := Static{Credentials: Credentials{AccessKeyID: "ak", AccessKeySecret: "sk"}}
	got, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "ak" || got.AccessKeySecret != "sk" {
		t.Fatalf("unexpected credentials: %+v", got)
	}
}

func TestCachedProvider(t *testing.T) {
	f := &fakeProvider{creds: Credentials{AccessKeyID: "ak", Expires: time.Now().Add(time.Hour)}}
	c := NewCached(f, 5*time.Minute)

	for i := 0; i < 3; i++ {
		got, err := c.Get(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got.AccessKeyID != "ak" {
			t.Fatalf("unexpected credentials: %+v", got)
		}
	}
	if calls := atomic.LoadInt32(&f.calls); calls != 1 {
		t.Fatalf("provider should be called once, got %d", calls)
	}
}

func TestCachedProviderRefresh(t *testing.T) {
	f := &fakeProvider{creds: Credentials{AccessKeyID: "old", Expires: time.Now().Add(100 * time.Millisecond)}}
	c := NewCached(f, 50*time.Millisecond)

	if _, err := c.Get(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.creds = Credentials{AccessKeyID: "new", Expires: time.Now().Add(time.Hour)}
	time.Sleep(60 * time.Millisecond)

	got, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "new" {
		t.Fatalf("expected refreshed credentials, got %+v", got)
	}
	if calls := atomic.LoadInt32(&f.calls); calls != 2 {
		t.Fatalf("provider should be called twice, got %d", calls)
	}
}

func TestCachedProviderKeepsCredentialsOnError(t *testing.T) {
	c := NewCached(&fakeProvider{err: errors.New("sts is down")}, 5*time.Minute)
	c.creds = Credentials{AccessKeyID: "cached", Expires: time.Now().Add(time.Hour)}
	c.hasCreds = true
	c.refreshAt = time.Now().Add(-time.Minute)

	got, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "cached" {
		t.Fatalf("expected cached credentials, got %+v", got)
	}
}

func TestCachedProviderUsesValidCredentialsDuringErrorCache(t *testing.T) {
	c := NewCached(&fakeProvider{err: errors.New("sts is down")}, 5*time.Minute)
	c.creds = Credentials{AccessKeyID: "cached", Expires: time.Now().Add(time.Hour)}
	c.hasCreds = true
	c.refreshAt = time.Now().Add(-time.Minute)
	c.errUntil = time.Now().Add(time.Minute)
	c.lastErr = errors.New("sts is down")

	got, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "cached" {
		t.Fatalf("expected cached credentials, got %+v", got)
	}
}

func TestCachedProviderErrorCache(t *testing.T) {
	f := &fakeProvider{err: errors.New("sts is down")}
	c := NewCached(f, 5*time.Minute)

	if _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if calls := atomic.LoadInt32(&f.calls); calls != 1 {
		t.Fatalf("provider should not be retried immediately, got %d calls", calls)
	}
}

func TestCachedProviderExpiredError(t *testing.T) {
	c := NewCached(&fakeProvider{err: errors.New("sts is down")}, 5*time.Minute)
	c.creds = Credentials{AccessKeyID: "cached", Expires: time.Now().Add(-time.Minute)}
	c.hasCreds = true
	c.refreshAt = time.Now().Add(-time.Minute)

	if _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expected an error after cached credentials expired")
	}
}

func TestFallbackProvider(t *testing.T) {
	primary := &fakeProvider{err: errors.New("sts is down")}
	secondary := Static{Credentials: Credentials{AccessKeyID: "static"}}

	p := NewFallback(primary, secondary, true)
	got, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "static" {
		t.Fatalf("expected static fallback, got %+v", got)
	}
	if got.Expires.IsZero() || time.Until(got.Expires) > 2*time.Minute {
		t.Fatalf("static fallback should have a short lease, got %s", got.Expires)
	}

	p = NewFallback(primary, secondary, false)
	if _, err := p.Get(context.Background()); err == nil {
		t.Fatal("expected an error when fallback is disabled")
	}

	p = NewFallback(primary, nil, true)
	if _, err := p.Get(context.Background()); err == nil {
		t.Fatal("expected an error when there is no secondary provider")
	}
}

func TestFallbackCooldown(t *testing.T) {
	primary := &fakeProvider{err: errors.New("sts is down")}
	secondary := Static{Credentials: Credentials{AccessKeyID: "static"}}
	f := NewFallback(primary, secondary, true)

	if _, err := f.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := atomic.LoadInt32(&primary.calls); calls != 1 {
		t.Fatalf("primary should not be retried during the cooldown, got %d calls", calls)
	}

	// Simulate the cooldown expiring and STS recovering.
	f.mu.Lock()
	f.retryAt = time.Now().Add(-time.Second)
	f.mu.Unlock()
	primary.err = nil
	primary.creds = Credentials{AccessKeyID: "sts", Expires: time.Now().Add(time.Hour)}

	got, err := f.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "sts" {
		t.Fatalf("expected STS credentials after recovery, got %+v", got)
	}
	if calls := atomic.LoadInt32(&primary.calls); calls != 2 {
		t.Fatalf("primary should be retried after the cooldown, got %d calls", calls)
	}
}
