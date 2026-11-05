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
	"errors"
	"syscall"
	"time"
)

// ErrSessionLost means that a new client instance and session are required.
var ErrSessionLost = errors.New("client session is permanently lost")

// SessionLease is the session lease saved during graceful restart.
type SessionLease struct {
	Sid         uint64
	Heartbeat   time.Duration
	LastRefresh int64 // Unix nanoseconds at the start of the last successful refresh
}

type sessionLeaseState struct {
	refreshed time.Time
	heartbeat time.Duration
	lost      bool
}

func (s *sessionLeaseState) age(now time.Time) time.Duration {
	if s == nil {
		return 0
	}
	age := now.Sub(s.refreshed)
	if wall := now.Round(0).Sub(s.refreshed.Round(0)); wall > age {
		age = wall
	}
	return age
}

func (s *sessionLeaseState) expired(now time.Time) bool {
	if s == nil || s.heartbeat <= 0 {
		return false
	}
	age := s.age(now)
	return age < 0 || age >= 4*s.heartbeat-time.Second
}

func (m *baseMeta) loseSession() {
	for {
		old := m.lease.Load()
		if old != nil && old.lost {
			return
		}
		if m.lease.CompareAndSwap(old, &sessionLeaseState{lost: true}) {
			m.notifySessionLost(old, "metadata session is missing, expired, or cannot be restored")
			return
		}
	}
}

func (m *baseMeta) notifySessionLost(previous *sessionLeaseState, reason string) {
	close(m.sessionLost)
	if m.sessCtx != nil {
		m.sessCtx.Cancel()
	}
	age, heartbeat := "unknown", m.conf.Heartbeat
	if previous != nil {
		age, heartbeat = previous.age(time.Now()).String(), previous.heartbeat
	}
	logger.Errorf("Session %d permanently lost: %s (last successful refresh age %s, heartbeat %s); create a new client to recover",
		m.sid, reason, age, heartbeat)
}

func (m *baseMeta) CheckSession() syscall.Errno {
	for {
		s := m.lease.Load()
		if s == nil {
			return 0
		}
		if s.lost {
			return syscall.EIO
		}
		if !s.expired(time.Now()) {
			return 0
		}
		if m.lease.CompareAndSwap(s, &sessionLeaseState{lost: true}) {
			m.notifySessionLost(s, "local lease expired")
			return syscall.EIO
		}
	}
}

func (m *baseMeta) restoreSessionLease() error {
	l := m.conf.SessionLease
	if m.conf.Heartbeat > 0 {
		if l == nil || l.Sid != m.sid || l.Heartbeat != m.conf.Heartbeat || l.LastRefresh <= 0 || time.Now().UnixNano() < l.LastRefresh {
			m.loseSession()
			return ErrSessionLost
		}
		old := m.lease.Load()
		if old != nil && old.lost {
			return ErrSessionLost
		}
		if !m.lease.CompareAndSwap(old, &sessionLeaseState{refreshed: time.Unix(0, l.LastRefresh), heartbeat: l.Heartbeat}) {
			return ErrSessionLost
		}
		if m.CheckSession() != 0 {
			return ErrSessionLost
		}
	}
	return nil
}

// refreshSession is serialized by sesMu. A late RPC cannot renew an expired lease.
func (m *baseMeta) refreshSession() {
	old := m.lease.Load()
	if old == nil || m.CheckSession() != 0 {
		return
	}
	start := time.Now()
	lost, err := m.en.doRefreshSession()
	if lost {
		m.loseSession()
		return
	}
	if err != nil {
		logger.Errorf("Refresh session %d: %s", m.sid, err)
		m.CheckSession()
		return
	}
	m.publishRefresh(old, start)
}

func (m *baseMeta) SessionLost() <-chan struct{} { return m.sessionLost }

func (m *baseMeta) ExportSessionLease() (SessionLease, error) {
	if m.CheckSession() != 0 {
		return SessionLease{}, ErrSessionLost
	}
	s := m.lease.Load()
	if s == nil {
		return SessionLease{}, nil
	}
	if s.lost {
		return SessionLease{}, ErrSessionLost
	}
	return SessionLease{Sid: m.sid, Heartbeat: s.heartbeat, LastRefresh: s.refreshed.UnixNano()}, nil
}

// publishRefresh must not replace a lease invalidated while the RPC was running.
func (m *baseMeta) publishRefresh(old *sessionLeaseState, start time.Time) bool {
	if m.CheckSession() != 0 {
		return false
	}
	if !m.lease.CompareAndSwap(old, &sessionLeaseState{refreshed: start, heartbeat: m.conf.Heartbeat}) {
		return false
	}
	return m.CheckSession() == 0
}

func (m *baseMeta) monitorSession(ctx Context) {
	interval := min(m.conf.Heartbeat/4, time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.CheckSession() != 0 {
				return
			}
		}
	}
}
