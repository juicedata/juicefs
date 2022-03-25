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
			uids = append(uids, pwent{uint32(p.pw_uid), name})
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
			gids = append(gids, pwent{uint32(p.gr_gid), name})
		}
	}
	return gids
}
