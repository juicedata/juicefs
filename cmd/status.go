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

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"

	"github.com/dustin/go-humanize"
	"github.com/urfave/cli/v2"
	"golang.org/x/sync/errgroup"
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
			&cli.StringSliceFlag{
				Name:  "scan",
				Usage: "scanning the meta engine for more information, may take a long time",
			},
		},
	}
}

type sections struct {
	Setting   *meta.Format
	Sessions  []*meta.Session
	FsStat    *fsStat
	Statistic *statistic `json:",omitempty"`
}

type fsStat struct {
	UsedSpace       string
	AvailableSpace  string
	UsedInodes      uint64
	AvailableInodes uint64

	totalSpace     uint64
	availableSpace uint64
}

type statistic struct {
	DelayedDeleted statDelayedDeleted `json:",omitempty"`
}
type statDelayedDeleted struct {
	SliceCount uint64 `json:",omitempty"`
	SliceSize  string `json:",omitempty"`
	FileCount  uint64 `json:",omitempty"`
	FileSize   string `json:",omitempty"`

	sliceSize uint64
	fileSize  uint64
}

func (s *fsStat) Format() {
	s.AvailableSpace = humanize.IBytes(s.availableSpace)
	s.UsedSpace = humanize.IBytes(s.totalSpace - s.availableSpace)
}

func (s *statDelayedDeleted) Format() {
	if s.SliceCount > 0 {
		s.SliceSize = humanize.IBytes(s.sliceSize)
	}
	if s.FileCount > 0 {
		s.FileSize = humanize.IBytes(s.fileSize)
	}
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
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
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

	var fs fsStat
	if err = m.StatFS(meta.Background, &fs.totalSpace, &fs.availableSpace, &fs.UsedInodes, &fs.AvailableInodes); err != syscall.Errno(0) {
		logger.Fatalf("stat fs: %s", err)
	}
	fs.Format()

	sec := &sections{format, sessions, &fs, nil}
	if scan := ctx.StringSlice("scan"); len(scan) > 0 {
		stat := &statistic{}
		progress := utils.NewProgress(false, false)
		eg := &errgroup.Group{}
		for _, s := range scan {
			switch s {
			case "slice":
				eg.Go(func() (err error) {
					return scanDeletedSlices(ctx, m, &stat.DelayedDeleted, progress)
				})
			case "file":
				eg.Go(func() (err error) {
					return scanDeletedFiles(ctx, m, &stat.DelayedDeleted, progress)
				})
			default:
				logger.Warnf("unknown scan option: %s, ignored", s)
			}
		}
		if err := eg.Wait(); err != nil {
			logger.Fatalf("scan: %s", err)
		}
		sec.Statistic = stat
	}
	printJson(sec)
	return nil
}

func scanDeletedSlices(ctx *cli.Context, m meta.Meta, stat *statDelayedDeleted, progress *utils.Progress) error {
	slicesSpinner := progress.AddDoubleSpinner("Delayed Slices")
	defer slicesSpinner.Done()
	err := m.ScanDeletedSlices(ctx.Context, func(s meta.Slice) error {
		slicesSpinner.IncrInt64(int64(s.Size))
		return nil
	})
	if err != nil {
		logger.Fatalf("scan delayed slices: %s", err)
		return err
	}
	count, size := slicesSpinner.Current()
	stat.SliceCount = uint64(count)
	stat.sliceSize = uint64(size)
	stat.Format()
	return nil
}

func scanDeletedFiles(ctx *cli.Context, m meta.Meta, stat *statDelayedDeleted, progress *utils.Progress) error {
	fileSpinner := progress.AddDoubleSpinner("Delayed Files")
	defer fileSpinner.Done()
	err := m.ScanDeletedFiles(ctx.Context, func(_ meta.Ino, size uint64) error {
		fileSpinner.IncrInt64(int64(size))
		return nil
	})
	if err != nil {
		logger.Fatalf("scan delayed files: %s", err)
		return err
	}
	count, size := fileSpinner.Current()
	stat.FileCount = uint64(count)
	stat.fileSize = uint64(size)
	stat.Format()
	return nil
}
