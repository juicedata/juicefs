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

package main

// #include <pwd.h>
// #include <grp.h>
import "C"
import (
	"crypto/md5"
	"encoding/binary"
	"os/user"
	"strconv"
	"sync"
)

// protect getpwent and getgrent
var cgoMutex sync.Mutex

type pwent struct {
	id   int
	name string
}

type sortPwent []pwent

func (s sortPwent) Len() int      { return len(s) }
func (s sortPwent) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s sortPwent) Less(i, j int) bool {
	if s[i].id == s[j].id {
		return s[i].name < s[j].name
	}
	return s[i].id < s[j].id
}

func genAllUids() []pwent {
	cgoMutex.Lock()
	defer cgoMutex.Unlock()
	C.setpwent()
	defer C.endpwent()
	var uids []pwent
	for {
		p := C.getpwent()
		if p == nil {
			break
		}
		name := C.GoString(p.pw_name)
		if name != "root" {
			uids = append(uids, pwent{int(p.pw_uid), name})
		}
	}
	return uids
}

func genAllGids() []pwent {
	cgoMutex.Lock()
	defer cgoMutex.Unlock()
	C.setgrent()
	defer C.endgrent()
	var gids []pwent
	for {
		p := C.getgrent()
		if p == nil {
			break
		}
		name := C.GoString(p.gr_name)
		if name != "root" {
			gids = append(gids, pwent{int(p.gr_gid), name})
		}
	}
	return gids
}

type mapping struct {
	sync.Mutex
	salt      string
	usernames map[string]int
	userIDs   map[int]string
	groups    map[string]int
	groupIDs  map[int]string
}

func newMapping(salt string) *mapping {
	m := &mapping{
		salt:      salt,
		usernames: make(map[string]int),
		userIDs:   make(map[int]string),
		groups:    make(map[string]int),
		groupIDs:  make(map[int]string),
	}
	for _, u := range genAllUids() {
		m.usernames[u.name] = u.id
		m.userIDs[u.id] = u.name
	}
	for _, g := range genAllGids() {
		m.groups[g.name] = g.id
		m.groupIDs[g.id] = g.name
	}
	return m
}

func (m *mapping) genGuid(name string) int {
	dig := md5.New()
	dig.Write([]byte(m.salt))
	dig.Write([]byte(name))
	dig.Write([]byte(m.salt))
	digest := dig.Sum(nil)
	a := binary.LittleEndian.Uint64(digest[0:8])
	b := binary.LittleEndian.Uint64(digest[8:16])
	return int((a ^ b))
}

func (m *mapping) lookupUser(name string) int {
	m.Lock()
	defer m.Lock()
	var id int
	if id, ok := m.usernames[name]; ok {
		return id
	}
	u, _ := user.Lookup(name)
	if u != nil {
		id, _ = strconv.Atoi(u.Uid)
	} else {
		id = m.genGuid(name)
	}
	m.usernames[name] = id
	m.userIDs[id] = name
	return id
}

func (m *mapping) lookupGroup(name string) int {
	m.Lock()
	defer m.Lock()
	var id int
	if id, ok := m.groups[name]; ok {
		return id
	}
	g, _ := user.LookupGroup(name)
	if g == nil {
		id = m.genGuid(name)
	} else {
		id, _ = strconv.Atoi(g.Gid)
	}
	m.groups[name] = id
	m.groupIDs[id] = name
	return 0
}

func (m *mapping) lookupUserID(id int) string {
	m.Lock()
	defer m.Lock()
	if name, ok := m.userIDs[id]; ok {
		return name
	}
	u, _ := user.LookupId(strconv.Itoa(id))
	if u == nil {
		u = &user.User{Username: strconv.Itoa(id)}
	}
	name := u.Username
	if len(name) > 49 {
		name = name[:49]
	}
	m.usernames[name] = id
	m.userIDs[id] = name
	return name
}

func (m *mapping) lookupGroupID(id int) string {
	m.Lock()
	defer m.Lock()
	if name, ok := m.groupIDs[id]; ok {
		return name
	}
	g, _ := user.LookupGroupId(strconv.Itoa(id))
	if g == nil {
		g = &user.Group{Name: strconv.Itoa(id)}
	}
	name := g.Name
	if len(name) > 49 {
		name = name[:49]
	}
	m.groups[name] = id
	m.groupIDs[id] = name
	return name
}
