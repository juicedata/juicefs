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
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestFormatUpdateJSON(t *testing.T) {
	f := testFormat()
	f.Capacity = 1 << 30
	body, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	fields["FutureSetting"] = json.RawMessage(`{"enabled":true}`)
	fields["capacity"] = fields["Capacity"]
	body, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	f.setRevision(body)
	f.Capacity = 0
	u, err := f.prepareUpdate(body, false)
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]json.RawMessage
	if err = json.Unmarshal(u.data, &updated); err != nil {
		t.Fatal(err)
	}
	var got, want bytes.Buffer
	if err = json.Compact(&got, updated["FutureSetting"]); err != nil {
		t.Fatal(err)
	}
	if err = json.Compact(&want, fields["FutureSetting"]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("unknown field lost: %s", u.data)
	}
	if _, ok := updated["Capacity"]; ok {
		t.Fatalf("omitempty update retained old capacity: %s", u.data)
	}
	if _, ok := updated["capacity"]; ok {
		t.Fatalf("old JSON alias retained: %s", u.data)
	}
	if _, err = f.prepareUpdate(nil, false); !errors.Is(err, ErrFormatChanged) {
		t.Fatalf("deleted format accepted: %v", err)
	}
	changed := append(append([]byte(nil), body...), '\n')
	if _, err = f.Clone().prepareUpdate(changed, false); !errors.Is(err, ErrFormatChanged) {
		t.Fatalf("changed revision accepted: %v", err)
	}
	if _, err = f.prepareUpdate(changed, true); err != nil {
		t.Fatalf("explicit force rejected: %v", err)
	}
	if bytes.Contains(u.data, []byte("revision")) {
		t.Fatalf("local revision persisted: %s", u.data)
	}
}

func testFormatUpdates(t *testing.T, m Meta) {
	defer func() {
		if err := m.Reset(); err != nil {
			t.Fatal(err)
		}
	}()
	for _, action := range []string{"stale", "repeat", "concurrent", "force", "deleted"} {
		t.Run(action, func(t *testing.T) {
			if err := m.Reset(); err != nil {
				t.Fatal(err)
			}
			initial := testFormat()
			initial.Capacity, initial.TrashDays = 1<<30, 1
			if err := m.Init(initial, false); err != nil {
				t.Fatal(err)
			}
			a, err := m.Load(false)
			if err != nil {
				t.Fatal(err)
			}
			b := a.Clone()
			a.Capacity, b.TrashDays = 2<<30, 0
			if m.GetFormat().Capacity != 1<<30 {
				t.Fatal("editing a loaded snapshot changed the published format")
			}
			if action == "concurrent" {
				writers := []Meta{m, m}
				if db, ok := m.(*dbMeta); ok {
					peer, err := newSQLMeta(db.Name(), strings.TrimPrefix(db.addr, "postgres://"), testConfig())
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Shutdown()
					b, err = peer.Load(false)
					if err != nil {
						t.Fatal(err)
					}
					b.TrashDays = 0
					writers[1] = peer // A separate Init mutex must not protect this write.
				}
				start, results := make(chan struct{}), make(chan error, 2)
				for i, f := range []*Format{a, b} {
					go func(writer Meta) { <-start; results <- writer.Init(f, false) }(writers[i])
				}
				close(start)
				var succeeded, conflicted int
				for range 2 {
					switch err := <-results; {
					case err == nil:
						succeeded++
					case errors.Is(err, ErrFormatChanged):
						conflicted++
					default:
						t.Fatal(err)
					}
				}
				if succeeded != 1 || conflicted != 1 {
					t.Fatalf("successes=%d conflicts=%d", succeeded, conflicted)
				}
				return
			}
			if action == "deleted" {
				if err = m.Reset(); err != nil {
					t.Fatal(err)
				}
				if err = m.Init(a, false); !errors.Is(err, ErrFormatChanged) {
					t.Fatalf("deleted volume recreated: %v", err)
				}
				return
			}
			if err = m.Init(a, false); err != nil {
				t.Fatal(err)
			}
			if action == "repeat" {
				a.TrashDays = 0
				if err = m.Init(a, false); err != nil {
					t.Fatalf("successful snapshot cannot be reused: %v", err)
				}
				a.Capacity = 3 << 30
				if m.GetFormat().Capacity != 2<<30 {
					t.Fatal("caller mutation changed published format")
				}
				return
			}
			before, err := m.getBase().en.doLoad()
			if err != nil {
				t.Fatal(err)
			}
			err = m.Init(b, action == "force")
			if action == "force" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrFormatChanged) {
				t.Fatalf("stale update accepted: %v", err)
			}
			after, err := m.getBase().en.doLoad()
			if err != nil || !bytes.Equal(before, after) || m.GetFormat().Capacity != 2<<30 {
				t.Fatalf("rejected update changed stored or published state: %v", err)
			}
		})
	}
}

func testFormatChangeLogErrors(t *testing.T, r *redisMeta) {
	defer func() {
		if err := r.Reset(); err != nil {
			t.Fatal(err)
		}
	}()
	for _, failure := range []string{"log-type", "counter-type", "counter-invalid", "counter-overflow", "two-log-overflow"} {
		t.Run(failure, func(t *testing.T) {
			f := testFormat()
			f.ChangeLog = true
			f.DirStats = false
			f.UserGroupQuota = false
			if err := r.Reset(); err != nil {
				t.Fatal(err)
			}
			if err := r.Init(f, false); err != nil {
				t.Fatal(err)
			}
			ctx := Background()
			if err := r.rdb.HSet(ctx, r.dirUsedSpaceKey(), "1", "4096").Err(); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "log-type":
				if err := r.rdb.Set(ctx, r.txnLogKey(), "wrong type", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "counter-type":
				if err := r.rdb.RPush(ctx, r.txnLastLog(), "wrong type").Err(); err != nil {
					t.Fatal(err)
				}
			case "counter-invalid":
				if err := r.rdb.Set(ctx, r.txnLastLog(), "not-an-integer", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "counter-overflow":
				if err := r.rdb.Set(ctx, r.txnLastLog(), "9223372036854775807", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "two-log-overflow":
				if err := r.rdb.Set(ctx, r.txnLastLog(), "9223372036854775806", 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			next, err := r.Load(false)
			if err != nil {
				t.Fatal(err)
			}
			next.DirStats = true
			if failure == "two-log-overflow" {
				next.UserGroupQuota = true
			}
			before, err := r.doLoad()
			if err != nil {
				t.Fatal(err)
			}
			logBefore, err := r.rdb.Dump(ctx, r.txnLogKey()).Bytes()
			if err != nil && err != redis.Nil {
				t.Fatal(err)
			}
			counterBefore, err := r.rdb.Dump(ctx, r.txnLastLog()).Bytes()
			if err != nil && err != redis.Nil {
				t.Fatal(err)
			}
			err = r.Init(next, false)
			if err == nil {
				t.Fatal("invalid change-log inputs accepted")
			}
			after, loadErr := r.doLoad()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if !bytes.Equal(before, after) {
				t.Error("failed update changed stored format")
			}
			if space, getErr := r.rdb.HGet(ctx, r.dirUsedSpaceKey(), "1").Result(); getErr != nil || space != "4096" {
				t.Errorf("failed update cleaned dir stats: %q %v", space, getErr)
			}
			logAfter, _ := r.rdb.Dump(ctx, r.txnLogKey()).Bytes()
			counterAfter, _ := r.rdb.Dump(ctx, r.txnLastLog()).Bytes()
			if !bytes.Equal(logBefore, logAfter) || !bytes.Equal(counterBefore, counterAfter) {
				t.Error("failed update changed log/version")
			}
		})
	}
}
