/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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

func TestClock(t *testing.T) {
	now := Now()
	if time.Since(now).Microseconds() > 1000 {
		t.Fatal("time is not accurate")
	}
	c1 := Clock()
	c2 := Clock()
	if c2-c1 > time.Microsecond {
		t.Fatalf("clock is not accurate: %s", c2-c1)
	}
}
