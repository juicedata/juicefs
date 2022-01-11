//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	Total  uint64
	Avail  uint64
	Files  uint64
	Favail uint64
}

func (v *VFS) StatFS(ctx Context, ino Ino) (st *Statfs, err syscall.Errno) {
	var totalspace, availspace, iused, iavail uint64
	_ = v.Meta.StatFS(ctx, &totalspace, &availspace, &iused, &iavail)
	st = new(Statfs)
	st.Total = totalspace
	st.Avail = availspace
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

func (v *VFS) Access(ctx Context, ino Ino, mask int) (err syscall.Errno) {
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

	err = v.Meta.Access(ctx, ino, uint8(mmask), nil)
	return
}

func setattrStr(set int, mode, uid, gid uint32, atime, mtime int64, size uint64) string {
	var sb strings.Builder
	if set&meta.SetAttrMode != 0 {
		sb.WriteString(fmt.Sprintf("mode=%s:0%04o,", smode(uint16(mode)), mode&07777))
	}
	if set&meta.SetAttrUID != 0 {
		sb.WriteString(fmt.Sprintf("uid=%d,", uid))
	}
	if set&meta.SetAttrGID != 0 {
		sb.WriteString(fmt.Sprintf("gid=%d,", gid))
	}

	var atimeStr string
	if set&meta.SetAttrAtimeNow != 0 || (set&meta.SetAttrAtime) != 0 && atime < 0 {
		atimeStr = "NOW"
	} else if set&meta.SetAttrAtime != 0 {
		atimeStr = strconv.FormatInt(atime, 10)
	}
	if atimeStr != "" {
		sb.WriteString("atime=" + atimeStr + ",")
	}

	var mtimeStr string
	if set&meta.SetAttrMtimeNow != 0 || (set&meta.SetAttrMtime) != 0 && mtime < 0 {
		mtimeStr = "NOW"
	} else if set&meta.SetAttrMtime != 0 {
		mtimeStr = strconv.FormatInt(mtime, 10)
	}
	if mtimeStr != "" {
		sb.WriteString("mtime=" + mtimeStr + ",")
	}

	if set&meta.SetAttrSize != 0 {
		sizeStr := strconv.FormatUint(size, 10)
		sb.WriteString("size=" + sizeStr + ",")
	}
	r := sb.String()
	if len(r) > 1 {
		r = r[:len(r)-1] // drop last ,
	}
	return r
}

func (v *VFS) SetAttr(ctx Context, ino Ino, set int, opened uint8, mode, uid, gid uint32, atime, mtime int64, atimensec, mtimensec uint32, size uint64) (entry *meta.Entry, err syscall.Errno) {
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
	if set&meta.SetAttrSize != 0 {
		err = v.Truncate(ctx, ino, int64(size), opened, attr)
		if err != 0 {
			return
		}
	}
	if set&meta.SetAttrMode != 0 {
		attr.Mode = uint16(mode & 07777)
	}
	if set&meta.SetAttrUID != 0 {
		attr.Uid = uid
	}
	if set&meta.SetAttrGID != 0 {
		attr.Gid = gid
	}
	if set&meta.SetAttrAtime != 0 {
		attr.Atime = atime
		attr.Atimensec = atimensec
	}
	if set&meta.SetAttrMtime != 0 {
		attr.Mtime = mtime
		attr.Mtimensec = mtimensec
	}
	err = v.Meta.SetAttr(ctx, ino, uint16(set), 0, attr)
	if err == 0 {
		v.UpdateLength(ino, attr)
		entry = &meta.Entry{Inode: ino, Attr: attr}
	}
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

func (v *VFS) Getlk(ctx Context, ino Ino, fh uint64, owner uint64, start, len *uint64, typ *uint32, pid *uint32) (err syscall.Errno) {
	logit(ctx, "getlk (%d,%016X): %s (%d,%d,%s,%d)", ino, owner, strerr(err), *start, *len, lockType(*typ), *pid)
	if lockType(*typ).String() == "X" {
		return syscall.EINVAL
	}
	if IsSpecialNode(ino) {
		err = syscall.EPERM
		return
	}
	if v.findHandle(ino, fh) == nil {
		err = syscall.EBADF
		return
	}
	err = v.Meta.Getlk(ctx, ino, owner, typ, start, len, pid)
	return
}

func (v *VFS) Setlk(ctx Context, ino Ino, fh uint64, owner uint64, start, end uint64, typ uint32, pid uint32, block bool) (err syscall.Errno) {
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
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.addOp(ctx)
	defer h.removeOp(ctx)

	err = v.Meta.Setlk(ctx, ino, owner, block, typ, start, end, pid)
	if err == 0 {
		h.Lock()
		if typ != syscall.F_UNLCK {
			h.locks |= 2
		}
		h.Unlock()
	}
	return
}

func (v *VFS) Flock(ctx Context, ino Ino, fh uint64, owner uint64, typ uint32, block bool) (err syscall.Errno) {
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
	h := v.findHandle(ino, fh)
	if h == nil {
		err = syscall.EBADF
		return
	}
	h.addOp(ctx)
	defer h.removeOp(ctx)
	err = v.Meta.Flock(ctx, ino, owner, typ, block)
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
