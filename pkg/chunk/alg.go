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

package chunk

func TwoRandomEvict(keys map[string]cacheItem, goal int64) *[]string {
	var todel []string
	var freed int64
	var cnt int
	var lastKey string
	var lastValue cacheItem
	// for each two random keys, then compare the access time, evict the older one
	for key, value := range keys {
		if cnt == 0 || lastValue.atime > value.atime {
			lastKey = key
			lastValue = value
		}
		cnt++
		if cnt > 1 {
			delete(keys, lastKey)
			freed += int64(lastValue.size + 4096)
			todel = append(todel, lastKey)
			cnt = 0
			if freed > goal {
				break
			}
		}
	}
	return &todel
}
