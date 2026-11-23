/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type pausedDirStatFlushEngine struct {
	engine
	entered chan chan struct{}
}

func (e *pausedDirStatFlushEngine) doUpdateDirStat(ctx Context, stats map[Ino]dirStat) error {
	release := make(chan struct{})
	e.entered <- release
	<-release
	return e.engine.doUpdateDirStat(ctx, stats)
}

type pausedDirStatReadEngine struct {
	engine
	inode            Ino
	entered, release chan struct{}
}

func (e *pausedDirStatReadEngine) doGetDirStat(ctx Context, inode Ino, trySync bool) (*dirStat, syscall.Errno) {
	stat, st := e.engine.doGetDirStat(ctx, inode, trySync)
	if inode == e.inode {
		close(e.entered)
		<-e.release
	}
	return stat, st
}

func TestDirQuotaAcrossStatFlush(t *testing.T) {
	for _, backend := range []string{"sqlite3", "badger", "redis"} {
		t.Run(backend, func(t *testing.T) {
			for _, timing := range []string{"before", "during", "after", "read-crosses-flush", "overlapping-flushes"} {
				t.Run(timing, func(t *testing.T) {
					var m Meta
					var err error
					if backend == "sqlite3" {
						m, err = newSQLMeta("sqlite3", filepath.Join(t.TempDir(), "meta.db"), testConfig())
					} else if backend == "badger" {
						m, err = newKVMeta("badger", t.TempDir(), testConfig())
					} else {
						m, err = newRedisMeta("redis", "127.0.0.1:6379/10", testConfig())
					}
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = m.Shutdown() })
					if err := m.Reset(); err != nil {
						t.Fatal(err)
					}
					if err := m.Init(testFormat(), true); err != nil {
						t.Fatal(err)
					}
					ctx := Background()
					var parent, child Ino
					var attr Attr
					if st := m.Mkdir(ctx, RootInode, "parent", 0755, 0, 0, &parent, &attr); st != 0 {
						t.Fatal(st)
					}
					// Establish a durable zero statistic before adding the child.
					if _, st := m.GetDirStat(ctx, parent); st != 0 {
						t.Fatal(st)
					}
					if st := m.Mkdir(ctx, parent, "child", 0755, 0, 0, &child, &attr); st != 0 {
						t.Fatal(st)
					}
					base := m.getBase()
					realEngine := base.en
					wantInodes := int64(1)
					var finish func()
					var crossed *pausedDirStatReadEngine
					switch timing {
					case "during", "overlapping-flushes":
						paused := &pausedDirStatFlushEngine{engine: base.en, entered: make(chan chan struct{})}
						base.en = paused
						done := make(chan struct{})
						go func() { base.doFlushDirStat(); close(done) }()
						release := <-paused.entered
						if timing == "overlapping-flushes" {
							if st := m.Mkdir(ctx, parent, "child2", 0755, 0, 0, &child, &attr); st != 0 {
								t.Fatal(st)
							}
							secondDone := make(chan struct{})
							go func() { base.doFlushDirStat(); close(secondDone) }()
							secondRelease := <-paused.entered
							close(release)
							<-done
							done, release = secondDone, secondRelease
							wantInodes = 2
						}
						finish = func() { close(release); <-done }
					case "read-crosses-flush":
						crossed = &pausedDirStatReadEngine{engine: base.en, inode: parent, entered: make(chan struct{}), release: make(chan struct{})}
						base.en = crossed
					case "after":
						base.doFlushDirStat()
					}
					setQuota := func() error {
						return m.HandleQuota(ctx, QuotaSet, "/parent", DirQuotaType, map[string]*Quota{"/parent": {MaxSpace: 1 << 30, MaxInodes: 100}}, false, false, false)
					}
					var quotaErr error
					if crossed != nil {
						initialized := make(chan error, 1)
						go func() { initialized <- setQuota() }()
						<-crossed.entered
						base.doFlushDirStat()
						close(crossed.release)
						quotaErr = <-initialized
						base.en = crossed.engine
					} else if finish == nil {
						quotaErr = setQuota()
					} else {
						initialized := make(chan error, 1)
						go func() { initialized <- setQuota() }()
						select {
						case quotaErr = <-initialized:
						case <-time.After(250 * time.Millisecond):
							finish()
							finish = nil
							quotaErr = <-initialized
						}
					}
					if finish != nil {
						finish()
					}
					base.en = realEngine
					if quotaErr != nil {
						t.Fatal(quotaErr)
					}
					q, err := base.en.doGetQuota(ctx, DirQuotaType, uint64(parent))
					if err != nil || q == nil {
						t.Fatalf("quota: %+v, %v", q, err)
					}
					t.Logf("initial quota: %+v", q)
					if q.UsedSpace != 4096*wantInodes || q.UsedInodes != wantInodes {
						t.Errorf("initial quota must include child: %+v", q)
					}
					base.loadQuotas()
					if st := m.Rmdir(ctx, parent, "child", true); st != 0 {
						t.Fatal(st)
					}
					if wantInodes == 2 {
						if st := m.Rmdir(ctx, parent, "child2", true); st != 0 {
							t.Fatal(st)
						}
					}
					base.doFlushQuotas()
					q, err = base.en.doGetQuota(ctx, DirQuotaType, uint64(parent))
					if err != nil || q == nil {
						t.Fatalf("quota after rmdir: %+v, %v", q, err)
					}
					t.Logf("after rmdir quota: %+v", q)
					if q.UsedSpace != 0 || q.UsedInodes != 0 {
						t.Errorf("quota must be empty after rmdir: %+v", q)
					}
				})
			}
		})
	}
}
