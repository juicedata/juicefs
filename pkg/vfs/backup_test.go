/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
)

func TestRotate(t *testing.T) {
	format := func(ts time.Time) string {
		return "dump-" + ts.UTC().Format("2006-01-02-150405") + ".json.gz"
	}

	now := time.Now()
	objs := make([]string, 0, 25)
	for cursor, i := now.AddDate(0, 0, -100), 0; i <= 200; i++ { // one backup for every half day
		objs = append(objs, format(cursor))
		toDel := rotate(objs, cursor)
		for _, d := range toDel {
			for j, k := range objs {
				if k == d {
					objs = append(objs[:j], objs[j+1:]...)
					break
				}
			}
		}
		cursor = cursor.Add(time.Duration(12) * time.Hour)
	}

	expect := make([]string, 0, 25)
	expect = append(expect, format(now.AddDate(0, 0, -100)))
	for days := 65; days > 14; days -= 7 {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for days := 13; days > 2; days-- {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for i := 4; i >= 0; i-- {
		expect = append(expect, format(now.Add(time.Duration(-i*12)*time.Hour)))
	}

	if len(objs) != len(expect) {
		t.Fatalf("length of objs %d != length of expect %d", len(objs), len(expect))
	}
	for i, o := range objs {
		if o != expect[i] {
			t.Fatalf("obj %s != expect %s", o, expect[i])
		}
	}
}

func TestBackup(t *testing.T) {
	v, blob := createTestVFS(nil, "")
	go Backup(v.Meta, blob, time.Millisecond*100, false)
	time.Sleep(time.Millisecond * 100)

	blob = object.WithPrefix(blob, "meta/")
	kc, _ := osync.ListAll(blob, "", "", "", true)
	var keys []string
	for obj := range kc {
		keys = append(keys, obj.Key())
	}
	if len(keys) < 1 {
		t.Fatalf("there should be at least 1 backup file")
	}
}
