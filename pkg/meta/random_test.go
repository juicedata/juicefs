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
	"os"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"pgregory.net/rapid"
)

type tSlice struct {
	pos  uint32
	id   uint64
	clen uint32
	off  uint32
	len  uint32
}

type tNode struct {
	inode Ino
	_type uint8
	mode  uint16
	uid   uint32
	gid   uint32
	// atime    uint32
	// mtime    uint32
	// ctime    uint32
	iflags   uint8
	length   uint64
	parents  []*tNode
	chunks   map[uint32][]tSlice
	children map[string]*tNode
	target   string
	xattrs   map[string][]byte
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
	ctx   Context
}

func (m *fsMachine) Init(t *rapid.T) {
	m.nodes = make(map[Ino]*tNode)
	m.nodes[1] = &tNode{
		_type:    TypeDirectory,
		mode:     0777,
		inode:    1,
		length:   4096,
		xattrs:   make(map[string][]byte),
		children: make(map[string]*tNode),
	}
	_ = os.Remove(settingPath)
	m.meta, _ = newKVMeta("memkv", "jfs-unit-test", testConfig())
	if err := m.meta.Init(testFormat(), true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
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
		_type:   _type,
		mode:    mode &^ umask,
		inode:   inode,
		uid:     m.ctx.Uid(),
		gid:     m.ctx.Gids()[0],
		parents: []*tNode{p},
		xattrs:  make(map[string][]byte),
	}
	m.nodes[inode] = n
	p.children[name] = n
	// p.mtime = m.ctx.ts
	// p.ctime = m.ctx.ts
	switch _type {
	case TypeDirectory:
		n.children = make(map[string]*tNode)
	case TypeFile:
		n.chunks = make(map[uint32][]tSlice)
	}
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
	if !n.access(m.ctx, MODE_MASK_R) {
		return "", syscall.EACCES
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

func (m *fsMachine) unlink(parent Ino, name string, dir bool) syscall.Errno {
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
	c := p.children[name]
	if c == nil {
		return syscall.ENOENT
	}
	if !p.stickyAccess(c, m.ctx.Uid()) {
		return syscall.EPERM
	}
	if dir {
		if c._type != TypeDirectory {
			return syscall.ENOTDIR
		}
		if len(c.children) != 0 {
			return syscall.ENOTEMPTY
		}
	} else {
		if c.children != nil {
			return syscall.EPERM
		}
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

func (m *fsMachine) lookup(parent Ino, name string) (Ino, syscall.Errno) {
	p := m.nodes[parent]
	if p == nil {
		return 0, syscall.ENOENT
	}
	if fsnodes_namecheck(name) != 0 {
		return 0, syscall.EINVAL
	}
	if p.children == nil {
		return 0, syscall.ENOTDIR
	}
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
	if !n.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
	if offset+size > n.length {
		n.length = offset + size
	}
	// n.mtime = m.ctx.ts
	// n.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) copy_file_range(srcinode Ino, srcoff uint64, dstinode Ino, dstoff uint64, size uint64, flags uint64) syscall.Errno {
	if srcinode == dstinode && (size == 0 || srcoff <= dstoff && dstoff < srcoff+size || dstoff < srcoff && srcoff < dstoff+size) {
		return syscall.EINVAL // overlap
	}
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
	if !src.access(m.ctx, MODE_MASK_R) {
		return syscall.EACCES
	}
	if !dst.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
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

func (m *fsMachine) rmr(parent Ino, name string, removed *uint64) syscall.Errno {
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
	c := p.children[name]
	if c == nil {
		return syscall.ENOENT
	}
	if !p.access(m.ctx, MODE_MASK_W|MODE_MASK_X) {
		return syscall.EACCES
	}
	if !p.stickyAccess(c, m.ctx.Uid()) {
		return syscall.EPERM
	}
	for n := range c.children {
		_ = m.rmr(c.inode, n, removed)
	}
	st := m.unlink(parent, name, c.children != nil)
	if st == 0 && removed != nil {
		(*removed)++
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
	srcnode := src.children[srcname]
	if srcnode == nil {
		return syscall.ENOENT
	}
	if !src.stickyAccess(srcnode, m.ctx.Uid()) {
		return syscall.EPERM
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
	if srcnode.children != nil && m.isancestor(srcnode, dst) {
		return syscall.EINVAL
	}
	if src != dst && srcnode._type == TypeDirectory && !srcnode.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
	if c := dst.children[dstname]; c != nil {
		if dst == src && dstname == srcname {
			return 0
		}
		if c == srcnode {
			return syscall.EPERM
		}
		if len(c.children) != 0 {
			return syscall.ENOTEMPTY
		}
		if dst != src || dstname != srcname {
			// if c._type == TypeDirectory && !c.access(m.ctx, MODE_MASK_W) {
			// 	return syscall.EACCES
			// }
			if !dst.stickyAccess(c, m.ctx.Uid()) {
				return syscall.EPERM
			}
			if st := m.rmr(dst.inode, dstname, nil); st != 0 {
				return syscall.ENOTEMPTY
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
	dst.children[dstname] = srcnode
	// srcnode.ctime = m.ctx.ts
	// src.mtime = m.ctx.ts
	// src.ctime = m.ctx.ts
	// dst.mtime = m.ctx.ts
	// dst.ctime = m.ctx.ts
	return 0
}

func (m *fsMachine) readdir(inode Ino) ([]string, syscall.Errno) {
	n := m.nodes[inode]
	if n == nil {
		return nil, syscall.ENOENT
	}
	if n.children == nil {
		return nil, syscall.ENOTDIR
	}
	if !n.access(m.ctx, MODE_MASK_R) {
		return nil, syscall.EACCES
	}
	var names = []string{".", ".."}
	for name := range n.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names, 0
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
	pos = pos % ChunkSize // fix invalid pos
	if chunkid == 0 || cleng == 0 || len == 0 || pos+len > ChunkSize || off+len > cleng {
		return syscall.EINVAL
	}
	if !n.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
	n.chunks[indx] = append(n.chunks[indx], tSlice{pos, chunkid, cleng, off, len})
	if uint64(indx)*ChunkSize+uint64(pos+len) > n.length {
		n.length = uint64(indx)*ChunkSize + uint64(pos) + uint64(len)
	}
	return 0
}

func (m *fsMachine) append_file(inode Ino, srcinode Ino) syscall.Errno {
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
	if inode == srcinode {
		return syscall.EPERM
	}
	sn := m.nodes[srcinode]
	if sn == nil {
		return syscall.ENOENT
	}
	if sn._type != TypeFile {
		return syscall.EPERM
	}
	if !sn.access(m.ctx, MODE_MASK_R) {
		return syscall.EACCES
	}
	return m.copy_file_range(srcinode, 0, inode, n.length, sn.length, 0)
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
		cs[i].clen = clen[cs[i].id]
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
			chunk = append(chunk, tSlice{pos: pos, len: s.pos - pos})
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
	if !n.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
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
	if !n.access(m.ctx, MODE_MASK_W) {
		return syscall.EACCES
	}
	if n.xattrs[name] == nil {
		return syscall.ENOATTR
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
	if !n.access(m.ctx, MODE_MASK_R) {
		return nil, syscall.EACCES
	}
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
	if !n.access(m.ctx, MODE_MASK_R) {
		return nil, syscall.EACCES
	}
	var names []string
	for name := range n.xattrs {
		names = append(names, name+"\x00")
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
	var inode Ino
	var attr Attr
	st := m.meta.Mkdir(m.ctx, parent, name, mode, 0, 0, &inode, &attr)
	st2 := m.create(TypeDirectory, parent, name, mode, 0, inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Mknod(t *rapid.T) {
	parent := m.pickNode(t)
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
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
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
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
	st2 := m.unlink(parent, name, true)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Unlink(t *rapid.T) {
	parent := m.pickNode(t)
	name := m.pickChild(parent, t)
	st := m.meta.Unlink(m.ctx, parent, name)
	st2 := m.unlink(parent, name, false)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

const SymlinkMax = 65536

func (m *fsMachine) Symlink(t *rapid.T) {
	parent := m.pickNode(t)
	name := rapid.StringN(1, 200, 255).Draw(t, "name")
	target := rapid.StringN(1, 1000, SymlinkMax+1).Draw(t, "target")
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
	inode2, st2 := m.lookup(parent, name)
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
	srcparent := m.pickNode(t)
	srcname := m.pickChild(srcparent, t)
	if srcname == "" {
		return
	}
	dstparent := m.pickNode(t)
	dstname := rapid.StringN(1, 200, 255).Draw(t, "name")
	var inode Ino
	var attr Attr
	st := m.meta.Rename(m.ctx, srcparent, srcname, dstparent, dstname, 0, &inode, &attr)
	st2 := m.rename(srcparent, srcname, dstparent, dstname, 0)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Rmr(t *rapid.T) {
	parent := m.pickNode(t)
	name := m.pickChild(parent, t)
	var removed, removed2 uint64
	st := m.meta.Remove(m.ctx, parent, name, &removed)
	st2 := m.rmr(parent, name, &removed2)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if removed != removed2 {
		t.Logf("expect removed %d but got %d", removed2, removed)
	}
}

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
	names2, st2 := m.readdir(inode)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
	if st == 0 && !reflect.DeepEqual(names, names2) {
		t.Fatalf("expect %+v but got %+v", names2, names)
	}
}

func (m *fsMachine) Truncate(t *rapid.T) {
	inode := m.pickNode(t)
	length := rapid.Uint64Range(0, 500<<20).Draw(t, "length")
	var attr Attr
	st := m.meta.Truncate(m.ctx, inode, 0, length, &attr, false)
	st2 := m.truncate(inode, length)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) Fallocate(t *rapid.T) {
	inode := m.pickNode(t)
	offset := rapid.Uint64Range(0, 500<<20).Draw(t, "offset")
	length := rapid.Uint64Range(1, 500<<20).Draw(t, "length")
	st := m.meta.Fallocate(m.ctx, inode, 0, offset, length)
	st2 := m.fallocate(inode, 0, offset, length)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

func (m *fsMachine) CopyFileRange(t *rapid.T) {
	srcinode := m.pickNode(t)
	srcoff := rapid.Uint64Max(m.nodes[srcinode].length).Draw(t, "srcoff")
	dstinode := m.pickNode(t)
	dstoff := rapid.Uint64Max(m.nodes[dstinode].length).Draw(t, "dstoff")
	size := rapid.Uint64Max(m.nodes[srcinode].length).Draw(t, "size")
	st := m.meta.CopyFileRange(m.ctx, srcinode, srcoff, dstinode, dstoff, size, 0, nil)
	st2 := m.copy_file_range(srcinode, srcoff, dstinode, dstoff, size, 0)
	if st != st2 {
		t.Fatalf("expect %s but got %s", st2, st)
	}
}

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

func (m *fsMachine) Setxattr(t *rapid.T) {
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

func (m *fsMachine) Getxattr(t *rapid.T) {
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

func (m *fsMachine) Listxattr(t *rapid.T) {
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

func (m *fsMachine) Test(t *rapid.T) {
}

func (m *fsMachine) Check(t *rapid.T) {
}

func TestFSOps(t *testing.T) {
	flag.Set("timeout", "10s")
	flag.Set("rapid.steps", "2")
	flag.Set("rapid.checks", "10000")
	flag.Set("rapid.seed", "1")
	rapid.Check(t, rapid.Run[*fsMachine]())
}
