/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package meta

import "time"

type applyStateKey struct{}

type applyState struct {
	Time    time.Time
	Inode   Ino
	Trash   Ino
	Sid     uint64
	Parents map[Ino]bool
	Opened  map[Ino]bool
}

func getApplyState(ctx Context) *applyState {
	s, _ := ctx.Value(applyStateKey{}).(*applyState)
	return s
}

func isApplyMode(ctx Context) bool {
	return getApplyState(ctx) != nil
}

func operationTime(ctx Context) time.Time {
	if s := getApplyState(ctx); s != nil {
		return s.Time
	}
	return time.Now()
}

func applyParent(ctx Context, parent Ino, update bool) bool {
	if s := getApplyState(ctx); s != nil {
		if v, ok := s.Parents[parent]; ok {
			return v
		}
	}
	return update
}

func (m *baseMeta) sessionID(ctx Context) uint64 {
	if s := getApplyState(ctx); s != nil {
		return s.Sid
	}
	return m.sid
}

func (m *baseMeta) isOpen(ctx Context, inode Ino) bool {
	if s := getApplyState(ctx); s != nil {
		return s.Opened[inode]
	}
	return m.of.IsOpen(inode)
}
