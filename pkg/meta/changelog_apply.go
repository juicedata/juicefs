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

type changelogApplyStateKey struct{}

type changelogApplyState struct {
	Time    time.Time
	Inode   Ino
	Trash   Ino          // Source trash directory; zero skips trash.
	Sid     uint64       // Source session ID for sustained inodes.
	Parents map[Ino]bool // Whether the source updated each parent directory.
	Opened  map[Ino]bool // Whether each inode was open on the source when removed.
}

func getChangelogApplyState(ctx Context) *changelogApplyState {
	s, _ := ctx.Value(changelogApplyStateKey{}).(*changelogApplyState)
	return s
}

func isApplyMode(ctx Context) bool {
	return getChangelogApplyState(ctx) != nil
}

func operationTime(ctx Context) time.Time {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.Time
	}
	return time.Now()
}

func applyParent(ctx Context, parent Ino, update bool) bool {
	if s := getChangelogApplyState(ctx); s != nil {
		if v, ok := s.Parents[parent]; ok {
			return v
		}
	}
	return update
}

func (m *baseMeta) sessionID(ctx Context) uint64 {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.Sid
	}
	return m.sid
}

func (m *baseMeta) isOpen(ctx Context, inode Ino) bool {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.Opened[inode]
	}
	return m.of.IsOpen(inode)
}
