/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/goccy/go-json"
	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/utils"
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
	NextTrash         int64 `json:"nextTrash"`
	NextCleanupSlices int64 `json:"nextCleanupSlices,omitempty"` // deprecated, always 0
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
	Flags     uint8  `json:"flags,omitempty"`
	Type      string `json:"type"`
	Mode      uint16 `json:"mode"`
	Uid       uint32 `json:"uid"`
	Gid       uint32 `json:"gid"`
	Atime     int64  `json:"atime"`
	Mtime     int64  `json:"mtime"`
	Ctime     int64  `json:"ctime"`
	Atimensec uint32 `json:"atimensec,omitempty"`
	Mtimensec uint32 `json:"mtimensec,omitempty"`
	Ctimensec uint32 `json:"ctimensec,omitempty"`
	Nlink     uint32 `json:"nlink"`
	Length    uint64 `json:"length"`
	Rdev      uint32 `json:"rdev,omitempty"`
	full      bool
}

type DumpedSlice struct {
	Chunkid uint64 `json:"chunkid,omitempty"`
	Id      uint64 `json:"id"`
	Pos     uint32 `json:"pos,omitempty"`
	Size    uint32 `json:"size"`
	Off     uint32 `json:"off,omitempty"`
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

type DumpedQuota struct {
	MaxSpace   int64 `json:"maxSpace"`
	MaxInodes  int64 `json:"maxInodes"`
	UsedSpace  int64 `json:"-"`
	UsedInodes int64 `json:"-"`
}

type DumpedACLEntry struct {
	Id   uint32 `json:"id"`
	Perm uint16 `json:"perm"`
}

type DumpedACL struct {
	Owner  uint16           `json:"owner"`
	Group  uint16           `json:"group"`
	Other  uint16           `json:"other"`
	Mask   uint16           `json:"mask"`
	Users  []DumpedACLEntry `json:"users"`
	Groups []DumpedACLEntry `json:"groups"`
}

type DumpedEntry struct {
	Name       string                  `json:"-"`
	Parents    []Ino                   `json:"-"`
	Attr       *DumpedAttr             `json:"attr,omitempty"`
	Symlink    string                  `json:"symlink,omitempty"`
	Xattrs     []*DumpedXattr          `json:"xattrs,omitempty"`
	Chunks     []*DumpedChunk          `json:"chunks,omitempty"`
	Entries    map[string]*DumpedEntry `json:"entries,omitempty"`
	AccessACL  *DumpedACL              `json:"posix_acl_access,omitempty"`
	DefaultACL *DumpedACL              `json:"posix_acl_default,omitempty"`
}

type wrapEntryPool struct {
	sync.Pool
}

func (p *wrapEntryPool) Get() *DumpedEntry {
	return p.Pool.Get().(*DumpedEntry)
}

func (p *wrapEntryPool) Put(de *DumpedEntry) {
	if de == nil {
		return
	}

	de.Name = ""
	de.Xattrs = nil
	de.Chunks = nil
	de.Symlink = ""
	de.AccessACL = nil
	de.DefaultACL = nil
	de.Entries = nil
	p.Pool.Put(de)
}

var entryPool = wrapEntryPool{
	Pool: sync.Pool{
		New: func() interface{} {
			return &DumpedEntry{
				Attr: &DumpedAttr{},
			}
		},
	},
}

var CHARS = []byte("0123456789ABCDEF")

func escape(original string) string {
	// similar to url.Escape but backward compatible if no '%' in it
	var escValue = make([]byte, 0, len(original))
	for i, r := range original {
		if r == utf8.RuneError || r < 32 || r == '%' || r == '"' || r == '\\' {
			if escValue == nil {
				escValue = make([]byte, i, len(original)*2)
				for j := 0; j < i; j++ {
					escValue[j] = original[j]
				}
			}
			c := byte(r)
			if r == utf8.RuneError {
				c = original[i]
			}
			escValue = append(escValue, '%')
			escValue = append(escValue, CHARS[(c>>4)&0xF])
			escValue = append(escValue, CHARS[c&0xF])
		} else if escValue != nil {
			n := utf8.RuneLen(r)
			escValue = append(escValue, original[i:i+n]...)
		}
	}
	if escValue == nil {
		return original
	}
	return string(escValue)
}

func parseHex(c byte) (byte, error) {
	if c >= '0' && c <= '9' {
		return c - '0', nil
	} else if c >= 'A' && c <= 'F' {
		return 10 + (c - 'A'), nil
	} else {
		return 0, fmt.Errorf("hex expected: %c", c)
	}
}

func unescape(s string) []byte {
	if !strings.ContainsRune(s, '%') {
		return []byte(s)
	}

	p := []byte(s)
	n := 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '%' && i+2 < len(p) {
			h, e1 := parseHex(p[i+1])
			l, e2 := parseHex(p[i+2])
			if e1 == nil && e2 == nil {
				c = h*16 + l
				i += 2
			}
		}
		p[n] = c
		n++
	}
	return p[:n]
}

func (de *DumpedEntry) writeJSON(bw *bufio.Writer, depth int) error {
	prefix := strings.Repeat(jsonIndent, depth)
	fieldPrefix := prefix + jsonIndent
	write := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	write(fmt.Sprintf("\n%s\"%s\": {", prefix, escape(de.Name)))
	data, err := json.Marshal(de.Attr)
	if err != nil {
		panic(err)
	}
	write(fmt.Sprintf("\n%s\"attr\": %s", fieldPrefix, data))
	if len(de.Symlink) > 0 {
		write(fmt.Sprintf(",\n%s\"symlink\": \"%s\"", fieldPrefix, escape(de.Symlink)))
	}
	if len(de.Xattrs) > 0 {
		for _, dumpedXattr := range de.Xattrs {
			dumpedXattr.Value = escape(dumpedXattr.Value)
		}
		if data, err = json.Marshal(de.Xattrs); err != nil {
			panic(err)
		}
		write(fmt.Sprintf(",\n%s\"xattrs\": %s", fieldPrefix, data))
	}
	if de.AccessACL != nil {
		if data, err = json.Marshal(de.AccessACL); err != nil {
			return err
		}
		write(fmt.Sprintf(",\n%s\"posix_acl_access\": %s", fieldPrefix, data))
	}
	if de.DefaultACL != nil {
		if data, err = json.Marshal(de.DefaultACL); err != nil {
			return err
		}
		write(fmt.Sprintf(",\n%s\"posix_acl_default\": %s", fieldPrefix, data))
	}
	if len(de.Chunks) == 1 {
		if data, err = json.Marshal(de.Chunks); err != nil {
			panic(err)
		}
		write(fmt.Sprintf(",\n%s\"chunks\": %s", fieldPrefix, data))
	} else if len(de.Chunks) > 1 {
		chunkPrefix := fieldPrefix + jsonIndent
		write(fmt.Sprintf(",\n%s\"chunks\": [", fieldPrefix))
		for i, c := range de.Chunks {
			if data, err = json.Marshal(c); err != nil {
				panic(err)
			}
			write(fmt.Sprintf("\n%s%s", chunkPrefix, data))
			if i != len(de.Chunks)-1 {
				write(",")
			}
		}
		write(fmt.Sprintf("\n%s]", fieldPrefix))
	}
	write(fmt.Sprintf("\n%s}", prefix))
	return nil
}

func (de *DumpedEntry) writeJsonWithOutEntry(bw *bufio.Writer, depth int) error {
	prefix := strings.Repeat(jsonIndent, depth)
	fieldPrefix := prefix + jsonIndent
	write := func(s string) {
		if _, err := bw.WriteString(s); err != nil {
			panic(err)
		}
	}
	write(fmt.Sprintf("\n%s\"%s\": {", prefix, escape(de.Name)))
	data, err := json.Marshal(de.Attr)
	if err != nil {
		panic(err)
	}
	write(fmt.Sprintf("\n%s\"attr\": %s", fieldPrefix, data))
	if len(de.Xattrs) > 0 {
		for _, dumpedXattr := range de.Xattrs {
			dumpedXattr.Value = escape(dumpedXattr.Value)
		}
		if data, err = json.Marshal(de.Xattrs); err != nil {
			panic(err)
		}
		write(fmt.Sprintf(",\n%s\"xattrs\": %s", fieldPrefix, data))
	}
	if de.AccessACL != nil {
		if data, err = json.Marshal(de.AccessACL); err != nil {
			return err
		}
		write(fmt.Sprintf(",\n%s\"posix_acl_access\": %s", fieldPrefix, data))
	}
	if de.DefaultACL != nil {
		if data, err = json.Marshal(de.DefaultACL); err != nil {
			return err
		}
		write(fmt.Sprintf(",\n%s\"posix_acl_default\": %s", fieldPrefix, data))
	}
	write(fmt.Sprintf(",\n%s\"entries\": {", fieldPrefix))
	return nil
}

type DumpedMeta struct {
	Setting   Format
	Counters  *DumpedCounters
	Sustained []*DumpedSustained
	DelFiles  []*DumpedDelFile
	Quotas    map[Ino]*DumpedQuota `json:",omitempty"`
	FSTree    *DumpedEntry         `json:",omitempty"`
	Trash     *DumpedEntry         `json:",omitempty"`
}

func (dm *DumpedMeta) validate() error {
	if dm.Counters == nil {
		return errors.New("invalid dumped meta: missing 'Counters'")
	}
	return nil
}

func (dm *DumpedMeta) writeJsonWithOutTree(w io.Writer) (*bufio.Writer, error) {
	if dm.FSTree != nil || dm.Trash != nil {
		return nil, fmt.Errorf("invalid dumped meta")
	}
	data, err := json.MarshalIndent(dm, "", jsonIndent)
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(w, jsonWriteSize)
	if _, err = bw.Write(append(data[:len(data)-2], ',')); err != nil { // delete \n}
		return nil, err
	}
	return bw, nil
}

func (m *baseMeta) loadDumpedQuotas(ctx Context, quotas map[Ino]*DumpedQuota) {
	// update quota
	for inode, q := range quotas {
		if _, err := m.en.doSetQuota(ctx, inode, &Quota{q.MaxSpace, q.MaxInodes, q.UsedSpace, q.UsedInodes, 0, 0}); err != nil {
			logger.Warnf("reset quota of %d: %s", inode, err)
			continue
		}
	}
}

func dumpAttr(a *Attr, d *DumpedAttr) {
	if a.Typ > 0 {
		d.Type = typeToString(a.Typ)
	}
	d.Flags = a.Flags
	d.Mode = a.Mode
	d.Uid = a.Uid
	d.Gid = a.Gid
	d.Atime = a.Atime
	d.Mtime = a.Mtime
	d.Ctime = a.Ctime
	d.Atimensec = a.Atimensec
	d.Mtimensec = a.Mtimensec
	d.Ctimensec = a.Ctimensec
	d.Nlink = a.Nlink
	d.Rdev = a.Rdev
	if a.Typ == TypeFile {
		d.Length = a.Length
	} else {
		d.Length = 0
	}
	d.full = a.Full
}

func loadAttr(d *DumpedAttr) *Attr {
	return &Attr{
		Flags:     d.Flags,
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

type chunkKey struct {
	id   uint64
	size uint32
}

func loadEntries(r io.Reader, load func(*DumpedEntry), addChunk func(*chunkKey)) (dm *DumpedMeta,
	counters *DumpedCounters, parents map[Ino][]Ino, refs map[chunkKey]int64, err error) {
	logger.Infoln("Loading from file ...")
	dec := json.NewDecoder(r)
	if _, err = dec.Token(); err != nil {
		return
	}

	progress := utils.NewProgress(false)
	bar := progress.AddCountBar("Loaded entries", 1) // with root
	dm = &DumpedMeta{}
	counters = &DumpedCounters{ // rebuild counters
		NextInode: 2,
		NextChunk: 1,
	}
	parents = make(map[Ino][]Ino)
	refs = make(map[chunkKey]int64)
	var name json.Token
	for dec.More() {
		name, err = dec.Token()
		if err != nil {
			err = fmt.Errorf("parse name: %s", err)
			return
		}
		switch name {
		case "Setting":
			if err = dec.Decode(&dm.Setting); err == nil {
				_, err = json.MarshalIndent(dm.Setting, "", "")
			}
		case "Counters":
			if err = dec.Decode(&dm.Counters); err == nil {
				bar.SetTotal(dm.Counters.UsedInodes) // TODO
			}
		case "Sustained":
			err = dec.Decode(&dm.Sustained)
		case "DelFiles":
			err = dec.Decode(&dm.DelFiles)
		case "Quotas":
			err = dec.Decode(&dm.Quotas)
		case "FSTree":
			_, err = decodeEntry(dec, 0, counters, parents, dm.Quotas, refs, bar, load, addChunk)
		case "Trash":
			_, err = decodeEntry(dec, 1, counters, parents, nil, refs, bar, load, addChunk)
		}
		if err != nil {
			err = fmt.Errorf("load %v: %s", name, err)
			return
		}
	}
	_, _ = dec.Token() // }
	progress.Done()

	if err = dm.validate(); err != nil {
		return
	}

	logger.Infof("Dumped counters: %+v", *dm.Counters)
	logger.Infof("Loaded counters: %+v", *counters)
	return
}

func decodeEntry(dec *json.Decoder, parent Ino, cs *DumpedCounters, parents map[Ino][]Ino, quotas map[Ino]*DumpedQuota,
	refs map[chunkKey]int64, bar *utils.Bar, load func(*DumpedEntry), addChunk func(*chunkKey)) (*DumpedEntry, error) {
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var e = DumpedEntry{}
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch name {
		case "attr":
			err = dec.Decode(&e.Attr)
			if err == nil {
				if parent == 0 {
					parent = 1
					e.Attr.Inode = 1 // fix loading from subdir
				}
				inode := e.Attr.Inode
				if typeFromString(e.Attr.Type) == TypeDirectory {
					e.Attr.Nlink = 2
				} else {
					e.Attr.Nlink = 1
				}
				e.Parents = append(parents[inode], parent)
				parents[inode] = e.Parents
				if len(e.Parents) == 1 {
					if inode > 1 && inode != TrashInode {
						cs.UsedSpace += align4K(e.Attr.Length)
						cs.UsedInodes += 1
					}
					if inode < TrashInode {
						if cs.NextInode <= int64(inode) {
							cs.NextInode = int64(inode) + 1
						}
					} else {
						if cs.NextTrash < int64(inode-TrashInode) {
							cs.NextTrash = int64(inode - TrashInode)
						}
					}
				}
			}
		case "chunks":
			err = dec.Decode(&e.Chunks)
			if err == nil && len(e.Parents) == 1 {
				for _, c := range e.Chunks {
					for _, s := range c.Slices {
						if s.Chunkid != 0 && s.Id == 0 {
							s.Id = s.Chunkid
							s.Chunkid = 0
						}
						ck := chunkKey{s.Id, s.Size}
						refs[ck]++
						if addChunk != nil && refs[ck] == 1 {
							addChunk(&ck)
						}
						if cs.NextChunk <= int64(s.Id) {
							cs.NextChunk = int64(s.Id) + 1
						}
					}
				}
			}
		case "entries":
			e.Entries = make(map[string]*DumpedEntry)
			_, err = dec.Token()
			var usedSpace, usedInodes int64
			if err == nil {
				for dec.More() {
					var n json.Token
					n, err = dec.Token()
					if err != nil {
						break
					}
					var child *DumpedEntry
					child, err = decodeEntry(dec, e.Attr.Inode, cs, parents, quotas, refs, bar, load, addChunk)
					if err != nil {
						break
					}
					if e.Attr.Inode < TrashInode && typeFromString(child.Attr.Type) == TypeDirectory {
						e.Attr.Nlink++
					}
					e.Entries[n.(string)] = &DumpedEntry{
						Attr: &DumpedAttr{
							Inode:  child.Attr.Inode,
							Type:   child.Attr.Type,
							Length: child.Attr.Length,
						},
					}
					usedSpace += align4K(child.Attr.Length)
					usedInodes++
				}
				if err == nil {
					i := e.Attr.Inode
					for {
						if q := quotas[i]; q != nil {
							q.UsedSpace += usedSpace
							q.UsedInodes += usedInodes
						}
						if i <= 1 || len(parents[i]) == 0 {
							break
						}
						i = parents[i][0]
					}

					var t json.Token
					t, err = dec.Token()
					if err == nil && t != json.Delim('}') {
						err = fmt.Errorf("unexpected %v", t)
					}
				}
			}
		case "symlink":
			err = dec.Decode(&e.Symlink)
		case "xattrs":
			err = dec.Decode(&e.Xattrs)
		case "posix_acl_access":
			err = dec.Decode(&e.AccessACL)
		case "posix_acl_default":
			err = dec.Decode(&e.DefaultACL)
		}
		if err != nil {
			return nil, fmt.Errorf("decode %v: %s", name, err)
		}
	}
	if len(e.Parents) == 1 {
		load(&e)
		bar.Increment()
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return &e, nil
}

func dumpACL(rule *aclAPI.Rule) *DumpedACL {
	if rule == nil {
		return nil
	}
	return &DumpedACL{
		Owner:  rule.Owner,
		Group:  rule.Group,
		Other:  rule.Other,
		Mask:   rule.Mask,
		Users:  dumpACLEntries(rule.NamedUsers),
		Groups: dumpACLEntries(rule.NamedGroups),
	}
}

func dumpACLEntries(entries aclAPI.Entries) []DumpedACLEntry {
	if len(entries) == 0 {
		return nil
	}
	dumpedEnts := make([]DumpedACLEntry, len(entries))
	for i, ent := range entries {
		dumpedEnts[i].Id = ent.Id
		dumpedEnts[i].Perm = ent.Perm
	}
	return dumpedEnts
}

func loadACL(dumped *DumpedACL) *aclAPI.Rule {
	if dumped == nil {
		return nil
	}
	return &aclAPI.Rule{
		Owner:       dumped.Owner,
		Group:       dumped.Group,
		Mask:        dumped.Mask,
		Other:       dumped.Other,
		NamedUsers:  loadACLEntries(dumped.Users),
		NamedGroups: loadACLEntries(dumped.Groups),
	}
}

func loadACLEntries(dumpedEnts []DumpedACLEntry) aclAPI.Entries {
	if len(dumpedEnts) == 0 {
		return nil
	}
	ents := make(aclAPI.Entries, len(dumpedEnts))
	for i, d := range dumpedEnts {
		ents[i].Id = d.Id
		ents[i].Perm = d.Perm
	}
	return ents
}
