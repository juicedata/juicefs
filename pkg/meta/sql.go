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
	"sync"
	"syscall"
	"time"

	"xorm.io/xorm"
)

// DBConfig is config for Redis client.
type DBConfig struct {
	Strict  bool // update ctime
	Retries int
}

type setting struct {
	Name  string
	Value string `xorm:"varchar(4096)"`
}

type counter struct {
	Name  string
	Value uint64
}

type edge struct {
	Parent uint64
	Name   string
	Inode  uint64
	Type   uint8
}

type node struct {
	Inode  uint64
	Type   uint8
	Flags  uint8
	Mode   uint16
	Uid    uint32
	Gid    uint32
	Atime  time.Time
	Mtime  time.Time
	Ctime  time.Time
	Nlink  uint32
	Length uint64
	Rdev   uint64
	Parent uint64
}

type chunk struct {
	Inode   uint64
	Indx    uint32
	Pos     uint32
	Chunkid uint64
	Size    uint32
	Off     uint32
	Len     uint32
}

type symlink struct {
	Inode  uint64
	Target string `xorm:"varchar(4096)"`
}

type xattr struct {
	Inode uint64
	Name  string `xorm:"varchar(256)"`
	Value []byte `xorm:"varchar(4096)"`
}

type session struct {
	sid       uint64
	heartbeta time.Time
}

type delchunk struct {
	inode  uint64
	start  uint64
	end    uint64
	maxid  uint64
	expire time.Time
}

type flock struct {
	inode uint64
	sid   uint64
	owner uint64
	ltype uint8
}

type Plock struct {
	inode uint64
	sid   uint64
	owner uint64
	pid   uint32
	ltype uint8
	start uint64
	end   uint64
}

type dbMeta struct {
	sync.Mutex
	conf   *DBConfig
	engine *xorm.Engine

	sid          int64
	openFiles    map[Ino]int
	removedFiles map[Ino]bool
	compacting   map[uint64]bool
	msgCallbacks *msgCallbacks
}

func NewSQLMeta(driver, dsn string) (*dbMeta, error) {
	engine, err := xorm.NewEngine(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to use data source %s: %s", driver, err)
	}

	m := &dbMeta{
		engine:       engine,
		openFiles:    make(map[Ino]int),
		removedFiles: make(map[Ino]bool),
		compacting:   make(map[uint64]bool),
		msgCallbacks: &msgCallbacks{
			callbacks: make(map[uint32]MsgCallback),
		},
	}
	// TODO sid
	return m, nil
}

func (m *dbMeta) Init(format Format, force bool) error {
	_ = m.engine.Sync2(new(setting))
	_ = m.engine.Sync2(new(counter))
	_ = m.engine.Sync2(new(node))
	_ = m.engine.Sync2(new(node))
	_ = m.engine.Sync2(new(edge))
	_ = m.engine.Sync2(new(symlink))
	_ = m.engine.Sync2(new(xattr))
	_ = m.engine.Sync2(new(flock))
	_ = m.engine.Sync2(new(plock))

	// values, err := m.engine.QueryString("select * from setting")

	// body, err := m.rdb.Get(c, "setting").Bytes()
	// if err != nil && err != redis.Nil {
	// 	return err
	// }

	// data, err := json.MarshalIndent(format, "", "")
	// if err != nil {
	// 	loggem.Fatalf("json: %s", err)
	// }
	// err = m.rdb.Set(c, "setting", data, 0).Err()
	// if err != nil {
	// 	return err
	// }

	// root inode
	// var attr Attr
	// attm.Flags = 0
	// attm.Typ = TypeDirectory
	// attm.Mode = 0777
	// attm.Uid = 0
	// attm.Uid = 0
	// ts := time.Now().Unix()
	// attm.Atime = ts
	// attm.Mtime = ts
	// attm.Ctime = ts
	// attm.Nlink = 2
	// attm.Length = 4 << 10
	// attm.Rdev = 0
	// attm.Parent = 1
	// m.rdb.Set(c, m.inodeKey(1), m.marshal(&attr), 0)
	return nil
}

func (m *dbMeta) Load() (*Format, error) {
	// body, err := m.rdb.Get(c, "setting").Bytes()
	// if err == redis.Nil {
	// 	return nil, fmt.Errorf("no volume found")
	// }
	// if err != nil {
	// 	return nil, err
	// }
	// var format Format
	// err = json.Unmarshal(body, &format)
	// if err != nil {
	// 	return nil, fmt.Errorf("json: %s", err)
	// }
	// return &format, nil
}

func (m *dbMeta) OnMsg(mtype uint32, cb MsgCallback) {
	m.msgCallbacks.Lock()
	defer m.msgCallbacks.Unlock()
	m.msgCallbacks.callbacks[mtype] = cb
}

func (m *dbMeta) newMsg(mid uint32, args ...interface{}) error {
	m.msgCallbacks.Lock()
	cb, ok := m.msgCallbacks.callbacks[mid]
	m.msgCallbacks.Unlock()
	if ok {
		return cb(args...)
	}
	return fmt.Errorf("message %d is not supported", mid)
}

func (m *dbMeta) nextInode() (Ino, error) {
	m.engine.Exec("update counter set nextinode=nextinode+1")
	return Ino(0), nil
}

func (m *dbMeta) StatFS(ctx Context, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	// *totalspace = 1 << 50
	// used, _ := m.rdb.IncrBy(c, usedSpace, 0).Result()
	// used = ((used >> 16) + 1) << 16 // aligned to 64K
	// *availspace = *totalspace - uint64(used)
	// inodes, _ := m.rdb.IncrBy(c, totalInodes, 0).Result()
	// *iused = uint64(inodes)
	// *iavail = 10 << 20
	return 0
}

func (m *dbMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Access(ctx Context, inode Ino, modemask uint16) syscall.Errno {
	return 0 // handled by kernel
}

func (m *dbMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Truncate(ctx Context, inode Ino, flags uint8, length uint64, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64) syscall.Errno {
	return 0
}

func (m *dbMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) ReadLink(ctx Context, inode Ino, path *[]byte) syscall.Errno {
	return 0
}

func (m *dbMeta) Symlink(ctx Context, parent Ino, name string, path string, inode *Ino, attr *Attr) syscall.Errno {
	return m.mknod(ctx, parent, name, TypeSymlink, 0644, 022, 0, path, inode, attr)
}

func (m *dbMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, inode *Ino, attr *Attr) syscall.Errno {
	return m.mknod(ctx, parent, name, _type, mode, cumask, rdev, "", inode, attr)
}

func (m *dbMeta) mknod(ctx Context, parent Ino, name string, _type uint8, mode, cumask uint16, rdev uint32, path string, inode *Ino, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	return m.Mknod(ctx, parent, name, TypeDirectory, mode, cumask, 0, inode, attr)
}

func (m *dbMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, inode *Ino, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Rmdir(ctx Context, parent Ino, name string) syscall.Errno {
	return 0
}

func (m *dbMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, inode *Ino, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Link(ctx Context, inode, parent Ino, name string, attr *Attr) syscall.Errno {
	return 0
}

func (m *dbMeta) Readdir(ctx Context, inode Ino, plus uint8, entries *[]*Entry) syscall.Errno {
	return 0
}

func (m *dbMeta) Open(ctx Context, inode Ino, flags uint8, attr *Attr) syscall.Errno {
	var err syscall.Errno
	if attr != nil {
		err = m.GetAttr(ctx, inode, attr)
	}
	if err == 0 {
		m.Lock()
		m.openFiles[inode] = m.openFiles[inode] + 1
		m.Unlock()
	}
	return 0
}

func (m *dbMeta) Close(ctx Context, inode Ino) syscall.Errno {
	return 0
}

func (m *dbMeta) Read(ctx Context, inode Ino, indx uint32, chunks *[]Slice) syscall.Errno {
	return 0
}

func (m *dbMeta) NewChunk(ctx Context, inode Ino, indx uint32, offset uint32, chunkid *uint64) syscall.Errno {
	// cid, err := m.rdb.Incr(c, "nextchunk").Uint64()
	// if err == nil {
	// 	*chunkid = cid
	// }
	// return errno(err)
	return 0
}

func (m *dbMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *dbMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) ListXattr(ctx Context, inode Ino, names *[]byte) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) SetXattr(ctx Context, inode Ino, name string, value []byte) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	return syscall.ENOSYS
}

func (m *dbMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	return syscall.ENOSYS
}
