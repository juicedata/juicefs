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

package object

import (
	"bytes"
	"io/ioutil"
	"testing"
)

func TestRedisStore(t *testing.T) {
	s, err := newRedis("redis://127.0.0.1:6379/10", "", "")
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	if err := s.Put("chunks/1", bytes.NewBuffer([]byte("data"))); err != nil {
		t.Fatalf("put: %s", err)
	}
	if rb, err := s.Get("chunks/1", 0, -1); err != nil {
		t.Fatalf("get : %s", err)
	} else if d, err := ioutil.ReadAll(rb); err != nil || !bytes.Equal(d, []byte("data")) {
		t.Fatalf("get: %s %s", err, string(d))
	}
	if err := s.Delete("chunks/1"); err != nil {
		t.Fatalf("delete: %s", err)
	}
}
