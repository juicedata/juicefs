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

package utils

import (
	"testing"
	"time"
)

func TestRUsage(t *testing.T) {
	u := GetRusage()
	if u.GetUtime() < 0.0001 {
		t.Fatalf("invalid utime: %f", u.GetUtime())
	}
	var s string
	for i := 0; i < 1000; i++ {
		s += time.Now().String()
	}
	// don't optimize the loop
	if len(s) < 10 {
		panic("unreachable")
	}
	u2 := GetRusage()
	if u2.GetUtime()-u.GetUtime() < 0.0001 {
		t.Fatalf("invalid utime: %f", u2.GetStime()-u.GetStime())
	}
}
