/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"testing"
	"time"
)

func TestRelatimeNeedUpdate(t *testing.T) {
	attr := &Attr{
		Atime: 1000,
	}
	if !relatimeNeedUpdate(attr, time.Now()) {
		t.Fatal("atime not updated for 24 hours")
	}

	now := time.Now()
	attr.Atime = now.Unix()
	attr.Ctime = now.Unix() + 10
	if !relatimeNeedUpdate(attr, time.Now()) {
		t.Fatal("atime not updated for ctime")
	}

	now = time.Now()
	attr.Atime = now.Unix()
	attr.Mtime = now.Unix() + 10
	if !relatimeNeedUpdate(attr, time.Now()) {
		t.Fatal("atime not updated for mtime")
	}

	now = time.Now()
	attr.Atime = now.Unix()
	attr.Mtime = now.Unix()
	attr.Ctime = now.Unix()
	if relatimeNeedUpdate(attr, now) {
		t.Fatal("atime should not be updated")
	}
}

func TestAtimeNeedsUpdate(t *testing.T) {
	m := &baseMeta{
		conf: &Config{
			AtimeMode: NoAtime,
		},
	}
	attr := &Attr{
		Atime: 1000,
	}
	now := time.Now()
	if m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime updated for noatime")
	}

	m.conf.AtimeMode = RelAtime
	if !m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime not updated for relatime")
	}
	attr.Atime = now.Unix()
	if m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime updated for relatime")
	}

	m.conf.AtimeMode = StrictAtime
	attr.Atime = now.Unix() - 2
	if !m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime not updated for strictatime")
	}

	attr.Atime = now.Unix() - 1
	attr.Atimensec = uint32(now.Nanosecond())
	if m.atimeNeedsUpdate(attr, now) {
		t.Fatal("atime updated for strictatime when < 1s")
	}
}
