/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
	v, blob := createTestVFS()
	go Backup(v.Meta, blob, time.Millisecond*100)
	time.Sleep(time.Millisecond * 100)

	blob = object.WithPrefix(blob, "meta/")
	kc, _ := osync.ListAll(blob, "", "")
	var keys []string
	for obj := range kc {
		keys = append(keys, obj.Key())
	}
	if len(keys) < 1 {
		t.Fatalf("there should be at least 1 backup file")
	}
}
