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
	"hash/crc32"
	"strconv"
	"testing"
)

func TestChecksum(t *testing.T) {
	b := []byte("hello")
	expected := crc32.Update(0, crc32c, b)
	actual := generateChecksum(bytes.NewReader(b))
	if actual != strconv.Itoa(int(expected)) {
		t.Errorf("expect %d but got %s", expected, actual)
		t.FailNow()
	}

	actual = generateChecksum(bytes.NewReader(b))
	if actual != strconv.Itoa(int(expected)) {
		t.Errorf("expect %d but got %s", expected, actual)
		t.FailNow()
	}
}
