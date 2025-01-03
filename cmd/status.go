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

package cmd

import (
	"encoding/json"
	"fmt"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"

	"github.com/urfave/cli/v2"
)

func cmdStatus() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Action:    status,
		Category:  "INSPECTOR",
		Usage:     "Show status of a volume",
		ArgsUsage: "META-URL",
		Description: `
It shows basic setting of the target volume, and a list of active sessions (including mount, SDK,
S3-gateway and WebDAV) that are connected with the metadata engine.

NOTE: Read-only session is not listed since it cannot register itself in the metadata.

Examples:
$ juicefs status redis://localhost`,
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:    "session",
				Aliases: []string{"s"},
				Usage:   "show detailed information (sustained inodes, locks) of the specified session (sid)",
			},
			&cli.BoolFlag{
				Name:    "more",
				Aliases: []string{"m"},
				Usage:   "show more statistic information, may take a long time",
			},
		},
	}
}

type sections struct {
	Setting   *meta.Format
	Sessions  []*meta.Session
	Statistic *statistic
}

type statistic struct {
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

func printJson(v interface{}) {
	output, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}
	fmt.Println(string(output))
}

func status(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), nil)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	format.RemoveSecret()

	if sid := ctx.Uint64("session"); sid != 0 {
		s, err := m.GetSession(sid, true)
		if err != nil {
			logger.Fatalf("get session: %s", err)
		}
		printJson(s)
		return nil
	}

	sessions, err := m.ListSessions()
	if err != nil {
		logger.Fatalf("list sessions: %s", err)
	}

	stat := &statistic{}
	var totalSpace uint64
	if err = m.StatFS(meta.Background(), meta.RootInode, &totalSpace, &stat.AvailableSpace, &stat.UsedInodes, &stat.AvailableInodes); err != syscall.Errno(0) {
		logger.Fatalf("stat fs: %s", err)
	}
	stat.UsedSpace = totalSpace - stat.AvailableSpace

	if ctx.Bool("more") {
		progress := utils.NewProgress(false)
		trashFileSpinner := progress.AddDoubleSpinner("Trash Files")
		pendingDeletedFileSpinner := progress.AddDoubleSpinner("Pending Deleted Files")
		trashSlicesSpinner := progress.AddDoubleSpinner("Trash Slices")
		pendingDeletedSlicesSpinner := progress.AddDoubleSpinner("Pending Deleted Slices")
		err = m.ScanDeletedObject(
			meta.WrapContext(ctx.Context),
			func(ss []meta.Slice, _ int64) (bool, error) {
				for _, s := range ss {
					trashSlicesSpinner.IncrInt64(int64(s.Size))
				}
				return false, nil
			},
			func(_ uint64, size uint32) (bool, error) {
				pendingDeletedSlicesSpinner.IncrInt64(int64(size))
				return false, nil
			},
			func(_ meta.Ino, size uint64, _ time.Time) (bool, error) {
				trashFileSpinner.IncrInt64(int64(size))
				return false, nil
			},
			func(_ meta.Ino, size uint64, _ int64) (bool, error) {
				pendingDeletedFileSpinner.IncrInt64(int64(size))
				return false, nil
			},
		)
		if err != nil {
			logger.Fatalf("statistic: %s", err)
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

	printJson(&sections{format, sessions, stat})
	return nil
}
