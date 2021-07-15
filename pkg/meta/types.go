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

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	jsonIndent    = "  "
	jsonWriteSize = 64 << 10
)

type DumpedCounters struct {
	UsedSpace         int64 `json:"usedSpace"`
	UsedInodes        int64 `json:"usedInodes"`
	NextInode         int64 `json:"nextInodes"`
	NextChunk         int64 `json:"nextChunk"`
	NextSession       int64 `json:"nextSession"`
	NextCleanupSlices int64 `json:"nextCleanupSlices"` // deprecated, always 0
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
	Inode     Ino    `json:"inode"`
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
	Parent  Ino                     `json:"-"`
	Attr    *DumpedAttr             `json:"attr"`
	Symlink string                  `json:"symlink,omitempty"`
	Xattrs  []*DumpedXattr          `json:"xattrs,omitempty"`
	Chunks  []*DumpedChunk          `json:"chunks,omitempty"`
	Entries map[string]*DumpedEntry `json:"entries,omitempty"`
}

func (de *DumpedEntry) writeJSON(bw *bufio.Writer, depth int) error {
	prefix := strings.Repeat(jsonIndent, depth)
	fieldPrefix := prefix + jsonIndent
	write := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	write(fmt.Sprintf("\n%s\"%s\": {", prefix, de.Name))
	data, err := json.Marshal(de.Attr)
	if err != nil {
		return err
	}
	write(fmt.Sprintf("\n%s\"attr\": %s", fieldPrefix, data))
	if len(de.Symlink) > 0 {
		write(fmt.Sprintf(",\n%s\"symlink\": \"%s\"", fieldPrefix, de.Symlink))
	}
	if len(de.Xattrs) > 0 {
		if data, err = json.Marshal(de.Xattrs); err != nil {
			return err
		}
		write(fmt.Sprintf(",\n%s\"xattrs\": %s", fieldPrefix, data))
	}
	if len(de.Chunks) == 1 {
		if data, err = json.Marshal(de.Chunks); err != nil {
			return err
		}
		write(fmt.Sprintf(",\n%s\"chunks\": %s", fieldPrefix, data))
	} else if len(de.Chunks) > 1 {
		chunkPrefix := fieldPrefix + jsonIndent
		write(fmt.Sprintf(",\n%s\"chunks\": [", fieldPrefix))
		for i, c := range de.Chunks {
			if data, err = json.Marshal(c); err != nil {
				return err
			}
			write(fmt.Sprintf("\n%s%s", chunkPrefix, data))
			if i != len(de.Chunks)-1 {
				write(",")
			}
		}
		write(fmt.Sprintf("\n%s]", fieldPrefix))
	}
	if len(de.Entries) > 0 {
		entries := make([]*DumpedEntry, 0, len(de.Entries))
		for k, v := range de.Entries {
			v.Name = k
			entries = append(entries, v)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		write(fmt.Sprintf(",\n%s\"entries\": {", fieldPrefix))
		for i, e := range entries {
			if err = e.writeJSON(bw, depth+2); err != nil {
				return err
			}
			if i != len(entries)-1 {
				write(",")
			}
		}
		write(fmt.Sprintf("\n%s}", fieldPrefix))
	}
	write(fmt.Sprintf("\n%s}", prefix))
	return nil
}

type DumpedMeta struct {
	Setting   *Format
	Counters  *DumpedCounters
	Sustained []*DumpedSustained
	DelFiles  []*DumpedDelFile
	FSTree    *DumpedEntry `json:",omitempty"`
}

func (dm *DumpedMeta) writeJSON(w io.Writer) error {
	tree := dm.FSTree
	dm.FSTree = nil
	data, err := json.MarshalIndent(dm, "", jsonIndent)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, jsonWriteSize)
	if _, err = bw.Write(append(data[:len(data)-2], ',')); err != nil { // delete \n}
		return err
	}
	tree.Name = "FSTree"
	if err = tree.writeJSON(bw, 1); err != nil {
		return err
	}
	if _, err = bw.WriteString("\n}\n"); err != nil {
		return err
	}
	return bw.Flush()
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
