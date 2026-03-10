/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

// Statistic contains the statistics of the filesystem
type Statistic struct {
	UsedSpace                uint64
	AvailableSpace           uint64
	UsedInodes               uint64
	AvailableInodes          uint64
	TrashFileCount           int64 `json:",omitempty"`
	TrashFileSize            int64 `json:",omitempty"`
	PendingDeletedFileCount  int64 `json:",omitempty"`
	PendingDeletedFileSize   int64 `json:",omitempty"`
	TrashSliceCount          int64 `json:",omitempty"`
	TrashSliceSize           int64 `json:",omitempty"`
	PendingDeletedSliceCount int64 `json:",omitempty"`
	PendingDeletedSliceSize  int64 `json:",omitempty"`
}

type Sections struct {
	Setting  *Format
	Sessions []*Session
	Stat     *Statistic
}

// Status retrieves the status of the filesystem
func Status(ctx context.Context, m Meta, trash bool, sections *Sections) error {
	format, err := m.Load(true)
	if err != nil {
		return fmt.Errorf("load setting: %v", err)
	}
	format.RemoveSecret()

	sessions, err := m.ListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %v", err)
	}

	stat := &Statistic{}
	var totalSpace uint64
	if err = m.StatFS(Background(), RootInode, &totalSpace, &stat.AvailableSpace, &stat.UsedInodes, &stat.AvailableInodes); err != syscall.Errno(0) {
		return fmt.Errorf("stat fs: %v", err)
	}
	stat.UsedSpace = totalSpace - stat.AvailableSpace

	if trash {
		progress := utils.NewProgress(false)
		trashFileSpinner := progress.AddDoubleSpinner("Trash Files")
		pendingDeletedFileSpinner := progress.AddDoubleSpinner("Pending Deleted Files")
		trashSlicesSpinner := progress.AddDoubleSpinner("Trash Slices")
		pendingDeletedSlicesSpinner := progress.AddDoubleSpinner("Pending Deleted Slices")
		err = m.ScanDeletedObject(
			WrapContext(ctx),
			func(ss []Slice, _ int64) (bool, error) {
				for _, s := range ss {
					trashSlicesSpinner.IncrInt64(int64(s.Size))
				}
				return false, nil
			},
			func(_ uint64, size uint32) (bool, error) {
				pendingDeletedSlicesSpinner.IncrInt64(int64(size))
				return false, nil
			},
			func(_ Ino, size uint64, _ time.Time) (bool, error) {
				trashFileSpinner.IncrInt64(int64(size))
				return false, nil
			},
			func(_ Ino, size uint64, _ int64) (bool, error) {
				pendingDeletedFileSpinner.IncrInt64(int64(size))
				return false, nil
			},
		)
		if err != nil {
			return fmt.Errorf("statistic: %v", err)
		}

		trashSlicesSpinner.Done()
		pendingDeletedSlicesSpinner.Done()
		trashFileSpinner.Done()
		pendingDeletedFileSpinner.Done()
		progress.Done()
		stat.TrashSliceCount, stat.TrashSliceSize = trashSlicesSpinner.Current()
		stat.PendingDeletedSliceCount, stat.PendingDeletedSliceSize = pendingDeletedSlicesSpinner.Current()
		stat.TrashFileCount, stat.TrashFileSize = trashFileSpinner.Current()
		stat.PendingDeletedFileCount, stat.PendingDeletedFileSize = pendingDeletedFileSpinner.Current()
	}

	if sections != nil {
		sections.Setting = format
		sections.Sessions = sessions
		sections.Stat = stat
	}
	return nil
}
