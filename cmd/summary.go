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

package cmd

import (
	"os"
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
 
 # Show with inode
 $ cd /mnt/jfs
 $ juicefs summary -i 100
 
 # Show max depth of 5
 $ juicefs summary -d 5 /mnt/jfs/foo

 # Show top 20 entries
 $ juicefs summary -t 20 /mnt/jfs/foo

 # Show accurate result
 $ juicefs summary --strict /mnt/jfs/foo
 `,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "inode",
				Aliases: []string{"i"},
				Usage:   "use inode instead of path (current dir should be inside JuiceFS)",
			},
			&cli.UintFlag{
				Name:    "depth",
				Aliases: []string{"d"},
				Value:   3,
				Usage:   "depth of tree to show (zero means only show root)",
			},
			&cli.UintFlag{
				Name:    "top",
				Aliases: []string{"t"},
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
	var depth, topN, dirOnly uint8
	depth = 3
	topN = 10
	dirOnly = 1

	if ctx.IsSet("depth") {
		d := ctx.Uint("depth")
		if d > 10 {
			logger.Warn("depth should be less than 11")
			d = 10
		}
		depth = uint8(d)
	}
	if ctx.IsSet("top") {
		t := ctx.Uint("top")
		if t > 100 {
			logger.Warn("top should be less than 101")
			t = 100
		}
		topN = uint8(t)
	}
	if ctx.Bool("strict") {
		dirOnly = 0
	}

	progress := utils.NewProgress(depth > 4 || topN > 100) // only show progress for slow summary
	for i := 0; i < ctx.Args().Len(); i++ {
		path := ctx.Args().Get(i)
		dspin := progress.AddDoubleSpinner(path)
		var d string
		var inode uint64
		var err error
		if ctx.Bool("inode") {
			inode, err = strconv.ParseUint(path, 10, 64)
			d, _ = os.Getwd()
		} else {
			d, err = filepath.Abs(path)
			if err != nil {
				logger.Fatalf("abs of %s: %s", path, err)
			}
			inode, err = utils.GetFileInode(d)
		}
		if err != nil {
			logger.Errorf("lookup inode for %s: %s", path, err)
			continue
		}
		if inode < uint64(meta.RootInode) {
			logger.Fatalf("inode number shouldn't be less than %d", meta.RootInode)
		}
		f, err := openController(d)
		if err != nil {
			logger.Fatalf("open controller: %s", err)
			continue
		}
		headerLen := uint32(8)
		contentLen := uint32(8 + 1 + 1 + 1)
		wb := utils.NewBuffer(headerLen + contentLen)
		wb.Put32(meta.OpSummary)
		wb.Put32(contentLen)
		wb.Put64(inode)
		wb.Put8(depth)
		wb.Put8(topN)
		wb.Put8(dirOnly)
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}
		if errno := readProgress(f, func(count, size uint64) {
			dspin.SetCurrent(int64(count), int64(size))
		}); errno != 0 {
			logger.Errorf("failed to get info: %s", syscall.Errno(errno))
		}
		dspin.Done()

		var resp vfs.SummaryReponse
		err = resp.Decode(f)
		_ = f.Close()
		if err == syscall.EINVAL {
			logger.Fatalf("summary is not supported, please upgrade and mount again")
		}

		if err != nil {
			logger.Fatalf("summary: %s", err)
		}
		var results [][]string
		if dirOnly == 0 {
			results = [][]string{{"Path", "Type", "Length", "Size", "Files", "Dirs"}}
		} else {
			results = [][]string{{"Path", "Length", "Size", "Files", "Dirs"}}
		}
		renderTree(&results, &resp.Tree, dirOnly > 0)
		printResult(results, 0, false)
	}
	progress.Done()
	return nil
}

func renderTree(results *[][]string, tree *meta.TreeSummary, dirOnly bool) {
	if tree == nil {
		return
	}
	var result []string
	if dirOnly {
		result = []string{
			tree.Path,
			humanize.IBytes(uint64(tree.Length)),
			humanize.IBytes(uint64(tree.Size)),
			strconv.FormatUint(tree.Files, 10),
			strconv.FormatUint(tree.Dirs, 10),
		}
	} else {
		result = []string{
			tree.Path,
			typeToString(tree.Type),
			humanize.IBytes(uint64(tree.Length)),
			humanize.IBytes(uint64(tree.Size)),
			strconv.FormatUint(tree.Files, 10),
			strconv.FormatUint(tree.Dirs, 10),
		}
	}
	*results = append(*results, result)
	for _, child := range tree.Children {
		renderTree(results, child, dirOnly)
	}
}

func typeToString(tyoe uint8) string {
	switch tyoe {
	case meta.TypeFile:
		return "File"
	case meta.TypeDirectory:
		return "Dir"
	case meta.TypeSymlink:
		return "Symlink"
	case meta.TypeSocket:
		return "Socket"
	case meta.TypeBlockDev:
		return "BlockDev"
	case meta.TypeCharDev:
		return "CharDev"
	case meta.TypeFIFO:
		return "Fifo"
	default:
		return "Unknown"
	}
}
