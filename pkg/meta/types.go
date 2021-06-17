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

type DumpedDelFile struct {
	Inode  Ino    `json:"inode"`
	Length uint64 `json:"length"`
	Expire int64  `json:"expire"`
}

type DumpedSustained struct {
	Sid    uint64 `json:"sid"`
	Inodes []Ino  `json:"inodes"`
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
	Length    uint64 `json:"length"`
	Rdev      uint32 `json:"rdev,omitempty"`
}

type DumpedSlice struct {
	Pos     uint32 `json:"pos"`
	Chunkid uint64 `json:"chunkid"`
	Size    uint32 `json:"size"`
	Off     uint32 `json:"off"`
	Len     uint32 `json:"len"`
}

type DumpedChunk struct {
	Index  uint32         `json:"index"`
	Slices []*DumpedSlice `json:"slices"`
}

type DumpedXattr struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type DumpedEntry struct {
	Name    string                  `json:"-"`
	Inode   Ino                     `json:"inode"`
	Attr    *DumpedAttr             `json:"attr"`
	Parent  Ino                     `json:"-"`
	Symlink string                  `json:"symlink,omitempty"`
	Xattrs  []*DumpedXattr          `json:"xattrs,omitempty"`
	Chunks  []*DumpedChunk          `json:"chunks,omitempty"`
	Entries map[string]*DumpedEntry `json:"entries,omitempty"`
}

type DumpedMeta struct {
	Setting   *Format
	Counters  *DumpedCounters
	Sustained []*DumpedSustained
	DelFiles  []*DumpedDelFile
	FSTree    *DumpedEntry
}

func dumpAttr(a *Attr) *DumpedAttr {
	d := &DumpedAttr{
		Type:      typeToString(a.Typ),
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
	if a.Typ == TypeFile {
		d.Length = a.Length
	}
	return d
}

func loadAttr(d *DumpedAttr) *Attr {
	return &Attr{
		// Flags:     0,
		Typ:       typeFromString(d.Type),
		Mode:      d.Mode,
		Uid:       d.Uid,
		Gid:       d.Gid,
		Atime:     d.Atime,
		Mtime:     d.Mtime,
		Ctime:     d.Ctime,
		Atimensec: d.Atimensec,
		Mtimensec: d.Mtimensec,
		Ctimensec: d.Ctimensec,
		Nlink:     d.Nlink,
		Rdev:      d.Rdev,
		Full:      true,
	} // Length and Parent not set
}
