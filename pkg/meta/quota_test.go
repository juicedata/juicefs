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
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestQuotaEnableFormat(t *testing.T) {
	expected := &Format{Name: "test", UUID: "volume-id"}
	body := []byte(`{"Name":"test","UUID":"volume-id","Bucket":"current","future_setting":{"value":123}}`)
	for _, qtype := range []uint32{DirQuotaType, UserQuotaType, GroupQuotaType} {
		format, data, err := quotaEnableFormat(body, expected, qtype)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		if string(document["future_setting"]) != `{"value":123}` || format.Bucket != "current" {
			t.Fatalf("unrelated settings changed: %s", data)
		}
		if qtype == DirQuotaType && !format.DirStats || qtype != DirQuotaType && !format.UserGroupQuota {
			t.Fatalf("requested feature was not enabled: %s", format)
		}
		if _, again, err := quotaEnableFormat(data, expected, qtype); err != nil || again != nil {
			t.Fatalf("repeated enable should not rewrite format: %s, %v", again, err)
		}
		alias := "usergroupquota"
		if qtype == DirQuotaType {
			alias = "dirstats"
		}
		mixedCase := []byte(fmt.Sprintf(`{"Name":"test","UUID":"volume-id",%q:false}`, alias))
		_, data, err = quotaEnableFormat(mixedCase, expected, qtype)
		var loaded Format
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &loaded); err != nil {
			t.Fatal(err)
		}
		if qtype == DirQuotaType && !loaded.DirStats || qtype != DirQuotaType && !loaded.UserGroupQuota {
			t.Fatalf("existing case-insensitive setting overrode feature: %s", data)
		}
	}
	for _, invalid := range [][]byte{nil, []byte(`null`), []byte(`{`), []byte(`{"Name":"other","UUID":"volume-id"}`), []byte(`{"Name":"test","UUID":"other-volume"}`)} {
		if _, data, err := quotaEnableFormat(invalid, expected, DirQuotaType); err == nil || data != nil {
			t.Fatalf("invalid or replaced volume accepted: %q, %s, %v", invalid, data, err)
		}
	}
	if _, _, err := quotaEnableFormat(body, expected, AllQuotaType); err == nil {
		t.Fatal("invalid feature type accepted")
	}
	future := []byte(fmt.Sprintf(`{"Name":"test","UUID":"volume-id","MetaVersion":%d}`, MaxVersion+1))
	if _, data, err := quotaEnableFormat(future, expected, DirQuotaType); err == nil || data != nil {
		t.Fatal("unsupported metadata version accepted")
	}
}

func testQuotaEnableChangeLogErrors(t *testing.T, r *redisMeta) {
	ctx := Background()
	for _, qtype := range []uint32{DirQuotaType, UserQuotaType, GroupQuotaType} {
		t.Run(fmt.Sprint(qtype), func(t *testing.T) {
			for _, failure := range []string{"log-type", "counter-type", "counter-invalid", "counter-overflow"} {
				t.Run(failure, func(t *testing.T) {
					if err := r.Reset(); err != nil {
						t.Fatal(err)
					}
					f := testFormat()
					f.DirStats = false
					f.UserGroupQuota = false
					f.ChangeLog = true
					if err := r.Init(f, false); err != nil {
						t.Fatal(err)
					}
					keys := []string{r.dirUsedSpaceKey(), r.dirUsedInodesKey(), r.userQuotaKey(), r.userQuotaUsedSpaceKey(), r.userQuotaUsedInodesKey(), r.groupQuotaKey(), r.groupQuotaUsedSpaceKey(), r.groupQuotaUsedInodesKey()}
					for _, key := range keys {
						if err := r.rdb.HSet(ctx, key, "1", "4096").Err(); err != nil {
							t.Fatal(err)
						}
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
					}
					keys = append(keys, r.setting(), r.txnLogKey(), r.txnLastLog())
					before := make(map[string][]byte)
					for _, key := range keys {
						value, err := r.rdb.Dump(ctx, key).Bytes()
						if err != nil && err != redis.Nil {
							t.Fatal(err)
						}
						before[key] = value
					}
					_, err := r.enableQuota(ctx, qtype)
					if err == nil {
						t.Error("invalid change-log input accepted")
					}
					for _, key := range keys {
						after, e := r.rdb.Dump(ctx, key).Bytes()
						if e != nil && e != redis.Nil {
							t.Fatal(e)
						}
						if !bytes.Equal(before[key], after) {
							t.Errorf("failed quota enable mutated key %q", key)
						}
					}
					if r.getFormat().DirStats || r.getFormat().UserGroupQuota {
						t.Error("failed enable published feature")
					}
				})
			}
		})
	}
}
