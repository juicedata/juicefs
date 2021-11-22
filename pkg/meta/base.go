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

package meta

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

type baseMeta struct {
	sync.Mutex
	conf *Config
	fmt  Format

	root         Ino
	sid          uint64
	of           *openfiles
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	deleting     chan int
	symlinks     *sync.Map
	msgCallbacks *msgCallbacks
	newSpace     int64
	newInodes    int64
	usedSpace    int64
	usedInodes   int64
	umounting    bool

	freeMu     sync.Mutex
	freeInodes freeID
	freeChunks freeID

	// virtual methods
	incrCounter       func(name string, v int64) (int64, error)
	cleanStaleSession func(sid uint64, force bool)
	getattr           func(ctx Context, inode Ino, attr *Attr) syscall.Errno
	deleteInode       func(inode Ino)
	readlink          func(inode Ino) ([]byte, error)
	deleteSliceDB     func(chunkid uint64, size uint32) error
	deleteFile        func(inode Ino, length uint64)
	mknod             func(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno
	readdir           func(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno
	lookup            func(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno
}

func newBaseMeta(conf *Config) baseMeta {
	if conf.Retries == 0 {
		conf.Retries = 30
	}
	return baseMeta{
		conf:         conf,
		root:         1,
		of:           newOpenFiles(conf.OpenCache),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		deleting:     make(chan int, conf.MaxDeletes),
		symlinks:     &sync.Map{},
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}
}

func (m *baseMeta) checkRoot(inode Ino) Ino {
	if inode == 1 {
		return m.root
	}
	return inode
}

func (r *baseMeta) OnMsg(mtype uint32, cb MsgCallback) {
	r.msgCallbacks.Lock()
	defer r.msgCallbacks.Unlock()
	r.msgCallbacks.callbacks[mtype] = cb
}

func (r *baseMeta) newMsg(mid uint32, args ...interface{}) error {
	r.msgCallbacks.Lock()
	cb, ok := r.msgCallbacks.callbacks[mid]
	r.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}

func (m *baseMeta) CloseSession() error {
	if m.conf.ReadOnly {
		return nil
	}
	m.Lock()
	m.umounting = true
	m.Unlock()
	m.cleanStaleSession(m.sid, true)
	return nil
}

func (m *baseMeta) refreshUsage() {
	for {
		if v, err := m.incrCounter(usedSpace, 0); err == nil {
			atomic.StoreInt64(&m.usedSpace, v)
		}
		if v, err := m.incrCounter(totalInodes, 0); err == nil {
			atomic.StoreInt64(&m.usedInodes, v)
		}
		time.Sleep(time.Second * 10)
	}
}

func (m *baseMeta) checkQuota(size, inodes int64) bool {
	if size > 0 && m.fmt.Capacity > 0 && atomic.LoadInt64(&m.usedSpace)+atomic.LoadInt64(&m.newSpace)+size > int64(m.fmt.Capacity) {
		return true
	}
	return inodes > 0 && m.fmt.Inodes > 0 && atomic.LoadInt64(&m.usedInodes)+atomic.LoadInt64(&m.newInodes)+inodes > int64(m.fmt.Inodes)
}

func (m *baseMeta) updateStats(space int64, inodes int64) {
	atomic.AddInt64(&m.newSpace, space)
	atomic.AddInt64(&m.newInodes, inodes)
}

func (m *baseMeta) flushStats() {
	for {
		newSpace := atomic.SwapInt64(&m.newSpace, 0)
		if newSpace != 0 {
			if _, err := m.incrCounter(usedSpace, newSpace); err != nil {
				logger.Warnf("update space stats: %s", err)
				m.updateStats(newSpace, 0)
			}
		}
		newInodes := atomic.SwapInt64(&m.newInodes, 0)
		if newInodes != 0 {
			if _, err := m.incrCounter(totalInodes, newInodes); err != nil {
				logger.Warnf("update inodes stats: %s", err)
				m.updateStats(0, newInodes)
			}
		}
		time.Sleep(time.Second)
	}
}

func (m *baseMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	defer timeit(time.Now())
	var used, inodes int64
	var err error
	err = utils.WithTimeout(func() error {
		used, err = m.incrCounter(usedSpace, 0)
		return err
	}, time.Millisecond*150)
	if err != nil {
		used = atomic.LoadInt64(&m.usedSpace)
	}
	err = utils.WithTimeout(func() error {
		inodes, err = m.incrCounter(totalInodes, 0)
		return err
	}, time.Millisecond*150)
	if err != nil {
		inodes = atomic.LoadInt64(&m.usedInodes)
	}
	used += atomic.LoadInt64(&m.newSpace)
	inodes += atomic.LoadInt64(&m.newInodes)
	if used < 0 {
		used = 0
	}
	used = ((used >> 16) + 1) << 16 // aligned to 64K
	if m.fmt.Capacity > 0 {
		*totalspace = m.fmt.Capacity
		if *totalspace < uint64(used) {
			*totalspace = uint64(used)
		}
	} else {
		*totalspace = 1 << 50
		for *totalspace*8 < uint64(used)*10 {
			*totalspace *= 2
		}
	}
	*availspace = *totalspace - uint64(used)
	if inodes < 0 {
		inodes = 0
	}
	*iused = uint64(inodes)
	if m.fmt.Inodes > 0 {
		if *iused > m.fmt.Inodes {
			*iavail = 0
		} else {
			*iavail = m.fmt.Inodes - *iused
		}
	} else {
		*iavail = 10 << 20
		for *iused*10 > (*iused+*iavail)*8 {
			*iavail *= 2
		}
	}
	return 0
}

func (m *baseMeta) resolveCase(ctx Context, parent Ino, name string) *Entry {
	var entries []*Entry
	_ = m.readdir(ctx, parent, 0, &entries)
	for _, e := range entries {
		n := string(e.Name)
		if strings.EqualFold(name, n) {
			return e
		}
	}
	return nil
}

func (m *baseMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	if inode == nil || attr == nil {
		return syscall.EINVAL // bad request
	}
	defer timeit(time.Now())
	parent = m.checkRoot(parent)
	if name == ".." {
		if parent == m.root {
			name = "."
		} else {
			if st := m.GetAttr(ctx, parent, attr); st != 0 {
				return st
			}
			if attr.Typ != TypeDirectory {
				return syscall.ENOTDIR
			}
			*inode = attr.Parent
			return m.GetAttr(ctx, *inode, attr)
		}
	}
	if name == "." {
		st := m.GetAttr(ctx, parent, attr)
		if st != 0 {
			return st
		}
		if attr.Typ != TypeDirectory {
			return syscall.ENOTDIR
		}
		*inode = parent
		return 0
	}
	attr.Full = false
	err := m.lookup(ctx, parent, name, inode, attr)
	if err == syscall.ENOENT {
		if m.conf.CaseInsensi {
			if e := m.resolveCase(ctx, parent, name); e != nil {
				*inode = e.Inode
				return m.GetAttr(ctx, *inode, attr)
			}
		}
		return syscall.ENOENT
	}
	if err != 0 {
		return err
	}
	if attr.Full {
		return 0
	}
	return m.GetAttr(ctx, *inode, attr)
}

func (r *baseMeta) Resolve(ctx Context, parent Ino, path string, inode *Ino, attr *Attr) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *baseMeta) Access(ctx Context, inode Ino, mmask uint8, attr *Attr) syscall.Errno {
	if ctx.Uid() == 0 {
		return 0
	}
	if attr == nil || !attr.Full {
		if attr == nil {
			attr = &Attr{}
		}
		err := m.GetAttr(ctx, inode, attr)
		if err != 0 {
			return err
		}
	}
	mode := accessMode(attr, ctx.Uid(), ctx.Gid())
	if mode&mmask != mmask {
		logger.Debugf("Access inode %d %o, mode %o, request mode %o", inode, attr.Mode, mode, mmask)
		return syscall.EACCES
	}
	return 0
}

func (m *baseMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	inode = m.checkRoot(inode)
	if m.conf.OpenCache > 0 && m.of.Check(inode, attr) {
		return 0
	}
	defer timeit(time.Now())
	var err syscall.Errno
	if inode == 1 {
		e := utils.WithTimeout(func() error {
			err = m.getattr(ctx, inode, attr)
			return nil
		}, time.Millisecond*300)
		if e != nil {
			err = syscall.EIO
		}
	} else {
		err = m.getattr(ctx, inode, attr)
	}
	if err == 0 {
		m.of.Update(inode, attr)
	} else if err != 0 && inode == 1 {
		err = 0
		attr.Typ = TypeDirectory
		attr.Mode = 0777
		attr.Nlink = 2
		attr.Length = 4 << 10
	}
	return err
}

func (m *baseMeta) nextInode() (Ino, error) {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeInodes.next >= m.freeInodes.maxid {
		v, err := m.incrCounter("nextInode", 100)
		if err != nil {
			return 0, err
		}
		m.freeInodes.next = uint64(v) - 100
		m.freeInodes.maxid = uint64(v)
	}
	n := m.freeInodes.next
	m.freeInodes.next++
	return Ino(n), nil
}

func (m *baseMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	return m.mknod(ctx, parent, name, _type, mode, cumask, rdev, "", inode, attr)
}

func (m *baseMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	if attr == nil {
		attr = &Attr{}
	}
	err := m.mknod(ctx, parent, name, TypeFile, mode, cumask, 0, "", inode, attr)
	if err == syscall.EEXIST && (flags&syscall.O_EXCL) == 0 && attr.Typ == TypeFile {
		err = 0
	}
	if err == 0 && inode != nil {
		m.of.Open(*inode, attr)
	}
	return err
}

func (m *baseMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	return m.mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, "", inode, attr)
}

func (m *baseMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	defer timeit(time.Now())
	return m.mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (m *baseMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	if target, ok := m.symlinks.Load(inode); ok {
		*path = target.([]byte)
		return 0
	}
	defer timeit(time.Now())
	target, err := m.readlink(inode)
	if err != nil {
		return errno(err)
	}
	if target == nil {
		return syscall.ENOENT
	}
	*path = target
	m.symlinks.Store(inode, target)
	return 0
}

func (m *baseMeta) Open(ctx Context, inode Ino, flags uint32, attr *Attr) syscall.Errno {
	if m.conf.ReadOnly && flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0 {
		return syscall.EROFS
	}
	if m.conf.OpenCache > 0 && m.of.OpenCheck(inode, attr) {
		return 0
	}
	var err syscall.Errno
	if attr != nil && !attr.Full {
		err = m.GetAttr(ctx, inode, attr)
	}
	if err == 0 {
		m.of.Open(inode, attr)
	}
	return err
}

func (m *baseMeta) InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno {
	m.of.InvalidateChunk(inode, indx)
	return 0
}

func (m *baseMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	m.freeMu.Lock()
	defer m.freeMu.Unlock()
	if m.freeChunks.next >= m.freeChunks.maxid {
		v, err := m.incrCounter("nextChunk", 1000)
		if err != nil {
			return errno(err)
		}
		m.freeChunks.next = uint64(v) - 1000
		m.freeChunks.maxid = uint64(v)
	}
	*chunkid = m.freeChunks.next
	m.freeChunks.next++
	return 0
}

func (m *baseMeta) Close(ctx Context, inode Ino) syscall.Errno {
	if m.of.Close(inode) {
		m.Lock()
		defer m.Unlock()
		if m.removedFiles[inode] {
			delete(m.removedFiles, inode)
			go m.deleteInode(inode)
		}
	}
	return 0
}

func (m *baseMeta) fileDeleted(opened bool, inode Ino, length uint64) {
	if opened {
		m.Lock()
		m.removedFiles[inode] = true
		m.Unlock()
	} else {
		go m.deleteFile(inode, length)
	}
}

func (m *baseMeta) deleteSlice(chunkid uint64, size uint32) {
	if m.conf.MaxDeletes == 0 {
		return
	}
	m.deleting <- 1
	defer func() { <-m.deleting }()
	err := m.newMsg(DeleteChunk, chunkid, size)
	if err != nil {
		logger.Warnf("delete chunk %d (%d bytes): %s", chunkid, size, err)
	} else {
		err := m.deleteSliceDB(chunkid, size)
		if err != nil {
			logger.Errorf("delete slice %d: %s", chunkid, err)
		}
	}
}
