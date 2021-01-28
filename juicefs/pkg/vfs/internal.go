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
	"os"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

const (
	minInternalNode = 0x7FFFFFFFFFFFF0
	logInode        = minInternalNode + 1
)

type internalNode struct {
	inode Ino
	name  string
	attr  *Attr
}

var internalNodes = []*internalNode{
	{logInode, ".accesslog", &Attr{Mode: 0400}},
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
	}
}

func IsSpecialNode(ino Ino) bool {
	return ino >= minInternalNode
}

func isSpecialName(name string) bool {
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
