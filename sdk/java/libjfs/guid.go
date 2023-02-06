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

package main

import (
	"crypto/md5"
	"encoding/binary"
	"os/user"
	"strconv"
	"sync"
)

type pwent struct {
	id   uint32
	name string
}

type mapping struct {
	sync.Mutex
	salt      string
	local     bool
	usernames map[string]uint32
	userIDs   map[uint32]string
	groups    map[string]uint32
	groupIDs  map[uint32]string
}

func newMapping(salt string) *mapping {
	m := &mapping{
		salt:      salt,
		usernames: make(map[string]uint32),
		userIDs:   make(map[uint32]string),
		groups:    make(map[string]uint32),
		groupIDs:  make(map[uint32]string),
	}
	m.update(genAllUids(), genAllGids(), true)
	return m
}

func (m *mapping) genGuid(name string) uint32 {
	digest := md5.Sum([]byte(m.salt + name + m.salt))
	a := binary.LittleEndian.Uint64(digest[0:8])
	b := binary.LittleEndian.Uint64(digest[8:16])
	return uint32(a ^ b)
}

func (m *mapping) lookupUser(name string) uint32 {
	m.Lock()
	defer m.Unlock()
	var id uint32
	if id, ok := m.usernames[name]; ok {
		return id
	}
	if !m.local {
		return m.genGuid(name)
	}
	if name == "root" { // root in hdfs sdk is a normal user
		id = m.genGuid(name)
	} else {
		u, _ := user.Lookup(name)
		if u != nil {
			id_, _ := strconv.ParseUint(u.Uid, 10, 32)
			id = uint32(id_)
		} else {
			id = m.genGuid(name)
		}
	}
	m.usernames[name] = id
	m.userIDs[id] = name
	return id
}

func (m *mapping) lookupGroup(name string) uint32 {
	m.Lock()
	defer m.Unlock()
	var id uint32
	if id, ok := m.groups[name]; ok {
		return id
	}
	if !m.local {
		return m.genGuid(name)
	}
	if name == "root" {
		id = m.genGuid(name)
	} else {
		g, _ := user.LookupGroup(name)
		if g == nil {
			id = m.genGuid(name)
		} else {
			id_, _ := strconv.ParseUint(g.Gid, 10, 32)
			id = uint32(id_)
		}
	}
	m.groups[name] = id
	m.groupIDs[id] = name
	return 0
}

func (m *mapping) lookupUserID(id uint32) string {
	m.Lock()
	defer m.Unlock()
	if name, ok := m.userIDs[id]; ok {
		return name
	}
	if !m.local {
		return strconv.Itoa(int(id))
	}
	u, _ := user.LookupId(strconv.Itoa(int(id)))
	if u == nil {
		u = &user.User{Username: strconv.Itoa(int(id))}
	}
	name := u.Username
	if len(name) > 49 {
		name = name[:49]
	}
	m.usernames[name] = id
	m.userIDs[id] = name
	return name
}

func (m *mapping) lookupGroupID(id uint32) string {
	m.Lock()
	defer m.Unlock()
	if name, ok := m.groupIDs[id]; ok {
		return name
	}
	if !m.local {
		return strconv.Itoa(int(id))
	}
	g, _ := user.LookupGroupId(strconv.Itoa(int(id)))
	if g == nil {
		g = &user.Group{Name: strconv.Itoa(int(id))}
	}
	name := g.Name
	if len(name) > 49 {
		name = name[:49]
	}
	m.groups[name] = id
	m.groupIDs[id] = name
	return name
}

func (m *mapping) update(uids []pwent, gids []pwent, local bool) {
	m.Lock()
	defer m.Unlock()
	m.local = local
	for _, u := range uids {
		oldId := m.usernames[u.name]
		oldName := m.userIDs[u.id]
		delete(m.userIDs, oldId)
		delete(m.usernames, oldName)
		m.usernames[u.name] = u.id
		m.userIDs[u.id] = u.name
	}
	for _, g := range gids {
		oldId := m.groups[g.name]
		oldName := m.groupIDs[g.id]
		delete(m.groupIDs, oldId)
		delete(m.groups, oldName)
		m.groups[g.name] = g.id
		m.groupIDs[g.id] = g.name
	}
}
