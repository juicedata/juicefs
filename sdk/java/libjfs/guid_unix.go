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

package main

// #include <pwd.h>
// #include <grp.h>
import "C"
import (
	"sync"
)

// protect getpwent and getgrent
var cgoMutex sync.Mutex

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
