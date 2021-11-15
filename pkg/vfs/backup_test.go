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
	"sort"
	"testing"
	"time"
)

func TestFindDeletes(t *testing.T) {
	format := func(ts time.Time) string {
		return "dump-" + ts.Format("2006-01-02-150405") + ".json.gz"
	}

	now := time.Now().UTC()
	objMap := make(map[string]bool)
	for i := 0; i < 200; i++ { // one backup for every half day
		objMap[format(now.Add(time.Duration(-i*12)*time.Hour))] = true
	}
	objs := make([]string, 0, 200)
	for key := range objMap {
		objs = append(objs, key)
	}
	toDel := findDeletes(objs)
	for _, key := range toDel {
		delete(objMap, key)
	}
	left := make([]string, 0, 25)
	for key := range objMap {
		left = append(left, key)
	}

	expect := make([]string, 0, 25)
	for i := 0; i < 4; i++ {
		expect = append(expect, format(now.Add(time.Duration(-i*12)*time.Hour)))
	}
	var days int
	for days = 2; days < 14; days++ {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for ; days < 60; days += 7 {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for ; days < 100; days += 30 {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}

	if len(left) != len(expect) {
		t.Fatalf("length of left %d != length of expect %d", len(left), len(expect))
	}
	sort.Strings(left)
	sort.Strings(expect)
	for i, s := range left {
		if s != expect[i] {
			t.Fatalf("left %s != expect %s", s, expect[i])
		}
	}
}
