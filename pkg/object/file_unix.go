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
		} else {
			logger.Warnf("lookup uid %d: %s", uid, err)
			name = strconv.Itoa(uid)
		}
		uids[uid] = name
	}
	return name
}

func groupName(gid int) string {
	name, ok := gids[gid]
	if !ok {
		if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil {
			name = g.Name
		} else {
			logger.Warnf("lookup gid %d: %s", gid, err)
			name = strconv.Itoa(gid)
		}
		gids[gid] = name
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
	} else {
		if g, e := strconv.Atoi(name); e == nil {
			uid = g
		} else {
			logger.Warnf("lookup user %s: %s", name, err)
		}
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
	} else {
		if g, e := strconv.Atoi(name); e == nil {
			gid = g
		} else {
			logger.Warnf("lookup group %s: %s", name, err)
		}
	}
	groups[name] = gid
	return gid
}
