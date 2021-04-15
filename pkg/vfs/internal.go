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
	"bytes"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

const (
	minInternalNode = 0x7FFFFFFFFFFFF0
	logInode        = minInternalNode + 1
	controlInode    = minInternalNode + 2
)

type internalNode struct {
	inode Ino
	name  string
	attr  *Attr
}

var internalNodes = []*internalNode{
	{logInode, ".accesslog", &Attr{Mode: 0400}},
	{controlInode, ".control", &Attr{Mode: 0666}},
}

func init() {
	uid := uint32(os.Getuid())
	gid := uint32(os.Getgid())
	now := time.Now().Unix()
	for _, v := range internalNodes {
		v.attr.Typ = meta.TypeFile
		v.attr.Uid = uid
		v.attr.Gid = gid
		v.attr.Atime = now
		v.attr.Mtime = now
		v.attr.Ctime = now
		v.attr.Nlink = 1
		v.attr.Full = true
	}
}

func IsSpecialNode(ino Ino) bool {
	return ino >= minInternalNode
}

func IsSpecialName(name string) bool {
	if name[0] != '.' {
		return false
	}
	for _, n := range internalNodes {
		if name == n.name {
			return true
		}
	}
	return false
}

func getInternalNode(ino Ino) *internalNode {
	for _, n := range internalNodes {
		if ino == n.inode {
			return n
		}
	}
	return nil
}

func GetInternalNodeByName(name string) (Ino, *Attr) {
	n := getInternalNodeByName(name)
	if n != nil {
		return n.inode, n.attr
	}
	return 0, nil
}

func getInternalNodeByName(name string) *internalNode {
	if name[0] != '.' {
		return nil
	}
	for _, n := range internalNodes {
		if name == n.name {
			return n
		}
	}
	return nil
}

func handleInternalMsg(ctx Context, msg []byte) []byte {
	r := utils.ReadBuffer(msg)
	cmd := r.Get32()
	size := int(r.Get32())
	if r.Left() != int(size) {
		logger.Warnf("broken message: %d %d != %d", cmd, size, r.Left())
		return []byte{uint8(syscall.EIO & 0xff)}
	}
	switch cmd {
	case meta.Rmr:
		inode := Ino(r.Get64())
		name := string(r.Get(int(r.Get8())))
		r := m.Rmr(ctx, inode, name)
		return []byte{uint8(r)}
	case meta.Info:
		var summary meta.Summary
		inode := Ino(r.Get64())

		wb := utils.NewBuffer(4)
		r := m.Summary(ctx, inode, &summary)
		if r != 0 {
			msg := r.Error()
			wb.Put32(uint32(len(msg)))
			return append(wb.Bytes(), []byte(msg)...)
		}
		var w = bytes.NewBuffer(nil)
		fmt.Fprintf(w, " inode: %d\n", inode)
		fmt.Fprintf(w, " files:\t%d\n", summary.Files)
		fmt.Fprintf(w, " dirs:\t%d\n", summary.Dirs)
		fmt.Fprintf(w, " length:\t%d\n", summary.Length)
		fmt.Fprintf(w, " size:\t%d\n", summary.Size)

		if summary.Files == 1 && summary.Dirs == 0 {
			fmt.Fprintf(w, " chunks:\n")
			for indx := uint64(0); indx*meta.ChunkSize < summary.Length; indx++ {
				var cs []meta.Slice
				_ = m.Read(ctx, inode, uint32(indx), &cs)
				for _, c := range cs {
					fmt.Fprintf(w, "\t%d:\t%d\t%d\t%d\t%d\n", indx, c.Chunkid, c.Size, c.Off, c.Len)
				}
			}
		}
		wb.Put32(uint32(w.Len()))
		return append(wb.Bytes(), w.Bytes()...)
	default:
		logger.Warnf("unknown message type: %d", cmd)
		return []byte{uint8(syscall.EINVAL & 0xff)}
	}
}
