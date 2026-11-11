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
	"encoding/json"
	"fmt"
	"testing"
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
