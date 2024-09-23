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
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"pgregory.net/rapid"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

type tSlice struct {
	pos  uint32
	id   uint64
	clen uint32
	off  uint32
	len  uint32
}

type tQuota struct {
	size   uint64
	inodes uint64
}

type tNode struct {
	name     string
	inode    Ino
	_type    uint8
	mode     uint16
	uid      uint32
	gid      uint32
	atime    int64
	mtime    int64
	ctime    int64
	iflags   uint8
	length   uint64
	parents  []*tNode
	hardlink bool
	chunks   map[uint32][]tSlice
	children map[string]*tNode
	target   string
	xattrs   map[string][]byte
	quota    *tQuota
	flocks   map[ownerKey]byte
	plocks   map[ownerKey][]plockRecord
	accACL   *aclAPI.Rule
	defACL   *aclAPI.Rule
}

func (n *tNode) accessMode(uid uint32, gids []uint32) uint8 {
	if uid == 0 {
		return 0x7
	}
	mode := n.mode
	if uid == n.uid {
		return uint8(mode>>6) & 7
	}
	for _, gid := range gids {
		if gid == n.gid {
			return uint8(mode>>3) & 7
		}
	}
	return uint8(mode & 7)
}

func (n *tNode) access(ctx Context, mask uint8) bool {
	if ctx.Uid() == 0 {
		return true
	}

	if n.accACL != nil && (n.mode&00070) != 0 {
		return n.accACL.CanAccess(ctx.Uid(), ctx.Gids(), n.uid, n.gid, mask)
	}

	mode := n.accessMode(ctx.Uid(), ctx.Gids())
	if mode&mask != mask {
		return false
	}
	return true
}

func (n *tNode) stickyAccess(child *tNode, uid uint32) bool {
	if uid == 0 || n.mode&01000 == 0 {
		return true
	}
	if uid == n.uid || uid == child.uid {
		return true
	}
	return false
}

type fsMachine struct {
	nodes map[Ino]*tNode
	meta  Meta
	sid   uint64
	ctx   Context
}

func (m *fsMachine) Init(t *rapid.T) {
	m.sid = rapid.Uint64().Draw(t, "sid")
	m.nodes = make(map[Ino]*tNode)
	m.nodes[1] = &tNode{
		_type:    TypeDirectory,
		mode:     0777,
		inode:    RootInode,
		length:   4096,
		xattrs:   make(map[string][]byte),
		children: make(map[string]*tNode),
		parents:  []*tNode{{inode: RootInode, _type: TypeDirectory}},
	}
	_ = os.Remove(settingPath)
	m.meta, _ = newKVMeta("memkv", "jfs-unit-test", testConfig())
	if err := m.meta.Init(testFormat(), true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	m.meta.getBase().sid = m.sid
	registry := prometheus.NewRegistry() // replace default so only JuiceFS metrics are exposed
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_",
		prometheus.WrapRegistererWith(prometheus.Labels{"mp": "virtual-mp", "vol_name": "test-vol"}, registry))
	m.meta.InitMetrics(registerer)
}

func (m *fsMachine) genName(t *rapid.T) string {
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
	name = strings.ReplaceAll(name, "|", "a") // FIXME: name can't contain '|'
	name = strings.ReplaceAll(name, ".#", "aa")
	name = strings.ReplaceAll(name, "\n", "a")
	return name
}

func (m *fsMachine) Cleanup() {
	m.meta.Reset()
}

func (m *fsMachine) prepare(t *rapid.T) {
	// m.ctx.ts++
	uid := rapid.Uint32Range(0, 5).Draw(t, "uid")
	gid := rapid.Uint32Range(0, 5).Draw(t, "gid")
	m.ctx = NewContext(1, uid, []uint32{gid})
	// t.Logf("time: %d", m.ctx.ts)
}

func (m *fsMachine) pickNode(t *rapid.T) Ino {
	m.prepare(t)
	var inodes []Ino
	for inode := range m.nodes {
		inodes = append(inodes, Ino(inode))
	}
	sort.Slice(inodes, func(i, j int) bool { return inodes[i] < inodes[j] })
	return rapid.SampledFrom(inodes).Draw(t, "node")
}

func (m *fsMachine) create(_type uint8, parent Ino, name string, mode, umask uint16, inode Ino) syscall.Errno {
	if _type < TypeFile || _type == TypeSymlink {
		return syscall.EINVAL
	}
	p := m.nodes[parent]
	if p == nil {
		return syscall.ENOENT
	}
	if p.children == nil {
		return syscall.ENOTDIR
	}
	if fsnodes_namecheck(name) != 0 {
		return syscall.EINVAL
	}

	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}
	if p.children[name] != nil {
		return syscall.EEXIST
	}
	n := &tNode{
		name:    name,
		_type:   _type,
		mode:    mode &^ umask,
		inode:   inode,
		uid:     m.ctx.Uid(),
		gid:     m.ctx.Gids()[0],
		parents: []*tNode{p},
		xattrs:  make(map[string][]byte),
	}

	if runtime.GOOS == "darwin" {
		n.gid = p.gid
	} else if runtime.GOOS == "linux" && p.mode&02000 != 0 {
		n.gid = p.gid
		if _type == TypeDirectory {
			p.mode |= 02000
		} else if n.mode&02010 == 02010 && m.ctx.Uid() != 0 {
			var found bool
			for _, gid := range m.ctx.Gids() {
				if gid == p.gid {
					found = true
				}
			}
			if !found {
				n.mode &= ^uint16(02000)
			}
		}
	}

	mode &= 07777
	if p.defACL != nil && _type != TypeSymlink {
		// inherit default acl
		if _type == TypeDirectory {
			n.defACL = p.defACL
		}

		// set access acl by parent's default acl
		rule := p.defACL

		if rule.IsMinimal() {
			// simple acl as default
			n.mode = mode & (0xFE00 | rule.GetMode())
		} else {
			cRule := rule.ChildAccessACL(mode)
			n.accACL = cRule
			n.mode = (mode & 0xFE00) | cRule.GetMode()
		}
	} else {
		n.mode = mode & ^umask
	}

	switch _type {
	case TypeDirectory:
		n.children = make(map[string]*tNode)
		n.length = 4 << 10
	case TypeFile:
		n.chunks = make(map[uint32][]tSlice)
	case TypeSymlink:
		n.length = uint64(len(name))
	default:
		n.length = 0
	}

	// p.mtime = m.ctx.ts
	// p.ctime = m.ctx.ts
	m.nodes[inode] = n
	p.children[name] = n
	return 0
}

func fsnodes_namecheck(name string) syscall.Errno {
	return 0
	nleng := len(name)
	if nleng == 0 {
		return syscall.EINVAL
	}
	if nleng > MaxName {
		return syscall.ENAMETOOLONG
	}
	if name[0] == '.' {
		if nleng == 1 {
			return syscall.EINVAL
		}
		if nleng == 2 && name[1] == '.' {
			return syscall.EINVAL
		}
	}
	for i := 0; i < nleng; i++ {
		if name[i] == 0 || name[i] == '/' {
			return syscall.EINVAL
		}
	}
	return 0
}

func (m *fsMachine) link(parent Ino, name string, inode Ino) syscall.Errno {
	if name == "." || name == ".." {
		return syscall.EEXIST
	}
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if n.children != nil {
		return syscall.EPERM
	}
	p := m.nodes[parent]
	if p == nil {
		return syscall.ENOENT
	}
	if p.children == nil {
		return syscall.ENOTDIR
	}
	if fsnodes_namecheck(name) != 0 {
		return syscall.EINVAL
	}
	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}
	if p.children[name] != nil {
		return syscall.EEXIST
	}
	// n.ctime = m.ctx.ts
	// p.mtime = m.ctx.ts
	// p.ctime = m.ctx.ts
	n.parents = append(n.parents, p)
	n.hardlink = true
	p.children[name] = n
	return 0
}

func (m *fsMachine) symlink(parent Ino, name string, inode Ino, target string) syscall.Errno {
	if len(target) == 0 || len(target) > SymlinkMax {
		return syscall.EINVAL
	}
	for _, c := range target {
		if c == 0 {
			return syscall.EINVAL
		}
	}
	p := m.nodes[parent]
	if p == nil {
		return syscall.ENOENT
	}
	if p.children == nil {
		return syscall.ENOTDIR
	}
	if fsnodes_namecheck(name) != 0 {
		return syscall.EINVAL
	}
	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}
	if p.children[name] != nil {
		return syscall.EEXIST
	}
	n := &tNode{
		name:  name,
		_type: TypeSymlink,
		inode: inode,
		mode:  0777,
		uid:   m.ctx.Uid(),
		gid:   m.ctx.Gids()[0],
		// atime:   m.ctx.ts,
		// mtime:   m.ctx.ts,
		// ctime:   m.ctx.ts,
		parents: []*tNode{p},
		target:  target,
		xattrs:  make(map[string][]byte),
	}

	_type := TypeSymlink
	if runtime.GOOS == "darwin" {
		n.gid = p.gid
	} else if runtime.GOOS == "linux" && p.mode&02000 != 0 {
		n.gid = p.gid
		if _type == TypeDirectory {
			p.mode |= 02000
		} else if n.mode&02010 == 02010 && m.ctx.Uid() != 0 {
			var found bool
			for _, gid := range m.ctx.Gids() {
				if gid == p.gid {
					found = true
				}
			}
			if !found {
				n.mode &= ^uint16(02000)
			}
		}
	}

	n.length = uint64(len(target))
	// p.mtime = m.ctx.ts
	// p.ctime = m.ctx.ts
	m.nodes[inode] = n
	p.children[name] = n
	return 0
}

func (m *fsMachine) readlink(inode Ino) (string, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return "", syscall.ENOENT
	}
	if n.target == "" {
		return "", syscall.EINVAL
	}
	return n.target, 0
}

func (m *fsMachine) pickChild(parent Ino, t *rapid.T) string {
	n := m.nodes[parent]
	if len(n.children) == 0 {
		return ""
	}
	var names []string
	for name := range n.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return rapid.SampledFrom(names).Draw(t, "child")
}

func (m *fsMachine) unlink(parent Ino, name string) syscall.Errno {
	p := m.nodes[parent]
	if p == nil {
		return syscall.ENOENT
	}
	if _, ok := p.children[name]; !ok {
		return syscall.ENOENT
	}
	if fsnodes_namecheck(name) != 0 {
		return syscall.EINVAL
	}

	c := p.children[name]

	if c._type == TypeDirectory {
		return syscall.EPERM
	}

	if !p.stickyAccess(c, m.ctx.Uid()) {
		return syscall.EACCES
	}

	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}

	delete(p.children, name)
	for i, tp := range c.parents {
		if tp == p {
			c.parents = append(c.parents[:i], c.parents[i+1:]...)
			break
		}
	}
	if len(c.parents) == 0 {
		delete(m.nodes, c.inode)
	} else {
		// c.ctime = m.ctx.ts
	}
	// p.mtime = m.ctx.ts
	// p.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) rmdir(parent Ino, name string) syscall.Errno {
	p := m.nodes[parent]
	if p == nil {
		return syscall.ENOENT
	}
	if _, ok := p.children[name]; !ok {
		return syscall.ENOENT
	}
	if fsnodes_namecheck(name) != 0 {
		return syscall.EINVAL
	}

	c := p.children[name]

	if c._type != TypeDirectory {
		return syscall.ENOTDIR
	}

	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}

	if len(c.children) != 0 {
		return syscall.ENOTEMPTY
	}

	if !p.stickyAccess(c, m.ctx.Uid()) {
		return syscall.EACCES
	}

	delete(p.children, name)
	for i, tp := range c.parents {
		if tp == p {
			c.parents = append(c.parents[:i], c.parents[i+1:]...)
			break
		}
	}
	if len(c.parents) == 0 {
		delete(m.nodes, c.inode)
	} else {
		// c.ctime = m.ctx.ts
	}
	// p.mtime = m.ctx.ts
	// p.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) lookup(parent Ino, name string, checkPerm bool) (Ino, syscall.Errno) {
	p := m.nodes[parent]
	if checkPerm {
		if !p.access(m.ctx, MODE_MASK_X) {
			return 0, syscall.EACCES
		}
	}
	if _, ok := p.children[name]; !ok {
		return 0, syscall.ENOENT
	}
	if p == nil {
		return 0, syscall.ENOENT
	}
	if fsnodes_namecheck(name) != 0 {
		return 0, syscall.EINVAL
	}
	//if p.children == nil {
	//	return 0, syscall.ENOENT
	//}
	if !p.access(m.ctx, MODE_MASK_X) {
		return 0, syscall.EACCES
	}
	c := p.children[name]
	if c == nil {
		return 0, syscall.ENOENT
	}
	return c.inode, 0
}

func (m *fsMachine) getattr(inode Ino) (*tNode, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	return n, 0
}

func (m *fsMachine) doMknod(inode Ino) (*tNode, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	return n, 0
}

func (m *fsMachine) setattr(inode Ino, attr Attr) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	// FIXME: check attr
	return 0
}

func (m *fsMachine) truncate(inode Ino, length uint64) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if n._type != TypeFile {
		return syscall.EPERM
	}
	if !n.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
	for i := range n.chunks {
		if uint64(i)*ChunkSize >= length {
			delete(n.chunks, i)
		} else if uint64(i)*ChunkSize+ChunkSize > length {
			var slices []tSlice
			for _, s := range n.chunks[i] {
				if s.pos < uint32(length-uint64(i)*ChunkSize) {
					if s.pos+s.len > uint32(length-uint64(i)*ChunkSize) {
						s.len = uint32(length-uint64(i)*ChunkSize) - s.pos
					}
					slices = append(slices, tSlice{s.pos, s.id, s.clen, s.off, s.len})
				}
			}
			n.chunks[i] = slices
		}
	}
	n.length = length
	// n.mtime = m.ctx.ts
	// n.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) fallocate(inode Ino, mode uint8, offset uint64, size uint64) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if n._type != TypeFile {
		return syscall.EPERM
	}
	//if !n.access(m.ctx, MODE_MASK_W) {
	//	return syscall.EACCES
	//}
	if offset+size > n.length {
		n.length = offset + size
	}
	// n.mtime = m.ctx.ts
	// n.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) copy_file_range(srcinode Ino, srcoff uint64, dstinode Ino, dstoff uint64, size uint64, flags uint64) syscall.Errno {
	//if srcinode == dstinode && (size == 0 || srcoff <= dstoff && dstoff < srcoff+size || dstoff < srcoff && srcoff < dstoff+size) {
	//	return syscall.EINVAL // overlap
	//}
	src := m.nodes[srcinode]
	if src == nil {
		return syscall.ENOENT
	}
	if src._type != TypeFile {
		return syscall.EINVAL
	}
	dst := m.nodes[dstinode]
	if dst == nil {
		return syscall.ENOENT
	}
	if dst._type != TypeFile {
		return syscall.EINVAL
	}
	//if !src.access(m.ctx, MODE_MASK_R) {
	//	return syscall.EACCES
	//}
	//if !dst.access(m.ctx, MODE_MASK_W) {
	//	return syscall.EACCES
	//}
	updateChunk := func(off uint64, s tSlice) {
		for s.len > 0 {
			indx := uint32(off / ChunkSize)
			pos := uint32(off % ChunkSize)
			len := uint32(ChunkSize - pos)
			if len > s.len {
				len = s.len
			}
			dst.chunks[indx] = append(dst.chunks[indx], tSlice{pos, s.id, s.clen, s.off, len})
			s.off += len
			s.len -= len
			off += uint64(len)
		}
	}
	if srcoff >= src.length {
		return 0
	}
	if srcoff+size > src.length {
		size = src.length - srcoff
	}
	if dstoff+size > dst.length {
		dst.length = dstoff + size
	}
	for size > 0 {
		indx := uint32(srcoff / ChunkSize)
		pos := uint32(srcoff % ChunkSize)
		l := uint32(ChunkSize - pos)
		if srcoff < src.length && srcoff+uint64(l) > src.length {
			l = uint32(src.length - srcoff)
		}
		if uint64(l) > size {
			l = uint32(size)
		}

		updateChunk(dstoff, tSlice{0, 0, 0, 0, l})
		var cs []tSlice
		cs = append(cs, src.chunks[indx]...) // copy
		for _, s := range cs {
			if s.pos+s.len <= pos || s.pos >= pos+l {
				continue
			}
			if s.pos+s.len > pos+l {
				s.len = pos + l - s.pos
			}
			if s.pos < pos {
				diff := pos - s.pos
				s.off += diff
				s.len -= diff
				s.pos = pos
			}
			updateChunk(dstoff+uint64(s.pos-pos), s)
		}
		srcoff += uint64(l)
		dstoff += uint64(l)
		size -= uint64(l)
	}
	// dst.mtime = m.ctx.ts
	// dst.ctime = m.ctx.ts
	return 0
}

// rmr Hint: Unlike the Rmr with the meta interface.
func (m *fsMachine) rmr(parent Ino, name string, removed *uint64) syscall.Errno {
	p := m.nodes[parent]
	if p == nil {
		return syscall.ENOENT
	}
	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}
	if p.children == nil {
		return syscall.ENOENT
	}
	if fsnodes_namecheck(name) != 0 {
		return syscall.EINVAL
	}

	c := p.children[name]
	if c == nil {
		return syscall.ENOENT
	}

	if !p.stickyAccess(c, m.ctx.Uid()) {
		return syscall.EPERM
	}
	for n := range c.children {
		if eno := m.rmr(c.inode, n, removed); eno != 0 {
			return eno
		}
	}

	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}

	var st syscall.Errno
	if c._type == TypeDirectory {
		st = m.rmdir(parent, name)
	} else {
		st = m.unlink(parent, name)
	}
	if st == 0 && removed != nil {
		*removed++
	}
	return 0
}

func (m *fsMachine) isancestor(a, b *tNode) bool {
	if a == b {
		return true
	}
	for _, p := range b.parents {
		if m.isancestor(a, p) {
			return true
		}
	}
	return false
}

func (m *fsMachine) rename(srcparent Ino, srcname string, dstparent Ino, dstname string, flag uint8) syscall.Errno {
	if dstparent == srcparent && dstname == srcname {
		return 0
	}

	src := m.nodes[srcparent]
	if src == nil {
		return syscall.ENOENT
	}
	if src.children == nil {
		return syscall.ENOTDIR
	}
	if fsnodes_namecheck(srcname) != 0 {
		return syscall.EINVAL
	}
	if !src.access(m.ctx, MODE_MASK_X|MODE_MASK_W) {
		return syscall.EACCES
	}

	dst := m.nodes[dstparent]
	if dst == nil {
		return syscall.ENOENT
	}
	if dst.children == nil {
		return syscall.ENOTDIR
	}
	if fsnodes_namecheck(dstname) != 0 {
		return syscall.EINVAL
	}
	if !dst.access(m.ctx, MODE_MASK_X|MODE_MASK_W) {
		return syscall.EACCES
	}

	srcnode := src.children[srcname]
	if srcnode == nil {
		return syscall.ENOENT
	}

	if !src.stickyAccess(srcnode, m.ctx.Uid()) {
		return syscall.EACCES
	}

	// owner of a directory cannot rename subdirectories owned by other users.
	uid := m.ctx.Uid()
	if src != dst && src.mode&0o1000 != 0 && uid != 0 &&
		uid != srcnode.uid && (uid != src.uid || srcnode._type == TypeDirectory) {
		return syscall.EACCES
	}

	if c := dst.children[dstname]; c != nil {
		if c == srcnode {
			return syscall.EPERM
		}
		if len(c.children) != 0 {
			return syscall.ENOTEMPTY
		}
		if dst != src || dstname != srcname {
			if !dst.stickyAccess(c, m.ctx.Uid()) {
				return syscall.EACCES
			}
			if st := m.rmr(dst.inode, dstname, nil); st != 0 {
				return st
			}
		}
	}
	for i, tp := range srcnode.parents {
		if tp == src {
			srcnode.parents[i] = dst
			break
		}
	}
	delete(src.children, srcname)
	srcnode.name = dstname
	dst.children[dstname] = srcnode
	// srcnode.ctime = m.ctx.ts
	// src.mtime = m.ctx.ts
	// src.ctime = m.ctx.ts
	// dst.mtime = m.ctx.ts
	// dst.ctime = m.ctx.ts
	return 0
}

type tEntry struct {
	name string
	node *tNode
}

func (m *fsMachine) readdir(inode Ino) ([]*tEntry, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	if !n.access(m.ctx, MODE_MASK_R) {
		return nil, syscall.EACCES
	}
	var result []*tEntry
	result = append(result, &tEntry{
		name: ".",
		node: n,
	}, &tEntry{
		name: "..",
		node: n.parents[0],
	})

	for name, node := range n.children {
		result = append(result, &tEntry{
			name: name,
			node: node,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, 0
}

func (m *fsMachine) write(inode Ino, indx uint32, pos uint32, chunkid uint64, cleng uint32, off, len uint32) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if n._type != TypeFile {
		return syscall.EPERM
	}
	if len == 0 {
		return 0
	}
	//pos = pos % ChunkSize // fix invalid pos
	//if chunkid == 0 || cleng == 0 || len == 0 || pos+len > ChunkSize || off+len > cleng {
	//	return syscall.EINVAL
	//}
	n.chunks[indx] = append(n.chunks[indx], tSlice{pos, chunkid, cleng, off, len})
	if uint64(indx)*ChunkSize+uint64(pos+len) > n.length {
		n.length = uint64(indx)*ChunkSize + uint64(pos) + uint64(len)
	}
	return 0
}

func (m *fsMachine) read(inode Ino, indx uint32) (uint64, []tSlice, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return 0, nil, syscall.ENOENT
	}
	if n._type != TypeFile {
		return 0, nil, syscall.EPERM
	}
	// if !n.access(m.ctx, MODE_MASK_R) {
	// 	return 0, nil, "", syscall.EACCES
	// }
	var ss []*slice
	var clen = make(map[uint64]uint32)
	for _, s := range n.chunks[indx] {
		ss = append(ss, &slice{id: s.id, off: s.off, len: s.len, pos: s.pos})
		clen[s.id] = s.clen
	}
	cs := buildSlice2(ss)
	for i := range cs {
		if _, ok := clen[cs[i].id]; ok {
			cs[i].clen = clen[cs[i].id]
		}
	}
	return n.length, cs, 0
}

func buildSlice2(ss []*slice) []tSlice {
	if len(ss) == 0 {
		return nil
	}
	var root *slice
	for i := range ss {
		s := new(slice)
		*s = *ss[i]
		var right *slice
		s.left, right = root.cut(s.pos)
		_, s.right = right.cut(s.pos + s.len)
		root = s
	}
	// root.optimize(1)
	var pos uint32
	var chunk []tSlice
	root.visit(func(s *slice) {
		if s.pos > pos {
			chunk = append(chunk, tSlice{pos: pos, len: s.pos - pos, clen: s.pos - pos})
			pos = s.pos
		}
		chunk = append(chunk, tSlice{pos: pos, id: s.id, off: s.off, len: s.len})
		pos += s.len
	})
	return chunk
}

func (m *fsMachine) setxattr(inode Ino, name string, value []byte, mode uint8) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	// if !xattr.check(name) {
	// 	return syscall.EINVAL
	// }
	switch mode {
	case XattrCreate:
		if n.xattrs[name] != nil {
			return syscall.EEXIST
		}
		n.xattrs[name] = value
	case XattrReplace:
		if n.xattrs[name] == nil {
			return ENOATTR
		}
		n.xattrs[name] = value
	case XattrCreateOrReplace:
		n.xattrs[name] = value
	default:
		return syscall.EINVAL
	}
	// n.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) removexattr(inode Ino, name string) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	// if !xattr.check(name) {
	// 	return syscall.EINVAL
	// }
	if n.xattrs[name] == nil {
		return ENOATTR
	}
	// n.ctime = m.ctx.ts
	delete(n.xattrs, name)
	return 0
}

func (m *fsMachine) getxattr(inode Ino, name string) ([]byte, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	// if !xattr.check(name) {
	// 	return nil, syscall.EINVAL
	// }
	if v, ok := n.xattrs[name]; ok {
		return v, 0
	}
	return nil, ENOATTR
}

func (m *fsMachine) listxattr(inode Ino) ([]byte, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	var names []string
	for name := range n.xattrs {
		names = append(names, name+"\x00")
	}

	if n.accACL != nil {
		names = append(names, "system.posix_acl_access"+"\x00")
	}
	if n.defACL != nil {
		names = append(names, "system.posix_acl_default"+"\x00")
	}

	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	r := []byte(strings.Join(names, ""))
	if len(r) > 65536 {
		return nil, syscall.ERANGE
	}
	return r, 0
}

func (m *fsMachine) Mkdir(t *rapid.T) {
	parent := m.pickNode(t)
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
	mode := rapid.Uint16Range(0, 01777).Draw(t, "mode")
	if name == "." || name == ".." {
		t.Skipf("skip mkdir %s", name)
	}
	t.Logf("parent ino %d", parent)
	var inode Ino
	var attr Attr
	st := m.meta.Mkdir(m.ctx, parent, name, mode, 0, 0, &inode, &attr)
	t.Logf("dir ino %d", inode)
	//var attr2 Attr
	//m.meta.GetAttr(m.ctx, inode, &attr2)
	st2 := m.create(TypeDirectory, parent, name, mode, 0, inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Mknod(t *rapid.T) {
	parent := m.pickNode(t)
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
	if name == "." || name == ".." {
		t.Skipf("skip mknod %s", name)
	}
	_type := rapid.Uint8Range(0, TypeDirectory).Draw(t, "type")
	mode := rapid.Uint16Range(0, 01777).Draw(t, "mode")
	var inode Ino
	var attr Attr
	st := m.meta.Mknod(m.ctx, parent, name, _type, mode, 0, 0, "", &inode, &attr)
	st2 := m.create(_type, parent, name, mode, 0, inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Link(t *rapid.T) {
	parent := m.pickNode(t)
	name := m.genName(t)
	inode := m.pickNode(t)
	st := m.meta.Link(m.ctx, inode, parent, name, nil)
	st2 := m.link(parent, name, inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}
func (m *fsMachine) Rmdir(t *rapid.T) {
	parent := m.pickNode(t)
	name := m.pickChild(parent, t)
	st := m.meta.Rmdir(m.ctx, parent, name)
	st2 := m.rmdir(parent, name)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Unlink(t *rapid.T) {
	parent := m.pickNode(t)
	name := m.pickChild(parent, t)
	st := m.meta.Unlink(m.ctx, parent, name)
	st2 := m.unlink(parent, name)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

const SymlinkMax = 65536

func (m *fsMachine) Symlink(t *rapid.T) {
	parent := m.pickNode(t)
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
	target := rapid.StringN(1, 1000, SymlinkMax+1).Draw(t, "target")
	if name == "." || name == ".." {
		t.Skipf("skip symlink %s", name)
	}
	if target == "." || target == ".." {
		t.Skipf("skip symlink %s", target)
	}
	var ti Ino
	st := m.meta.Symlink(m.ctx, parent, name, target, &ti, nil)
	st2 := m.symlink(parent, name, ti, target)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Readlink(t *rapid.T) {
	inode := m.pickNode(t)
	var target []byte
	st := m.meta.ReadLink(m.ctx, inode, &target)
	target2, st2 := m.readlink(inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && string(target) != target2 {
		t.Fatalf("expect %s but got %s", target2, target)
	}
}

func (m *fsMachine) Lookup(t *rapid.T) {
	parent := m.pickNode(t)
	name := m.pickChild(parent, t)
	var inode Ino
	var attr Attr
	st := m.meta.Lookup(m.ctx, parent, name, &inode, &attr, true)
	inode2, st2 := m.lookup(parent, name, true)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && inode != inode2 {
		t.Fatalf("expect %d but got %d", inode2, inode)
	}
}

func (m *fsMachine) Getattr(t *rapid.T) {
	inode := m.pickNode(t)
	var attr Attr
	st := m.meta.GetAttr(m.ctx, inode, &attr)
	t.Logf("attr %#v", attr)
	var n *tNode
	if st == 0 {
		n = new(tNode)
		n._type = attr.Typ
		n.mode = attr.Mode
		n.uid = attr.Uid
		n.gid = attr.Gid
		// n.atime = attr.Atime
		// n.mtime = attr.Mtime
		// n.ctime = attr.Ctime
		n.length = attr.Length
	}
	n2, st2 := m.getattr(inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if n2 != nil {
		if n2._type != n._type || n2.mode != n.mode ||
			n2.uid != n.uid || n2.gid != n.gid ||
			// n2.atime != n.atime || n2.mtime != n.mtime || n2.ctime != n.ctime ||
			n2.length != n.length {
			t.Logf("expect %+v but got %+v", n2, n)
			t.Fatalf("attr not matched")
		}
	}
}

func (m *fsMachine) Rename(t *rapid.T) {
	dstName := rapid.StringN(1, 200, 255).Draw(t, "name")
	if dstName == "." || dstName == ".." {
		t.Skipf("skip name . and ..")
	}

	srcParent := m.pickNode(t)
	srcName := m.pickChild(srcParent, t)
	if srcName == "" {
		return
	}
	var srcIno Ino
	for _, n := range m.nodes[srcParent].children {
		if n.name == srcName {
			srcIno = n.inode
		}
	}
	dstParent := m.pickNode(t)

	if srcIno == dstParent {
		t.Skipf("skip rename srcIno is dstParent")
	}
	tmp := m.nodes[dstParent].inode
	for {
		if tmp == RootInode {
			break
		}
		if tmp == srcIno {
			t.Skipf("skip rename dstParent is subdir of srcIno")
		} else {
			tmp = m.nodes[tmp].parents[0].inode
		}
	}

	var inode Ino
	var attr Attr
	st := m.rename(srcParent, srcName, dstParent, dstName, 0)
	st2 := m.meta.Rename(m.ctx, srcParent, srcName, dstParent, dstName, 0, &inode, &attr)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st, st2)
	}
}

/*
Due to concurrency issues, the execution result of rmr is unpredictable.

	func (m *fsMachine) Rmr(t *rapid.T) {
		parent := m.pickNode(t)
		t.Logf("rmr parent ino %d", parent)
		name := m.pickChild(parent, t)
		var removed, removed2 uint64
		st := m.meta.Remove(m.ctx, parent, name, &removed)
		st2 := m.rmr(parent, name, &removed2)
		if st != st2 {
			t.Fatalf("expect %s but got %s", st2, st)
		}
		if removed != removed2 {
			t.Fatalf("expect removed %d but got %d", removed2, removed)
		}
	}
*/

func (m *fsMachine) Readdir(t *rapid.T) {
	inode := m.pickNode(t)
	var names []string
	var result []*Entry
	st := m.meta.Readdir(m.ctx, inode, 0, &result)
	if st == 0 {
		for _, e := range result {
			names = append(names, string(e.Name))
		}
		sort.Strings(names)
	}
	stdRes, st2 := m.readdir(inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	var names2 []string
	for _, entry := range stdRes {
		names2 = append(names2, entry.name)
	}
	if st == 0 && !reflect.DeepEqual(names, names2) {
		t.Fatalf("expect %+v but got %+v", names2, names)
	}
}

// Truncate is currently disabled.
// FIXME: The comparison of the truncate results requires compacting all slices,
// and some tricky processing are required on results.
//func (m *fsMachine) Truncate(t *rapid.T) {
//	inode := m.pickNode(t)
//	length := rapid.Uint64Range(0, 500<<20).Draw(t, "length")
//	var attr Attr
//	st := m.meta.Truncate(m.ctx, inode, 0, length, &attr, false)
//	st2 := m.truncate(inode, length)
//	if st != st2 {
//		t.Fatalf("expect %s but got %s", st2, st)
//	}
//}

func (m *fsMachine) Fallocate(t *rapid.T) {
	inode := m.pickNode(t)
	offset := rapid.Uint64Range(0, 500<<20).Draw(t, "offset")
	length := rapid.Uint64Range(1, 500<<20).Draw(t, "length")
	st := m.meta.Fallocate(m.ctx, inode, 0, offset, length, nil)
	st2 := m.fallocate(inode, 0, offset, length)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

// CopyFileRange is currently disabled, same reason as Truncate.
//func (m *fsMachine) CopyFileRange(t *rapid.T) {
//	srcinode := m.pickNode(t)
//	srcoff := rapid.Uint64Max(m.nodes[srcinode].length).Draw(t, "srcoff")
//	dstinode := m.pickNode(t)
//	dstoff := rapid.Uint64Max(m.nodes[dstinode].length).Draw(t, "dstoff")
//	size := rapid.Uint64Max(m.nodes[srcinode].length).Draw(t, "size")
//	var copied uint64
//	st := m.meta.CopyFileRange(m.ctx, srcinode, srcoff, dstinode, dstoff, size, 0, &copied)
//	st2 := m.copy_file_range(srcinode, srcoff, dstinode, dstoff, size, 0)
//	if st != st2 {
//		t.Fatalf("expect %s but got %s", st2, st)
//	}
//}

func (m *fsMachine) getPath(inode Ino) string {
	n := m.nodes[inode]
	if n == nil {
		return ""
	}
	if len(n.parents) == 0 {
		return "/"
	}
	p := n.parents[0]
	for name, t := range p.children {
		if t == n {
			return m.getPath(p.inode) + "/" + name
		}
	}
	panic("unreachable")
}

func (m *fsMachine) Write(t *rapid.T) {
	inode := m.pickNode(t)
	indx := rapid.Uint32Range(0, 10).Draw(t, "indx")
	pos := rapid.Uint32Range(0, ChunkSize).Draw(t, "pos")
	var chunkid uint64
	m.meta.NewSlice(m.ctx, &chunkid)
	cleng := rapid.Uint32Range(1, ChunkSize).Draw(t, "cleng")
	off := rapid.Uint32Range(0, cleng-1).Draw(t, "off")
	len := rapid.Uint32Range(1, cleng-off).Draw(t, "len")
	st := m.meta.Write(m.ctx, inode, indx, pos, Slice{chunkid, cleng, off, len}, time.Time{})
	st2 := m.write(inode, indx, pos, chunkid, cleng, off, len)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Read(t *rapid.T) {
	inode := m.pickNode(t)
	indx := rapid.Uint32Range(0, 10).Draw(t, "indx")
	var result []Slice
	st := m.meta.Read(m.ctx, inode, indx, &result)
	var slices []tSlice
	if st == 0 {
		var pos uint32
		for _, so := range result {
			s := tSlice{pos, so.Id, so.Size, so.Off, so.Len}
			slices = append(slices, s)
			pos += slices[len(slices)-1].len
		}
	}
	_, slices2, st2 := m.read(inode, indx)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && !reflect.DeepEqual(cleanupSlices(slices), cleanupSlices(slices2)) {
		t.Fatalf("expect %+v but got %+v", slices2, slices)
	}
}

func cleanupSlices(ss []tSlice) []tSlice {
	for i := 0; i < len(ss); i++ {
		s := ss[i]
		if s.id == 0 && s.off > 0 {
			s.off = 0
			ss[i] = s
		}
		if ss[i].id == 0 && i > 0 && ss[i-1].id == 0 {
			ss[i-1].len += ss[i].len
			ss = append(ss[:i], ss[i+1:]...)
			i--
		}
	}
	for len(ss) > 0 && ss[len(ss)-1].id == 0 {
		ss = ss[:len(ss)-1]
	}
	if len(ss) == 0 {
		ss = nil
	}
	return ss
}

func (m *fsMachine) SetXAttr(t *rapid.T) {
	inode := m.pickNode(t)
	name := rapid.StringN(1, 200, XATTR_NAME_MAX+1).Draw(t, "name")
	value := rapid.SliceOfN(rapid.Byte(), 0, XATTR_SIZE_MAX+1).Draw(t, "value")
	mode := rapid.Uint8Range(0, XATTR_REMOVE).Draw(t, "mode")
	st := m.meta.SetXattr(m.ctx, inode, name, value, uint32(mode))
	st2 := m.setxattr(inode, name, value, mode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) RemoveXattr(t *rapid.T) {
	inode := m.pickNode(t)
	name := rapid.StringN(1, 200, XATTR_NAME_MAX+1).Draw(t, "name")
	st := m.meta.RemoveXattr(m.ctx, inode, name)
	st2 := m.removexattr(inode, name)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

const XATTR_REMOVE = 5
const XATTR_NAME_MAX = 255
const XATTR_SIZE_MAX = 65536

func (m *fsMachine) GetXAttr(t *rapid.T) {
	inode := m.pickNode(t)
	name := rapid.StringN(1, 200, XATTR_NAME_MAX+1).Draw(t, "name")
	var value []byte
	st := m.meta.GetXattr(m.ctx, inode, name, &value)
	value2, st2 := m.getxattr(inode, name)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && string(value) != string(value2) {
		t.Fatalf("expect %s but got %s", string(value2), string(value))
	}
}

func (m *fsMachine) ListXAttr(t *rapid.T) {
	inode := m.pickNode(t)
	var attrs []byte
	st := m.meta.ListXattr(m.ctx, inode, &attrs)
	attrs2, st2 := m.listxattr(inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	as := strings.Split(string(attrs), "\x00")
	sort.Strings(as)
	as2 := strings.Split(string(attrs2), "\x00")
	sort.Strings(as2)
	if st == 0 && !reflect.DeepEqual(as, as2) {
		t.Fatalf("expect %s but got %s", string(attrs2), string(attrs))
	}
}

func (m *fsMachine) Check(t *rapid.T) {
	m.ctx = NewContext(0, 0, []uint32{0})
	if err := m.checkFSTree(RootInode); err != nil {
		t.Fatalf("check FSTree error %s", err)
	}
}

func (m *fsMachine) checkFSTree(root Ino) error {
	var result []*Entry
	if st := m.meta.Readdir(m.ctx, root, 1, &result); st != 0 {
		return fmt.Errorf("meta readdir error %s", st)
	}
	sort.Slice(result, func(i, j int) bool { return string(result[i].Name) < string(result[j].Name) })

	stdResult, st := m.readdir(root)
	if st != 0 {
		return fmt.Errorf("standard meta readdir error %d", st)
	}
	if len(result) != len(stdResult) {
		return fmt.Errorf("the results of reading the directory should have equal lengths. standard meta: %#v test meta: %#v", stdResult, result)
	}
	for i := 0; i < len(result); i++ {
		stdEntry := stdResult[i]
		stdNode := stdEntry.node
		entry := result[i]
		if stdEntry.name == "." || stdEntry.name == ".." {
			continue
		}
		if stdEntry.name != string(entry.Name) {
			return fmt.Errorf("name should equal. ino %d standard meta: %s, test meta %s", stdNode.inode, stdNode.name, string(entry.Name))
		}
		if stdNode._type != entry.Attr.Typ {
			return fmt.Errorf("type should equal ino: %d, standard meta: %d, test meta %d", entry.Inode, stdNode._type, entry.Attr.Typ)
		}
		switch entry.Attr.Typ {
		case TypeDirectory:
			if err := m.checkFSTree(entry.Inode); err != nil {
				return err
			}
		default:
			if stdNode.inode != entry.Inode {
				return fmt.Errorf("inode should equal. standard meta: %d, test meta %d", stdNode.inode, entry.Inode)
			}
			if stdNode.gid != entry.Attr.Gid {
				return fmt.Errorf("gid should equal. ino %d standard meta: %d, test meta %d", stdNode.inode, stdNode.gid, entry.Attr.Gid)
			}
			if stdNode.uid != entry.Attr.Uid {
				return fmt.Errorf("uid should equal. ino %d standard meta: %d, test meta %d", stdNode.inode, stdNode.uid, entry.Attr.Uid)
			}
			if stdNode.length != entry.Attr.Length {
				return fmt.Errorf("length should equal. ino %d standard meta: %d, test meta %d", stdNode.inode, stdNode.length, entry.Attr.Length)
			}
			if stdNode.iflags != entry.Attr.Flags {
				return fmt.Errorf("flags should equal. ino %d standard meta: %d, test meta %d", stdNode.inode, stdNode.iflags, entry.Attr.Flags)
			}
			if stdNode.mode != entry.Attr.Mode {
				return fmt.Errorf("mode should equal. ino %d standard meta: %d, test meta %d", stdNode.inode, stdNode.mode, entry.Attr.Mode)
			}
			// If a hard link has been set, the parent will be cleared.
			if !stdNode.hardlink {
				if stdNode.parents[0].inode != entry.Attr.Parent {
					return fmt.Errorf("parent should equal. ino %d standard meta: %d, test meta %d", stdNode.inode, stdNode.parents[0].inode, entry.Attr.Parent)
				}
			}

			// check chunks
			for indx := range stdNode.chunks {
				var rs []Slice
				st := m.meta.Read(m.ctx, stdNode.inode, indx, &rs)
				var slices []tSlice
				if st == 0 {
					var pos uint32
					for _, so := range rs {
						s := tSlice{pos, so.Id, so.Size, so.Off, so.Len}
						slices = append(slices, s)
						pos += slices[len(slices)-1].len
					}
				}
				_, slices2, st2 := m.read(stdNode.inode, indx)
				if st != st2 {
					return fmt.Errorf("read eno should equal. standard meta ino %d ,indx %d std meta eno %d test meta eno %d", stdNode.inode, indx, st2, st)
				}
				if st == 0 && !reflect.DeepEqual(cleanupSlices(slices), cleanupSlices(slices2)) {
					return fmt.Errorf("slice should equal. standard meta %+v test meta %+v", slices2, slices)
				}
			}

			// check symlink
			var target []byte
			st := m.meta.ReadLink(m.ctx, stdNode.inode, &target)
			target2, st2 := m.readlink(stdNode.inode)
			if st != st2 {
				return fmt.Errorf("readlink eno should equal. standard meta ino %d stadndard meta %d test meta %d", stdNode.inode, st2, st)
			}
			if st == 0 && string(target) != target2 {
				return fmt.Errorf("symlink should equal. standard meta ino %d stadndard meta %s test meta %s", stdNode.inode, target2, string(target))
			}

			// check xattr
			var attrs []byte
			st = m.meta.ListXattr(m.ctx, stdNode.inode, &attrs)
			attrs2, st2 := m.listxattr(stdNode.inode)
			if st != st2 {
				return fmt.Errorf("listxattr eno should equal. standard meta ino %d stadndard meta %d test meta %d", stdNode.inode, st2, st)
			}
			as := strings.Split(string(attrs), "\x00")
			sort.Strings(as)
			as2 := strings.Split(string(attrs2), "\x00")
			sort.Strings(as2)
			if st == 0 && !reflect.DeepEqual(as, as2) {
				return fmt.Errorf("listxattr should equal. standard meta ino %d stadndard meta %s test meta %s", stdNode.inode, as2, as)
			}
		}
	}
	return nil
}

func (m *fsMachine) setfacl(inode Ino, atype uint8, rule *aclAPI.Rule) syscall.Errno {
	if atype != aclAPI.TypeAccess && atype != aclAPI.TypeDefault {
		return syscall.EINVAL
	}
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if m.ctx.Uid() != 0 && m.ctx.Uid() != n.uid {
		return syscall.EPERM
	}

	if rule.IsEmpty() {
		if atype == aclAPI.TypeDefault {
			n.defACL = nil
			m.removexattr(inode, "system.posix_acl_default")
		}
		// TODO: update ctime
		return 0
	}

	if rule.IsMinimal() && atype == aclAPI.TypeAccess {
		n.accACL = nil
		n.mode &= 07000
		n.mode |= ((rule.Owner & 7) << 6) | ((rule.Group & 7) << 3) | (rule.Other & 7)
		return 0
	}

	rule.InheritPerms(n.mode)
	if atype == aclAPI.TypeAccess {
		n.accACL = rule
		if n.accACL.GetMode() != n.mode&0777 {
			n.mode = n.mode&07000 | n.accACL.GetMode()
		}
	} else {
		n.defACL = rule
	}
	return 0
}

func (m *fsMachine) Setfacl(t *rapid.T) {
	inode := m.pickNode(t)
	atype := rapid.Uint8Range(1, 2).Draw(t, "atype")
	user := rapid.Uint16Range(0, 7).Draw(t, "user")
	group := rapid.Uint16Range(0, 7).Draw(t, "group")
	other := rapid.Uint16Range(0, 7).Draw(t, "other")
	mask := rapid.Uint16Range(0, 7).Draw(t, "mask")
	var users aclAPI.Entries
	var groups aclAPI.Entries

	us := rapid.IntRange(0, 3).Draw(t, "users")
	for i := 0; i < us; i++ {
		users = append(users, aclAPI.Entry{Id: rapid.Uint32Range(1, 5).Draw(t, "uid"), Perm: rapid.Uint16Range(0, 7).Draw(t, "perm")})
	}
	gs := rapid.IntRange(0, 3).Draw(t, "groups")
	for i := 0; i < gs; i++ {
		groups = append(groups, aclAPI.Entry{Id: rapid.Uint32Range(1, 5).Draw(t, "gid"), Perm: rapid.Uint16Range(0, 7).Draw(t, "perm")})
	}
	rule := &aclAPI.Rule{
		Owner:       user,
		Group:       group,
		Mask:        mask,
		Other:       other,
		NamedUsers:  users,
		NamedGroups: groups,
	}

	st := m.meta.SetFacl(m.ctx, inode, atype, rule)
	st2 := m.setfacl(inode, atype, rule)

	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) getfacl(inode Ino, atype uint8) (*aclAPI.Rule, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	switch atype {
	case aclAPI.TypeAccess:
		if n.accACL == nil {
			return nil, ENOATTR
		}
		return n.accACL, 0
	case aclAPI.TypeDefault:
		if n.defACL == nil {
			return nil, ENOATTR
		}
		return n.defACL, 0
	default:
		return nil, syscall.EINVAL
	}
}

func (m *fsMachine) GetACL(t *rapid.T) {
	inode := m.pickNode(t)
	atype := rapid.Uint8Range(1, 2).Draw(t, "atype")

	rule := &aclAPI.Rule{}
	st := m.meta.GetFacl(m.ctx, inode, atype, rule)
	rule2, st2 := m.getfacl(inode, atype)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && !rule.IsEqual(rule2) {
		t.Fatalf("expect %+v but got %+v, %t", rule2, rule, reflect.DeepEqual(rule, *rule2))
	}
}

func (m *fsMachine) RemoveACL(t *rapid.T) {
	inode := m.pickNode(t)
	atype := rapid.Uint8Range(1, 2).Draw(t, "atype")

	var rule *aclAPI.Rule
	if atype == aclAPI.TypeAccess {
		rule = &aclAPI.Rule{
			Mask: 0xFFFF,
		}
		rule.InheritPerms(m.nodes[inode].mode)
	} else {
		rule = aclAPI.EmptyRule()
	}

	st := m.meta.SetFacl(m.ctx, inode, atype, rule)
	st2 := m.setfacl(inode, atype, rule)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (n *tNode) stat(visited map[Ino]struct{}) (uint64, uint64) {
	if _, ok := visited[n.inode]; ok {
		return 0, 0
	}
	visited[n.inode] = struct{}{}

	var size uint64 = uint64(align4K(n.length))
	var inodes uint64 = 1
	if n._type == TypeDirectory {
		for _, c := range n.children {
			s, i := c.stat(visited)
			size += s
			inodes += i
		}
	}
	return size, inodes
}

func (m *fsMachine) statfs(format Format) (uint64, uint64, uint64, uint64) {
	n := m.nodes[RootInode]
	visited := make(map[Ino]struct{})
	used, iused := n.stat(visited)
	used -= uint64(align4K(0))
	iused -= 1
	var avail, iavail uint64
	avail = 1<<50 - used
	iavail = 10 << 20
	// if inodes is not limited in Format, iavail always keep the same number of inodes
	if format.Inodes > 0 {
		iavail -= iused
	}
	if n.quota != nil {
		if n.quota.size > 0 {
			if used > n.quota.size {
				avail = 0
			} else {
				avail = n.quota.size - used
			}
		}
		if n.quota.inodes > 0 {
			if iused > n.quota.inodes {
				iavail = 0
			} else {
				iavail = uint64(n.quota.inodes) - iused
			}
		}
	}
	return used + avail, avail, iused, iavail
}

func (m *fsMachine) StatFS(t *rapid.T) {
	var totalsize, availspace, iused, iavail uint64
	m.meta.StatFS(m.ctx, RootInode, &totalsize, &availspace, &iused, &iavail)
	total2, avail2, iused2, iavail2 := m.statfs(m.meta.GetFormat())
	if totalsize != total2 || availspace != avail2 || iused != iused2 || iavail != iavail2 {
		t.Fatalf("expect %d %d %d %d but got %d %d %d %d", total2, avail2, iused2, iavail2, totalsize, availspace, iused, iavail)
	}
}

func (m *fsMachine) amtime(inode Ino, flag uint16, atime, mtime int64, oattr *Attr) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}

	changed := false
	if flag&SetAttrAtime != 0 {
		n.atime = atime
		changed = changed || oattr.Atime != atime
	}
	if flag&SetAttrMtime != 0 {
		n.mtime = mtime
		changed = changed || oattr.Mtime != mtime
	}

	if changed {
		if n.uid == 0 && m.ctx.Uid() != 0 {
			return syscall.EPERM
		}
		if ok := n.access(m.ctx, MODE_MASK_W); !ok && n.uid != m.ctx.Uid() {
			return syscall.EACCES
		}
	}
	// TODO ctime
	return 0
}

func (m *fsMachine) SetAmtime(t *rapid.T) {
	inode := m.pickNode(t)

	oattr := &Attr{}
	if st := m.meta.GetAttr(m.ctx, inode, oattr); st != 0 {
		return
	}

	atime := rapid.Int64Range(0, 1e8).Draw(t, "atime")
	mtime := rapid.Int64Range(0, 1e8).Draw(t, "mtime")
	var flag uint16
	attr := &Attr{
		Atime: atime,
		Mtime: mtime,
	}

	if atime > 0 {
		flag |= SetAttrAtime
	}
	if mtime > 0 {
		flag |= SetAttrMtime
	}
	st2 := m.amtime(inode, flag, atime, mtime, oattr)
	st := m.meta.SetAttr(m.ctx, inode, flag, 0, attr)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}

	if st == 0 {
		// validate time only here
		node := m.nodes[inode]
		if flag&SetAttrAtime != 0 && attr.Atime != node.atime {
			t.Fatalf("expect %d but got %d", node.atime, attr.Atime)
		}
		if flag&SetAttrMtime != 0 && attr.Mtime != node.mtime {
			t.Fatalf("expect %d but got %d", node.mtime, attr.Mtime)
		}
	}
}

func (m *fsMachine) chmod(inode Ino, mode uint16) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if n.accACL != nil {
		n.accACL.SetMode(mode)
		n.mode = mode&07000 | n.accACL.GetMode()
	} else {
		if m.ctx.Uid() != 0 && m.ctx.Uid() != n.uid &&
			(n.mode&01777 != mode&01777 || mode&02000 > n.mode&02000 || mode&04000 > n.mode&04000) {
			return syscall.EPERM
		}
		n.mode = mode
	}
	// n.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) Chmod(t *rapid.T) {
	inode := m.pickNode(t)
	mode := rapid.Uint16Range(0, 01777).Draw(t, "mode")
	st := m.meta.SetAttr(m.ctx, inode, SetAttrMode, 0, &Attr{Mode: mode})
	st2 := m.chmod(inode, mode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) chown(inode Ino, flag uint16, uid, gid uint32) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if flag&SetAttrUID != 0 && n.uid != uid {
		if m.ctx.Uid() != 0 {
			return syscall.EPERM
		}
		n.uid = uid
	}
	if flag&SetAttrGID != 0 {
		if m.ctx.Uid() != 0 && m.ctx.Uid() != n.uid {
			return syscall.EPERM
		}
		if n.gid != gid {
			if m.ctx.CheckPermission() && m.ctx.Uid() != 0 && !containsGid(m.ctx, gid) {
				return syscall.EPERM
			}
			n.gid = gid
		}
	}
	// n.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) Chown(t *rapid.T) {
	inode := m.pickNode(t)
	uid := rapid.Uint32Range(0, 10).Draw(t, "uid")
	gid := rapid.Uint32Range(0, 10).Draw(t, "gid")
	var flag uint16
	if uid < 10 {
		flag |= SetAttrUID
	}
	if gid < 10 {
		flag |= SetAttrGID
	}
	st := m.meta.SetAttr(m.ctx, inode, flag, 0, &Attr{Uid: uid, Gid: gid})
	st2 := m.chown(inode, flag, uid, gid)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) flock(inode Ino, owner uint64, typ uint32) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	// m.openfiles[inode] = true
	if n.flocks == nil {
		n.flocks = make(map[ownerKey]byte)
	}
	lowner := ownerKey{Sid: m.sid, Owner: owner}
	switch typ {
	case F_UNLCK:
		delete(n.flocks, lowner)
	case F_RDLCK:
		for o, l := range n.flocks {
			if l == 'W' && o != lowner {
				return syscall.EAGAIN
			}
		}
		n.flocks[lowner] = 'R'
	case F_WRLCK:
		for o := range n.flocks {
			if o == lowner {
				continue
			}
			return syscall.EAGAIN
		}
		n.flocks[lowner] = 'W'
	default:
		return syscall.EINVAL
	}
	return 0
}

func (m *fsMachine) Flock(t *rapid.T) {
	inode := m.pickNode(t)
	owner := rapid.Uint64().Draw(t, "owner")
	typ := rapid.Uint32Range(0, 3).Draw(t, "typ")
	st := m.flock(inode, owner, typ)
	st2 := m.meta.Flock(m.ctx, inode, owner, typ, false)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}

	if st == 0 {
		plocks1, flocks1, err1 := m.meta.ListLocks(m.ctx, inode)
		plocks2, flocks2, err2 := m.listLocks(inode)
		if err1 != nil && err2 == nil || err1 == nil && err2 != nil {
			t.Fatalf("expect %s but got %s", err2, err1)
		}
		if err1 == nil {
			sort.Slice(flocks1, func(i, j int) bool {
				return flocks1[i].Owner < flocks1[j].Owner
			})
			sort.Slice(flocks2, func(i, j int) bool {
				return flocks2[i].Owner < flocks2[j].Owner
			})
			if !reflect.DeepEqual(flocks1, flocks2) {
				t.Fatalf("expect %+v but got %+v", flocks2, flocks1)
			}
			sort.Slice(plocks1, func(i, j int) bool {
				if plocks1[i].Owner != plocks1[j].Owner {
					return plocks1[i].Owner < plocks1[j].Owner
				}
				if plocks1[i].Start != plocks1[j].Start {
					return plocks1[i].Start < plocks1[j].Start
				}
				return plocks1[i].End < plocks1[j].End
			})
			sort.Slice(plocks2, func(i, j int) bool {
				if plocks2[i].Owner != plocks2[j].Owner {
					return plocks2[i].Owner < plocks2[j].Owner
				}
				if plocks2[i].Start != plocks2[j].Start {
					return plocks2[i].Start < plocks2[j].Start
				}
				return plocks2[i].End < plocks2[j].End
			})
			if !reflect.DeepEqual(plocks1, plocks2) {
				t.Fatalf("expect %+v but got %+v", plocks2, plocks1)
			}
		}
	}
}

func (m *fsMachine) listLocks(inode Ino) ([]PLockItem, []FLockItem, error) {
	var flocks []FLockItem
	var plocks []PLockItem
	n := m.nodes[inode]
	if n == nil {
		return plocks, flocks, syscall.ENOENT
	}
	for o, l := range n.flocks {
		flocks = append(flocks, FLockItem{ownerKey: ownerKey{
			Sid:   o.Sid,
			Owner: o.Owner,
		}, Type: string(l)})
	}
	for o, ls := range n.plocks {
		for _, l := range ls {
			plocks = append(plocks, PLockItem{ownerKey: ownerKey{
				Sid:   o.Sid,
				Owner: o.Owner,
			}, plockRecord: l})
		}
	}
	return plocks, flocks, nil
}

func (m *fsMachine) ListLocks(t *rapid.T) {
	inode := m.pickNode(t)
	plocks1, flocks1, err1 := m.meta.ListLocks(m.ctx, inode)
	plocks2, flocks2, err2 := m.listLocks(inode)
	if err1 != nil && err2 == nil || err1 == nil && err2 != nil {
		t.Fatalf("expect %s but got %s", err2, err1)
	}
	if err1 == nil {
		// sort flocks by owner
		sort.Slice(flocks1, func(i, j int) bool {
			return flocks1[i].Owner < flocks1[j].Owner
		})
		sort.Slice(flocks2, func(i, j int) bool {
			return flocks2[i].Owner < flocks2[j].Owner
		})
		if !reflect.DeepEqual(flocks1, flocks2) {
			t.Fatalf("expect %+v but got %+v", flocks2, flocks1)
		}
		// sort plocks by owner
		sort.Slice(plocks1, func(i, j int) bool {
			return plocks1[i].Owner < plocks1[j].Owner
		})
		sort.Slice(plocks2, func(i, j int) bool {
			return plocks2[i].Owner < plocks2[j].Owner
		})
		if !reflect.DeepEqual(plocks1, plocks2) {
			t.Fatalf("expect %+v but got %+v", plocks2, plocks1)
		}
	}
}

func (m *fsMachine) getlk(inode Ino, owner uint64, ltype *uint32, start *uint64, end *uint64, pid *uint32) syscall.Errno {
	if *ltype == F_UNLCK {
		*start = 0
		*end = 0
		*pid = 0
		return 0
	}
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	for o, ls := range n.plocks {
		for _, l := range ls {
			if o.Owner != owner && (*ltype == F_WRLCK || l.Type == F_WRLCK) && *end >= l.Start && *start <= l.End {
				*ltype = l.Type
				*start = l.Start
				*end = l.End
				if o.Sid == m.sid {
					*pid = l.Pid
				} else {
					*pid = 0
				}
				return 0
			}
		}
	}
	*ltype = F_UNLCK
	*start = 0
	*end = 0
	*pid = 0
	return 0
}

func (m *fsMachine) Getlk(t *rapid.T) {
	inode := m.pickNode(t)
	owner := rapid.Uint64().Draw(t, "owner")
	ltype := rapid.Uint32Range(0, 2).Draw(t, "ltype")
	start := rapid.Uint64Range(0, 500<<20).Draw(t, "start")
	length := rapid.Uint64Range(1, 500<<20).Draw(t, "len")
	end := start + length - 1

	var pid1, pid2 uint32
	ftype1, ftype2 := ltype, ltype
	fstart1, fstart2 := start, start
	fend1, fend2 := end, end
	st := m.getlk(inode, owner, &ftype1, &fstart1, &fend1, &pid1)
	st2 := m.meta.Getlk(m.ctx, inode, owner, &ftype2, &fstart2, &fend2, &pid2)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && ltype != F_UNLCK && (ftype1 == F_UNLCK && ftype2 != F_UNLCK || ftype1 != F_UNLCK && ftype2 == F_UNLCK) {
		t.Fatalf("status not right, %d %d", ftype1, ftype2)
	}
}

func (m *fsMachine) setlk(inode Ino, owner uint64, ltype uint32, start uint64, end uint64, pid uint32) syscall.Errno {
	n := m.nodes[inode]
	if n == nil {
		return syscall.ENOENT
	}
	if ltype != F_UNLCK {
		// m.openfiles[inode] = true
	}
	if n.plocks == nil {
		n.plocks = make(map[ownerKey][]plockRecord)
	}
	lowner := ownerKey{Sid: m.sid, Owner: owner}
	if ltype == F_UNLCK {
		if n.plocks[lowner] == nil {
			return 0
		}
	} else {
		for o, ls := range n.plocks {
			for _, l := range ls {
				if o != lowner && (ltype == F_WRLCK || l.Type == F_WRLCK) && end >= l.Start && start <= l.End {
					return syscall.EAGAIN
				}
			}
		}
	}
	ls := updateLocks(n.plocks[lowner], plockRecord{ltype, pid, start, end})
	if len(ls) == 0 {
		delete(n.plocks, lowner)
	} else {
		n.plocks[lowner] = ls
	}
	return 0
}

func (m *fsMachine) Setlk(t *rapid.T) {
	inode := m.pickNode(t)
	owner := rapid.Uint64().Draw(t, "owner")
	ltype := rapid.Uint32Range(0, 2).Draw(t, "ltype")
	start := rapid.Uint64Range(0, 500<<20).Draw(t, "start")
	len := rapid.Uint64Range(1, 500<<20).Draw(t, "len")
	pid := rapid.Uint32Range(1, 10000).Draw(t, "pid")
	var end = start + len - 1
	st := m.meta.Setlk(m.ctx, inode, owner, false, ltype, start, end, pid)
	st2 := m.setlk(inode, owner, ltype, start, end, pid)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func TestFSOps(t *testing.T) {
	logger.SetLevel(logrus.ErrorLevel)
	defer logger.SetLevel(logrus.InfoLevel)
	flag.Set("timeout", "10s")
	flag.Set("rapid.steps", "200")
	flag.Set("rapid.checks", "5000")
	// flag.Set("rapid.seed", time.Now().String())
	flag.Set("rapid.seed", "1")
	rapid.Check(t, rapid.Run[*fsMachine]())
}
