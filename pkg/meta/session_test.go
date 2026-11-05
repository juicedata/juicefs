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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type sessionTestEngine struct {
	engine
	refresh func() (bool, error)
}

func (e *sessionTestEngine) doRefreshSession() (bool, error) { return e.refresh() }

func TestSessionLease(t *testing.T) {
	conf := DefaultConf()
	conf.Heartbeat = time.Second
	conf.Sid = 1
	for _, tc := range []struct {
		name string
		age  time.Duration
		lost bool
	}{
		{"fresh", time.Second, false},
		{"expired", 3*time.Second + time.Millisecond, true},
		{"restored wall clock", 4 * time.Second, true},
		{"future restored wall clock", -time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newBaseMeta("", conf)
			start := time.Now().Add(-tc.age)
			if tc.name == "restored wall clock" || tc.name == "future restored wall clock" {
				start = start.Round(0)
			}
			m.lease.Store(&sessionLeaseState{refreshed: start, heartbeat: conf.Heartbeat})
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					st := m.CheckSession()
					if (st == syscall.EIO) != tc.lost {
						t.Errorf("CheckSession: %s", st)
					}
				}()
			}
			wg.Wait()
			select {
			case <-m.SessionLost():
				if !tc.lost {
					t.Fatal("fresh session was lost")
				}
			default:
				if tc.lost {
					t.Fatal("session loss was not signaled")
				}
			}
			if tc.lost {
				if m.publishRefresh(m.lease.Load(), time.Now()) {
					t.Fatal("lost session was renewed")
				}
				if !errors.Is(m.NewSession(true), ErrSessionLost) {
					t.Fatal("lost client started a new session")
				}
			} else if _, err := m.ExportSessionLease(); err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, conf := range []*Config{DefaultConf(), {ReadOnly: true}, {Heartbeat: 0, Sid: 1}} {
		m := newBaseMeta("", conf)
		if m.CheckSession() != 0 {
			t.Fatal("inactive session was rejected")
		}
		if l, err := m.ExportSessionLease(); err != nil || l != (SessionLease{}) {
			t.Fatalf("inactive lease: %+v, %v", l, err)
		}
	}
}

func TestSessionLeaseRefresh(t *testing.T) {
	for _, tc := range []string{"late success", "failure", "lost", "fresh success"} {
		t.Run(tc, func(t *testing.T) {
			conf := DefaultConf()
			conf.Sid = 1
			conf.Heartbeat = time.Second
			m := newBaseMeta("", conf)
			old := &sessionLeaseState{refreshed: time.Now(), heartbeat: conf.Heartbeat}
			m.lease.Store(old)
			m.en = &sessionTestEngine{refresh: func() (bool, error) {
				switch tc {
				case "late success":
					m.lease.Store(&sessionLeaseState{refreshed: time.Now().Add(-4 * time.Second), heartbeat: conf.Heartbeat})
				case "failure":
					return false, errors.New("injected refresh failure")
				case "lost":
					return true, nil
				}
				return false, nil
			}}
			m.refreshSession()
			if tc == "late success" || tc == "lost" {
				if m.CheckSession() != syscall.EIO {
					t.Fatal("invalid session became usable")
				}
			} else if tc == "failure" && m.lease.Load() != old {
				t.Fatal("failed refresh renewed lease")
			} else if tc == "fresh success" && m.lease.Load() == old {
				t.Fatal("successful refresh did not renew lease")
			}
		})
	}

	m := newBaseMeta("", DefaultConf())
	m.lease.Store(&sessionLeaseState{refreshed: time.Now().Add(-time.Hour), heartbeat: time.Second})
	m.en = &sessionTestEngine{refresh: func() (bool, error) { t.Fatal("expired session sent a refresh RPC"); return false, nil }}
	m.refreshSession()
	if m.CheckSession() != syscall.EIO {
		t.Fatal("expired session was not lost")
	}
}

func TestSessionLeaseRestore(t *testing.T) {
	for _, tc := range []string{"fresh", "missing", "expired", "future", "different sid", "different heartbeat"} {
		t.Run(tc, func(t *testing.T) {
			conf := DefaultConf()
			conf.Sid = 7
			conf.Heartbeat = time.Second
			conf.SessionLease = &SessionLease{Sid: 7, Heartbeat: time.Second, LastRefresh: time.Now().UnixNano()}
			switch tc {
			case "missing":
				conf.SessionLease = nil
			case "expired":
				conf.SessionLease.LastRefresh = time.Now().Add(-4 * time.Second).UnixNano()
			case "future":
				conf.SessionLease.LastRefresh = time.Now().Add(time.Second).UnixNano()
			case "different sid":
				conf.SessionLease.Sid++
			case "different heartbeat":
				conf.SessionLease.Heartbeat *= 2
			}
			m := newBaseMeta("", conf)
			err := m.restoreSessionLease()
			if (err == nil) != (tc == "fresh") {
				t.Fatalf("restore: %v", err)
			}
		})
	}
}

func TestSessionLeaseMonitor(t *testing.T) {
	conf := DefaultConf()
	conf.Heartbeat = time.Second
	m := newBaseMeta("", conf)
	m.sessCtx = Background()
	m.lease.Store(&sessionLeaseState{refreshed: time.Now().Add(-4 * time.Second), heartbeat: conf.Heartbeat})
	done := make(chan struct{})
	go func() { m.monitorSession(m.sessCtx); close(done) }()
	select {
	case <-m.SessionLost():
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not expire the session")
	}
	<-done
	// A lost-session close must not wait for a stalled heartbeat holding sesMu.
	m.sesMu.Lock()
	defer m.sesMu.Unlock()
	if !errors.Is(m.CloseSession(), ErrSessionLost) {
		t.Fatal("close should report lost session")
	}
}

func TestSessionLeaseCloseWaitingForRefresh(t *testing.T) {
	m := newBaseMeta("", DefaultConf())
	m.lease.Store(&sessionLeaseState{refreshed: time.Now(), heartbeat: time.Second})
	m.sesMu.Lock() // a heartbeat RPC is still in flight
	defer m.sesMu.Unlock()
	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() { close(entered); result <- m.CloseSession() }()
	<-entered
	select {
	case err := <-result:
		t.Fatalf("close returned before refresh finished or session was lost: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	m.loseSession()
	select {
	case err := <-result:
		if !errors.Is(err, ErrSessionLost) {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close remained blocked behind the heartbeat after session loss")
	}
}

func TestSessionLeaseResume(t *testing.T) {
	for _, tc := range []string{"live", "missing", "expired record", "expired lease", "heartbeat disabled"} {
		t.Run(tc, func(t *testing.T) {
			conf := DefaultConf()
			conf.NoBGJob = true
			conf.MaxDeletes = 0
			if tc == "heartbeat disabled" {
				conf.Heartbeat = 0
			}
			client, err := newKVMeta("memkv", "", conf)
			if err != nil {
				t.Fatal(err)
			}
			m := client.(*kvMeta)
			if err := m.Init(testFormat(), true); err != nil {
				t.Fatal(err)
			}
			if err := m.NewSession(true); err != nil {
				t.Fatal(err)
			}
			lease, err := m.ExportSessionLease()
			if err != nil {
				t.Fatal(err)
			}
			// Stop the old process's workers without deleting its session.
			m.sessCtx.Cancel()
			m.monitorWG.Wait()
			m.sessWG.Wait()
			switch tc {
			case "missing":
				if err := m.doCleanStaleSession(m.sid); err != nil {
					t.Fatal(err)
				}
			case "expired record":
				if err := m.setValue(m.sessionKey(m.sid), m.packInt64(time.Now().Unix()-1)); err != nil {
					t.Fatal(err)
				}
			case "expired lease":
				lease.LastRefresh = time.Now().Add(-time.Minute).UnixNano()
			}
			resumeConf := *conf
			resumeConf.Sid = lease.Sid
			resumeConf.SessionLease = &lease
			resumed := &kvMeta{baseMeta: newBaseMeta("", &resumeConf), client: m.client}
			resumed.en = resumed
			if _, err := resumed.Load(true); err != nil {
				t.Fatal(err)
			}
			err = resumed.NewSession(true)
			if tc == "live" || tc == "heartbeat disabled" {
				if err != nil {
					t.Fatal(err)
				}
				if resumed.sid != lease.Sid {
					t.Fatal("graceful restart changed the SID")
				}
				if err := resumed.CloseSession(); err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrSessionLost) {
					t.Fatalf("resume should fail closed: %v", err)
				}
				if resumed.CheckSession() != syscall.EIO {
					t.Fatal("failed resume remained usable")
				}
			}
		})
	}
}

func TestSessionLeaseHeartbeatCompatibility(t *testing.T) {
	for _, heartbeat := range []time.Duration{0, time.Nanosecond} {
		conf := DefaultConf()
		conf.NoBGJob, conf.MaxDeletes, conf.Heartbeat = true, 0, heartbeat
		m, err := newKVMeta("memkv", "", conf)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Init(testFormat(), true); err != nil {
			t.Fatal(err)
		}
		if err := m.NewSession(true); err != nil {
			t.Fatal(err)
		}
		lease, err := m.ExportSessionLease()
		if err != nil {
			t.Fatal(err)
		}
		want := heartbeat
		if want > 0 && want < time.Second {
			want = time.Second
		}
		if lease.Heartbeat != want {
			t.Fatalf("heartbeat %s normalized to %s", heartbeat, lease.Heartbeat)
		}
		if err := m.CloseSession(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionLeaseStopsBackgroundIO(t *testing.T) {
	m := newBaseMeta("", DefaultConf())
	called := false
	m.OnMsg(DeleteSlice, func(...interface{}) error { called = true; return nil })
	m.loseSession()
	if err := m.newMsg(DeleteSlice, uint64(1), uint32(1)); err != syscall.EIO || called {
		t.Fatalf("lost-session object callback: called %t, err %v", called, err)
	}
	// No engine is installed: these must return before making remote calls.
	m.doFlushStats()
	m.doFlushDirStat()
	m.doFlushQuotas()
	if err := m.syncVolumeStat(Background(), 0, 0); err != syscall.EIO {
		t.Fatalf("volume stat: %v", err)
	}
}

func TestSessionLeaseTransactionInvalidation(t *testing.T) {
	conf := DefaultConf()
	client, err := newKVMeta("memkv", "", conf)
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*kvMeta)
	key := []byte("session-lease-uncommitted-probe")
	err = m.txn(Background(), func(tx *kvTxn) error {
		tx.set(key, []byte("must not commit"))
		m.loseSession()
		return nil
	})
	if err != syscall.EIO {
		t.Fatalf("transaction after loss: %v", err)
	}
	if err := m.client.simpleTxn(Background(), func(tx *kvTxn) error {
		if tx.get(key) != nil {
			t.Error("transaction committed after session loss")
		}
		return nil
	}, 0); err != nil {
		t.Fatal(err)
	}
}

// Shared by every metadata family through testMeta.
func testSessionLeaseLoss(t *testing.T, m Meta) {
	b := m.getBase()
	if err := m.NewSession(true); err != nil {
		t.Fatal(err)
	}
	if lost, err := b.en.doRefreshSession(); err != nil || lost {
		t.Fatalf("live refresh: lost %t, %v", lost, err)
	}
	expired := time.Now().Unix() - 1
	switch e := m.(type) {
	case *redisMeta:
		err := e.rdb.ZAdd(Background(), e.allSessions(), redis.Z{Score: float64(expired), Member: e.sid}).Err()
		if err != nil {
			t.Fatal(err)
		}
	case *dbMeta:
		if _, err := e.db.Where("sid = ?", e.sid).Cols("expire").Update(&session2{Expire: expired}); err != nil {
			t.Fatal(err)
		}
	case *kvMeta:
		if err := e.setValue(e.sessionKey(e.sid), e.packInt64(expired)); err != nil {
			t.Fatal(err)
		}
	}
	if lost, err := b.en.doRefreshSession(); err != nil || !lost {
		t.Fatalf("expired record refresh: lost %t, %v", lost, err)
	}
	if err := b.en.doNewSession(b.currentSessionInfo(), true); !errors.Is(err, ErrSessionLost) {
		t.Fatalf("resume expired session: %v", err)
	}
	if err := b.en.doCleanStaleSession(b.sid); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if lost, err := b.en.doRefreshSession(); err != nil || !lost {
			t.Fatalf("cleaned refresh: lost %t, %v", lost, err)
		}
	}
	if err := b.en.doNewSession(b.currentSessionInfo(), true); !errors.Is(err, ErrSessionLost) {
		t.Fatalf("resume missing session: %v", err)
	}
	if _, err := m.GetSession(b.sid, false); err == nil {
		t.Fatal("missing session was recreated")
	}
	b.refreshSession()
	if m.CheckSession() != syscall.EIO {
		t.Fatal("cleaned session remained usable")
	}
	ctx := Background()
	var attr Attr
	if st := m.GetAttr(ctx, 1, &attr); st != syscall.EIO {
		t.Fatalf("getattr: %s", st)
	}
	if st := m.Access(ctx, 1, MODE_MASK_R, &Attr{Typ: TypeDirectory, Mode: 0777}); st != syscall.EIO {
		t.Fatalf("cached access: %s", st)
	}
	if st := m.Write(ctx, 1, 0, 0, Slice{}, time.Now()); st != syscall.EIO {
		t.Fatalf("write: %s", st)
	}
	if st := m.CopyFileRange(ctx, 1, 0, 1, 0, 1, 0, nil, nil); st != syscall.EIO {
		t.Fatalf("copy: %s", st)
	}
	if st := m.Flock(ctx, 1, 1, F_WRLCK, false); st != syscall.EIO {
		t.Fatalf("flock: %s", st)
	}
	if st := m.Setlk(ctx, 1, 1, false, F_WRLCK, 0, 1, 1); st != syscall.EIO {
		t.Fatalf("setlk: %s", st)
	}
	if _, err := m.ExportSessionLease(); !errors.Is(err, ErrSessionLost) {
		t.Fatalf("export lost lease: %v", err)
	}
	if !errors.Is(m.CloseSession(), ErrSessionLost) {
		t.Fatal("close lost session")
	}
}
