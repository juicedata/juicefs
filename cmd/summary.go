/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/dustin/go-humanize"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func cmdSummary() *cli.Command {
	return &cli.Command{
		Name:      "summary",
		Action:    summary,
		Category:  "INSPECTOR",
		Usage:     "Show tree summary of a directory",
		ArgsUsage: "PATH/INODE",
		Description: `
 It is used to show tree summary of target directory.
 
 Examples:
 # Show with path
 $ juicefs summary /mnt/jfs/foo
 
 # Show max depth of 5
 $ juicefs summary --depth 5 /mnt/jfs/foo

 # Show top 20 entries
 $ juicefs summary --entries 20 /mnt/jfs/foo

 # Show accurate result
 $ juicefs summary --strict /mnt/jfs/foo
 `,
		Flags: []cli.Flag{
			&cli.UintFlag{
				Name:    "depth",
				Aliases: []string{"d"},
				Value:   2,
				Usage:   "depth of tree to show (zero means only show root)",
			},
			&cli.UintFlag{
				Name:    "entries",
				Aliases: []string{"e"},
				Value:   10,
				Usage:   "show top N entries (sort by size)",
			},
			&cli.BoolFlag{
				Name:  "strict",
				Usage: "show accurate summary, including directories and files (may be slow)",
			},
		},
	}
}

func summary(ctx *cli.Context) error {
	setup(ctx, 1)
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	var depth, topN, strict uint8
	depth = 2
	topN = 10
	strict = 0

	if ctx.IsSet("depth") {
		d := ctx.Uint("depth")
		if d > 10 {
			logger.Warn("depth should be less than 11")
			d = 10
		}
		depth = uint8(d)
	}
	if ctx.IsSet("entries") {
		t := ctx.Uint("entries")
		if t > 100 {
			logger.Warn("top should be less than 101")
			t = 100
		}
		topN = uint8(t)
	}
	if ctx.Bool("strict") {
		strict = 1
	}

	progress := utils.NewProgress(false)
	path := ctx.Args().Get(0)
	dspin := progress.AddDoubleSpinner(path)
	d, err := filepath.Abs(path)
	if err != nil {
		logger.Fatalf("abs of %s: %s", path, err)
	}
	inode, err := utils.GetFileInode(d)
	if err != nil {
		logger.Fatalf("lookup inode for %s: %s", path, err)
	}
	if inode < uint64(meta.RootInode) {
		logger.Fatalf("inode number shouldn't be less than %d", meta.RootInode)
	}
	f, err := openController(d)
	if err != nil {
		logger.Fatalf("open controller: %s", err)
	}
	headerLen := uint32(8)
	contentLen := uint32(8 + 1 + 1 + 1)
	wb := utils.NewBuffer(headerLen + contentLen)
	wb.Put32(meta.OpSummary)
	wb.Put32(contentLen)
	wb.Put64(inode)
	wb.Put8(depth)
	wb.Put8(topN)
	wb.Put8(strict)
	_, err = f.Write(wb.Bytes())
	if err != nil {
		logger.Fatalf("write message: %s", err)
	}
	data, errno := readProgress(f, func(count, size uint64) {
		dspin.SetCurrent(int64(count), int64(size))
	})
	if errno == syscall.EINVAL {
		logger.Fatalf("summary is not supported, please upgrade and mount again")
	}
	if errno != 0 {
		logger.Errorf("failed to get info: %s", syscall.Errno(errno))
	}
	dspin.Done()
	progress.Done()

	var resp vfs.SummaryReponse
	err = json.Unmarshal(data, &resp)
	_ = f.Close()
	if err == nil && resp.Errno != 0 {
		err = resp.Errno
	}
	if err != nil {
		logger.Fatalf("summary: %s", err)
	}
	results := [][]string{{"PATH", "SIZE", "DIRS", "FILES"}}
	renderTree(&results, &resp.Tree)
	printResult(results, 0, false)
	return nil
}

func renderTree(results *[][]string, tree *meta.TreeSummary) {
	if tree == nil {
		return
	}
	result := []string{
		tree.Path,
		humanize.IBytes(uint64(tree.Size)),
		strconv.FormatUint(tree.Dirs, 10),
		strconv.FormatUint(tree.Files, 10),
	}
	*results = append(*results, result)
	for _, child := range tree.Children {
		renderTree(results, child)
	}
}
