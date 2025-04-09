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

package debugtest

import (
	"sync"
	"strings"
)

var (
	debugLock sync.Mutex
	DebugOptions map[string]int = make(map[string]int)
)

func SetDebugOption(opts string) {
	if DebugMode {
		opts = strings.TrimSuffix(strings.TrimSpace(opts), "\n")
		debugLock.Lock()
		defer debugLock.Unlock()
		if strings.HasPrefix(opts, "+") {
			opts = opts[1:]
			for _, v := range strings.Split(opts, ",") {
				if v != "" {
					DebugOptions[v] = 1
				}
			}
			return
		}
		if strings.HasPrefix(opts, "-") {
			opts = opts[1:]
			for _, v := range strings.Split(opts, ",") {
				delete(DebugOptions, v)
			}
		}
	}
}

func GetDebugOption(opt string) bool {
	if DebugMode {
	        debugLock.Lock()
        	defer debugLock.Unlock()
		_, ok := DebugOptions[opt]
		return ok
	}
	return false
}

func PrintDebugOption() string {
	var opts []string
	if DebugMode {
		debugLock.Lock()
		for k,_ := range DebugOptions {
			opts = append(opts, k)	
		}
		debugLock.Unlock()
	}
	return strings.Join(opts, ",") + "\n"
}


