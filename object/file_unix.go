package object

import (
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"
)

var uids = make(map[int]string)
var gids = make(map[int]string)
var users = make(map[string]int)
var groups = make(map[string]int)
var mutex sync.Mutex

func getOwnerGroup(info os.FileInfo) (string, string) {
	mutex.Lock()
	defer mutex.Unlock()
	var owner, group string
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if u, ok := uids[int(st.Uid)]; ok {
			owner = u
		} else if u, err := user.LookupId(strconv.Itoa(int(st.Uid))); err == nil {
			owner = u.Username
			uids[int(st.Uid)] = owner
		}
		if g, ok := gids[int(st.Gid)]; ok {
			group = g
		} else if g, err := user.LookupGroupId(strconv.Itoa(int(st.Gid))); err == nil {
			group = g.Name
			gids[int(st.Gid)] = group
		}
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
