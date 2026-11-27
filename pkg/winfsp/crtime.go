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

package winfsp

// Windows creation time (birth time) support.
//
// JuiceFS metadata has no birth-time field. The Windows mount already reports
// attr.Atime as the file's CreationTime, so this file makes atime behave like
// a creation time on Windows:
//
//   - Utimens never changes atime (Windows SetBasicInfo would otherwise copy
//     the source file's LastAccessTime into it on every copy/overwrite).
//   - Setcrtime (SetFileTime / SetBasicInfo CreationTime) writes atime.
//   - NTFS-style name tunneling: when a name is deleted or renamed away and
//     the same name is created again within tunnelTTL (the "write temp file,
//     delete/rename original, rename temp to original" save pattern used by
//     Office, MATLAB, editors...), the new file inherits the old creation time.
//
// This relies on atime not being updated by reads, i.e. every client of the
// volume must use --atime-mode=noatime (the default).

import (
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/winfsp/cgofuse/fuse"
)

// Same default as NTFS (MaximumTunnelEntryAgeInSeconds = 15).
const tunnelTTL = 15 * time.Second
const tunnelMaxEntries = 4096

type tunnelEntry struct {
	sec, nsec int64
	expire    time.Time
}

type nameTunnel struct {
	sync.Mutex
	entries map[string]tunnelEntry
}

func tunnelKey(p string) string { return strings.ToLower(p) }

func (t *nameTunnel) put(p string, sec, nsec int64) {
	now := time.Now()
	t.Lock()
	defer t.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]tunnelEntry)
	}
	if len(t.entries) >= tunnelMaxEntries {
		for k, v := range t.entries {
			if now.After(v.expire) {
				delete(t.entries, k)
			}
		}
		if len(t.entries) >= tunnelMaxEntries {
			return
		}
	}
	t.entries[tunnelKey(p)] = tunnelEntry{sec, nsec, now.Add(tunnelTTL)}
}

// take returns and removes a live entry for p.
func (t *nameTunnel) take(p string) (sec, nsec int64, ok bool) {
	t.Lock()
	defer t.Unlock()
	k := tunnelKey(p)
	e, found := t.entries[k]
	if !found {
		return
	}
	delete(t.entries, k)
	if time.Now().After(e.expire) {
		return
	}
	return e.sec, e.nsec, true
}

// getCrtime returns the creation time (stored in atime) of p.
func (j *juice) getCrtime(ctx vfs.LogContext, p string) (sec, nsec int64, ok bool) {
	fi, err := j.fs.Lstat(ctx, p)
	if err != 0 {
		return
	}
	attr := fi.Attr()
	return attr.Atime, int64(attr.Atimensec), true
}

// setCrtime stores the creation time of p into atime, leaving mtime untouched.
func (j *juice) setCrtime(ctx vfs.LogContext, p string, sec, nsec int64) (e int) {
	f, err := j.fs.Open(ctx, p, 0)
	if err != 0 {
		return errorconv(err)
	}
	e = errorconv(f.Utime2(ctx, sec, nsec, -1, -1))
	if e == 0 {
		j.invalidateAttrCache(f.Inode())
	}
	return
}

// restoreName applies a tunneled creation time to a newly created name.
func (j *juice) restoreName(ctx vfs.LogContext, p string) {
	if sec, nsec, ok := j.tunnel.take(p); ok {
		if e := j.setCrtime(ctx, p, sec, nsec); e != 0 {
			logger.Debugf("restore creation time of %s: %d", p, e)
		}
	}
}

// Setcrtime changes the file creation (birth) time.
func (j *juice) Setcrtime(p string, tmsp fuse.Timespec) (e int) {
	ctx := j.newContext()
	defer func() { j.log(ctx, "Setcrtime (%s, %v): %d", p, tmsp, e) }()
	e = j.setCrtime(ctx, p, tmsp.Sec, tmsp.Nsec)
	return
}
