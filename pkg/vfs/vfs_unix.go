// +build !windows

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

package vfs

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"

	"golang.org/x/sys/unix"
)

const O_ACCMODE = syscall.O_ACCMODE
const F_UNLCK = syscall.F_UNLCK

type Statfs struct {
	Bsize  uint32
	Blocks uint64
	Bavail uint64
	Files  uint64
	Favail uint64
}

func StatFS(ctx Context, ino Ino) (st *Statfs, err int) {
	var totalspace, availspace, iused, iavail uint64
	_ = m.StatFS(ctx, &totalspace, &availspace, &iused, &iavail)
	var bsize uint64 = 4096
	blocks := totalspace / bsize
	bavail := blocks - (totalspace-availspace+bsize-1)/bsize

	st = new(Statfs)
	st.Bsize = uint32(bsize)
	st.Blocks = blocks
	st.Bavail = bavail
	st.Files = iused + iavail
	st.Favail = iavail
	logit(ctx, "statfs (%d): OK (%d,%d,%d,%d)", ino, totalspace-availspace, availspace, iused, iavail)
	return
}

func accessTest(attr *Attr, mmode uint16, uid uint32, gid uint32) syscall.Errno {
	if uid == 0 {
		return 0
	}
	mode := attr.Mode
	var effected uint16
	if uid == attr.Uid {
		effected = (mode >> 6) & 7
	} else {
		effected = mode & 7
		if gid == attr.Gid {
			effected = (mode >> 3) & 7
		}
	}
	if mmode&effected != mmode {
		return syscall.EACCES
	}
	return 0
}

func Access(ctx Context, ino Ino, mask int) (err syscall.Errno) {
	defer func() { logit(ctx, "access (%d,0x%X): %s", ino, mask, strerr(err)) }()
	var mmask uint16
	if mask&unix.R_OK != 0 {
		mmask |= MODE_MASK_R
	}
	if mask&unix.W_OK != 0 {
		mmask |= MODE_MASK_W
	}
	if mask&unix.X_OK != 0 {
		mmask |= MODE_MASK_X
	}
	if IsSpecialNode(ino) {
		node := getInternalNode(ino)
		err = accessTest(node.attr, mmask, ctx.Uid(), ctx.Gid())
		return
	}

	err = m.Access(ctx, ino, uint8(mmask), nil)
	return
}

func setattrStr(set int, mode, uid, gid uint32, atime, mtime int64, size uint64) string {
	var sb strings.Builder
	if set&meta.SetAttrMode != 0 {
		sb.WriteString(fmt.Sprintf("mode=%s:0%04o;", smode(uint16(mode)), mode&07777))
	}
	if set&meta.SetAttrUID != 0 {
		sb.WriteString(fmt.Sprintf("uid=%d;", uid))
	}
	if set&meta.SetAttrGID != 0 {
		sb.WriteString(fmt.Sprintf("gid=%d;", gid))
	}

	var atimeStr string
	if (set&meta.SetAttrAtime) != 0 && atime < 0 {
		atimeStr = "NOW"
	} else if set&meta.SetAttrAtime != 0 {
		atimeStr = strconv.FormatInt(atime, 10)
	}
	if atimeStr != "" {
		sb.WriteString("atime=" + atimeStr + ";")
	}

	var mtimeStr string
	if (set&meta.SetAttrMtime) != 0 && mtime < 0 {
		mtimeStr = "NOW"
	} else if set&meta.SetAttrMtime != 0 {
		mtimeStr = strconv.FormatInt(mtime, 10)
	}
	if mtimeStr != "" {
		sb.WriteString("mtime=" + mtimeStr + ";")
	}

	if (set & meta.SetAttrSize) != 0 {
		sizeStr := strconv.FormatUint(size, 10)
		sb.WriteString("size=" + sizeStr + ";")
	}
	return sb.String()
}

func SetAttr(ctx Context, ino Ino, set int, opened uint8, mode, uid, gid uint32, atime, mtime int64, atimensec, mtimensec uint32, size uint64) (entry *meta.Entry, err syscall.Errno) {
	str := setattrStr(set, mode, uid, gid, atime, mtime, size)
	defer func() {
		logit(ctx, "setattr (%d,0x%X,[%s]): %s%s", ino, set, str, strerr(err), (*Entry)(entry))
	}()
	if IsSpecialNode(ino) {
		n := getInternalNode(ino)
		entry = &meta.Entry{Inode: ino, Attr: n.attr}
		return
	}
	err = syscall.EINVAL
	var attr = &Attr{}
	if (set & (meta.SetAttrMode | meta.SetAttrUID | meta.SetAttrGID | meta.SetAttrAtime | meta.SetAttrMtime | meta.SetAttrSize)) == 0 {
		// change other flags or change nothing
		err = m.SetAttr(ctx, ino, 0, 0, attr)
		if err != 0 {
			return
		}
	}
	if (set & (meta.SetAttrMode | meta.SetAttrUID | meta.SetAttrGID | meta.SetAttrAtime | meta.SetAttrMtime | meta.SetAttrAtimeNow | meta.SetAttrMtimeNow)) != 0 {
		if (set & meta.SetAttrMode) != 0 {
			attr.Mode = uint16(mode & 07777)
		}
		if (set & meta.SetAttrUID) != 0 {
			attr.Uid = uid
		}
		if (set & meta.SetAttrGID) != 0 {
			attr.Gid = gid
		}
		if set&meta.SetAttrAtime != 0 {
			attr.Atime = atime
			attr.Atimensec = atimensec
		}
		if (set & meta.SetAttrMtime) != 0 {
			attr.Mtime = mtime
			attr.Mtimensec = mtimensec
		}
		err = m.SetAttr(ctx, ino, uint16(set), 0, attr)
		if err != 0 {
			return
		}
	}
	if set&meta.SetAttrSize != 0 {
		err = Truncate(ctx, ino, int64(size), opened, attr)
	}
	UpdateLength(ino, attr)
	entry = &meta.Entry{Inode: ino, Attr: attr}
	return
}

type lockType uint32

func (l lockType) String() string {
	switch l {
	case syscall.F_UNLCK:
		return "U"
	case syscall.F_RDLCK:
		return "R"
	case syscall.F_WRLCK:
		return "W"
	default:
		return "X"
	}
}

func Getlk(ctx Context, ino Ino, fh uint64, owner uint64, start, len *uint64, typ *uint32, pid *uint32) (err syscall.Errno) {
	logit(ctx, "getlk (%d,%016X): %s (%d,%d,%s,%d)", ino, owner, strerr(err), *start, *len, lockType(*typ), *pid)
	if lockType(*typ).String() == "X" {
		return syscall.EINVAL
	}
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	if findHandle(ino, fh) == nil {
		err = syscall.EBADF
		return
	}
	err = m.Getlk(ctx, ino, owner, typ, start, len, pid)
	return
}

func Setlk(ctx Context, ino Ino, fh uint64, owner uint64, start, end uint64, typ uint32, pid uint32, block bool) (err syscall.Errno) {
	defer func() {
		logit(ctx, "setlk (%d,%016X,%d,%d,%s,%t,%d): %s", ino, owner, start, end, lockType(typ), block, pid, strerr(err))
	}()
	if lockType(typ).String() == "X" {
		return syscall.EINVAL
	}
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	h := findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.addOp(ctx)
	defer h.removeOp(ctx)

	err = m.Setlk(ctx, ino, owner, block, typ, start, end, pid)
	if err == 0 {
		h.Lock()
		if typ != syscall.F_UNLCK {
			h.locks |= 2
		}
		h.Unlock()
	}
	return
}

func Flock(ctx Context, ino Ino, fh uint64, owner uint64, typ uint32, block bool) (err syscall.Errno) {
	var name string
	var reqid uint32
	defer func() { logit(ctx, "flock (%d,%d,%016X,%s,%t): %s", reqid, ino, owner, name, block, strerr(err)) }()
	switch typ {
	case syscall.F_RDLCK:
		name = "LOCKSH"
	case syscall.F_WRLCK:
		name = "LOCKEX"
	case syscall.F_UNLCK:
		name = "UNLOCK"
	default:
		err = syscall.EINVAL
		return
	}

	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	h := findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.addOp(ctx)
	defer h.removeOp(ctx)
	err = m.Flock(ctx, ino, owner, typ, block)
	if err == 0 {
		h.Lock()
		if typ == syscall.F_UNLCK {
			h.locks &= 2
		} else {
			h.locks |= 1
			h.flockOwner = owner
		}
		h.Unlock()
	}
	return
}
