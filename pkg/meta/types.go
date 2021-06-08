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

package meta

type DumpedCounters struct {
	UsedSpace         int64 `json:"usedSpace"`
	UsedInodes        int64 `json:"usedInodes"`
	NextInode         int64 `json:"nextInodes"`
	NextChunk         int64 `json:"nextChunk"`
	NextSession       int64 `json:"nextSession"`
	NextCleanupSlices int64 `json:"nextCleanupSlices"`
}

type DumpedDelFiles struct {
	Inode  Ino
	Length uint64
	Second int
}

type DumpedSustained struct {
	Session int64
	Inodes  []Ino
}

type DumpedAttr struct {
	Type      string `json:"type"`
	Mode      uint16 `json:"mode"`
	Uid       uint32 `json:"uid"`
	Gid       uint32 `json:"gid"`
	Atime     int64  `json:"atime"`
	Mtime     int64  `json:"mtime"`
	Ctime     int64  `json:"ctime"`
	Atimensec uint32 `json:"atimensec"`
	Mtimensec uint32 `json:"mtimensec"`
	Ctimensec uint32 `json:"ctimensec"`
	Nlink     uint32 `json:"nlink"`
	Length    uint64 `json:"length,omitempty"`
	Rdev      uint32 `json:"rdev,omitempty"`
}

type DumpedEntry struct {
	Name    string         `json:"name"`
	Inode   Ino            `json:"inode"`
	Attr    *DumpedAttr    `json:"attr"`
	Entries []*DumpedEntry `json:"entries,omitempty"`
	Chunks  []Slice        `json:"chunks,omitempty"`
	Xattrs  []xattr        `json:"xattrs,omitempty"`
	Symlink string         `json:"symlink,omitempty"`
}

type DumpedMeta struct {
	Setting   *Format
	Counters  *DumpedCounters
	DelFiles  []*DumpedDelFiles
	Sustained []*DumpedSustained
	FSTree    *DumpedEntry
}

func dumpAttr(a *Attr) *DumpedAttr {
	d := &DumpedAttr{
		Mode:      a.Mode,
		Uid:       a.Uid,
		Gid:       a.Gid,
		Atime:     a.Atime,
		Mtime:     a.Mtime,
		Ctime:     a.Ctime,
		Atimensec: a.Atimensec,
		Mtimensec: a.Mtimensec,
		Ctimensec: a.Ctimensec,
		Nlink:     a.Nlink,
		Rdev:      a.Rdev,
	}
	d.Type = typeToString(a.Typ)
	if a.Typ == TypeFile {
		d.Length = a.Length
	}
	return d
}
