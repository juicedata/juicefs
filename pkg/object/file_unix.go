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

package object

import (
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"github.com/pkg/sftp"
)

var uids = make(map[int]string)
var gids = make(map[int]string)
var users = make(map[string]int)
var groups = make(map[string]int)
var mutex sync.Mutex

func userName(uid int) string {
	name, ok := uids[uid]
	if !ok {
		if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
			name = u.Username
			uids[uid] = name
		}
	}
	return name
}

func groupName(gid int) string {
	name, ok := gids[gid]
	if !ok {
		if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil {
			name = g.Name
			gids[gid] = name
		}
	}
	return name
}

func getOwnerGroup(info os.FileInfo) (string, string) {
	mutex.Lock()
	defer mutex.Unlock()
	var owner, group string
	switch st := info.Sys().(type) {
	case *syscall.Stat_t:
		owner = userName(int(st.Uid))
		group = groupName(int(st.Gid))
	case *sftp.FileStat:
		owner = userName(int(st.UID))
		group = groupName(int(st.GID))
	}
	return owner, group
}

func lookupUser(name string) int {
	mutex.Lock()
	defer mutex.Unlock()
	if u, ok := users[name]; ok {
		return u
	}
	var uid = -1
	if u, err := user.Lookup(name); err == nil {
		uid, _ = strconv.Atoi(u.Uid)
	}
	users[name] = uid
	return uid
}

func lookupGroup(name string) int {
	mutex.Lock()
	defer mutex.Unlock()
	if u, ok := groups[name]; ok {
		return u
	}
	var gid = -1
	if u, err := user.LookupGroup(name); err == nil {
		gid, _ = strconv.Atoi(u.Gid)
	}
	groups[name] = gid
	return gid
}
