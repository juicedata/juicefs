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

// Package credentials provides a small abstraction for object storage
// credentials that can be refreshed while the client is running.
package credentials

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

var logger = utils.GetLogger("juicefs")

// Credentials is a set of object storage credentials.
//
// Expires is the time at which the credentials become invalid. A zero value
// means the credentials do not expire.
type Credentials struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Expires         time.Time
}

// Provider returns credentials for object storage. Implementations must be
// safe for concurrent use.
type Provider interface {
	Get(ctx context.Context) (Credentials, error)
}

// Static always returns the same credentials. It is used for the traditional
// access-key/secret-key configuration and as a fallback when STS is
// temporarily unavailable.
type Static struct {
	Credentials Credentials
}

func (s Static) Get(context.Context) (Credentials, error) {
	return s.Credentials, nil
}

// Cached wraps another provider and refreshes credentials shortly before they
// expire. If a refresh fails, the previous credentials are returned as long as
// they have not expired yet. This makes a short STS outage invisible to the
// caller.
type Cached struct {
	provider Provider
	margin   time.Duration

	mu        sync.Mutex
	creds     Credentials
	refreshAt time.Time
	hasCreds  bool
	// Cache the last error for a short time so that a broken STS endpoint is
	// not hit by every object storage request.
	errUntil time.Time
	lastErr  error
}

const errorRetryInterval = 5 * time.Second

// NewCached creates a caching provider. margin is how long before expiration
// the credentials should be refreshed; a non-positive value defaults to five
// minutes.
func NewCached(p Provider, margin time.Duration) *Cached {
	if margin <= 0 {
		margin = 5 * time.Minute
	}
	return &Cached{provider: p, margin: margin}
}

func (c *Cached) Get(ctx context.Context) (Credentials, error) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasCreds && now.Before(c.refreshAt) {
		return c.creds, nil
	}
	// If a refresh failed recently but the current credentials are still
	// valid, keep using them instead of surfacing the cached error.
	if c.hasCreds && now.Before(c.creds.Expires) && now.Before(c.errUntil) {
		return c.creds, nil
	}
	if now.Before(c.errUntil) {
		return Credentials{}, c.lastErr
	}

	creds, err := c.provider.Get(ctx)
	// The provider call may have blocked for a while; use the current time for
	// the cache decisions below.
	now = time.Now()
	if err != nil {
		// Keep using the previous credentials until they really expire.
		if c.hasCreds && now.Before(c.creds.Expires) {
			return c.creds, nil
		}
		c.errUntil = now.Add(errorRetryInterval)
		c.lastErr = err
		return Credentials{}, err
	}
	c.errUntil = time.Time{}
	c.lastErr = nil
	if !creds.Expires.IsZero() && !creds.Expires.After(now) {
		err := fmt.Errorf("credentials returned by provider are already expired")
		c.errUntil = now.Add(errorRetryInterval)
		c.lastErr = err
		return Credentials{}, err
	}

	c.creds = creds
	c.hasCreds = true
	if creds.Expires.IsZero() {
		// Static credentials never expire; avoid calling the provider again.
		c.refreshAt = now.Add(24 * time.Hour)
	} else {
		// Do not let the refresh margin exceed half of the credential
		// lifetime. Some providers (Aliyun) only expose a short lease, and
		// refreshing on every request would be wasteful.
		margin := c.margin
		if lease := creds.Expires.Sub(now); margin >= lease {
			margin = lease / 2
		}
		c.refreshAt = creds.Expires.Add(-margin)
		if c.refreshAt.Before(now) {
			c.refreshAt = now
		}
	}
	return creds, nil
}

// Fallback tries the primary provider first and falls back to the secondary
// provider when the primary fails. A nil secondary provider disables fallback.
type Fallback struct {
	primary   Provider
	secondary Provider
	enabled   bool

	mu       sync.Mutex
	lastWarn time.Time
	retryAt  time.Time
}

// fallbackCooldown is how long the fallback provider waits before trying the
// primary provider again after a failure. Without it, every object storage
// request would block on a failing STS endpoint.
const fallbackCooldown = 30 * time.Second

// NewFallback creates a fallback provider. enabled is ignored when secondary is
// nil.
func NewFallback(primary, secondary Provider, enabled bool) *Fallback {
	if secondary == nil {
		enabled = false
	}
	return &Fallback{primary: primary, secondary: secondary, enabled: enabled}
}

func (f *Fallback) Get(ctx context.Context) (Credentials, error) {
	if f.primary == nil {
		if f.secondary != nil {
			return f.secondaryWithLease(ctx)
		}
		return Credentials{}, fmt.Errorf("no credential provider configured")
	}

	if f.enabled {
		f.mu.Lock()
		retryAt := f.retryAt
		f.mu.Unlock()
		if time.Now().Before(retryAt) {
			return f.secondaryWithLease(ctx)
		}
	}

	creds, err := f.primary.Get(ctx)
	if err == nil {
		f.mu.Lock()
		f.retryAt = time.Time{}
		f.mu.Unlock()
		return creds, nil
	}
	if !f.enabled {
		return Credentials{}, err
	}

	// Avoid flooding the log and hammering a broken STS endpoint: remember the
	// failure and use the static fallback for a short cooldown.
	f.mu.Lock()
	f.retryAt = time.Now().Add(fallbackCooldown)
	if time.Since(f.lastWarn) > time.Minute {
		logger.Warnf("STS credentials are unavailable, falling back to static credentials: %s", err)
		f.lastWarn = time.Now()
	}
	f.mu.Unlock()

	return f.secondaryWithLease(ctx)
}

func (f *Fallback) secondaryWithLease(ctx context.Context) (Credentials, error) {
	creds, err := f.secondary.Get(ctx)
	if err != nil {
		return Credentials{}, err
	}
	// Static credentials do not expire. Give them a short lease so that
	// credential caches (for example the AWS SDK cache) retry STS soon instead
	// of pinning the static fallback forever.
	now := time.Now()
	if creds.Expires.IsZero() || creds.Expires.After(now.Add(fallbackLease)) {
		creds.Expires = now.Add(fallbackLease)
	}
	return creds, nil
}

const fallbackLease = time.Minute
