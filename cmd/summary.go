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
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		ArgsUsage: "PATH",
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
			&cli.BoolFlag{
				Name:  "csv",
				Usage: "print summary in csv format",
			},
		},
	}
}

func summary(ctx *cli.Context) error {
	setup(ctx, 1)
	var strict uint8
	if ctx.Bool("strict") {
		strict = 1
	}
	depth := ctx.Uint("depth")
	if depth > 10 {
		logger.Warn("depth should be less than 11")
		depth = 10
	}
	topN := ctx.Uint("entries")
	if topN > 100 {
		logger.Warn("entries should be less than 101")
		topN = 100
	}

	csv := ctx.Bool("csv")
	progress := utils.NewProgress(csv)
	path := ctx.Args().Get(0)
	dspin := progress.AddDoubleSpinner(path)
	dpath, err := filepath.Abs(path)
	if err != nil {
		logger.Fatalf("abs of %s: %s", path, err)
	}
	inode, err := utils.GetFileInode(dpath)
	if err != nil {
		logger.Fatalf("lookup inode for %s: %s", path, err)
	}
	if inode < uint64(meta.RootInode) {
		logger.Fatalf("inode number shouldn't be less than %d", meta.RootInode)
	}
	f, err := openController(dpath)
	if err != nil {
		logger.Fatalf("open controller: %s", err)
	}
	headerLen := uint32(8)
	contentLen := uint32(8 + 1 + 1 + 1)
	wb := utils.NewBuffer(headerLen + contentLen)
	wb.Put32(meta.OpSummary)
	wb.Put32(contentLen)
	wb.Put64(inode)
	wb.Put8(uint8(depth))
	wb.Put8(uint8(topN))
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
	renderTree(&results, &resp.Tree, csv)
	if csv {
		printCSVResult(results)
	} else {
		printResult(results, 0, false)
	}
	return nil
}

func printCSVResult(results [][]string) {
	w := csv.NewWriter(os.Stdout)
	for _, r := range results {
		if err := w.Write(r); err != nil {
			logger.Fatalln("error writing record to csv:", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		logger.Fatal(err)
	}
}

func renderTree(results *[][]string, tree *meta.TreeSummary, csv bool) {
	if tree == nil {
		return
	}
	var size string
	if csv {
		size = strconv.FormatUint(tree.Size, 10)
	} else {
		size = humanize.IBytes(uint64(tree.Size))
	}

	path := tree.Path
	if tree.Type == meta.TypeDirectory && !strings.HasSuffix(path, "/") {
		path += "/"
	}

	result := []string{
		path,
		size,
		strconv.FormatUint(tree.Dirs, 10),
		strconv.FormatUint(tree.Files, 10),
	}
	*results = append(*results, result)
	for _, child := range tree.Children {
		renderTree(results, child, csv)
	}
}
