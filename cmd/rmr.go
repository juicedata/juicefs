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
	"fmt"
	"os"
	"path/filepath"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdRmr() *cli.Command {
	return &cli.Command{
		Name:      "rmr",
		Action:    rmr,
		Category:  "TOOL",
		Usage:     "Remove directories recursively",
		ArgsUsage: "PATH ...",
		Description: `
This command provides a faster way to remove huge directories in JuiceFS.

Examples:
$ juicefs rmr /mnt/jfs/foo`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "skip-trash",
				Usage: "skip trash and delete files directly (requires root)",
			},
			&cli.IntFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   50,
				Usage:   "number of threads for delete jobs (max 255)",
			},
		},
	}
}

func openController(dpath string) (*os.File, error) {
	st, err := os.Stat(dpath)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		dpath = filepath.Dir(dpath)
	}
	fp, err := os.OpenFile(filepath.Join(dpath, ".jfs.control"), os.O_RDWR, 0)
	if os.IsNotExist(err) {
		fp, err = os.OpenFile(filepath.Join(dpath, ".control"), os.O_RDWR, 0)
	}
	return fp, err
}

func rmr(ctx *cli.Context) error {
	setup(ctx, 1)
	var flag uint8
	var numThreads int

	numThreads = ctx.Int("threads")
	if numThreads <= 0 {
		numThreads = meta.RmrDefaultThreads
	}
	if numThreads > 255 {
		numThreads = 255
	}
	if ctx.Bool("skip-trash") {
		if os.Getuid() != 0 {
			logger.Fatalf("Only root can remove files directly")
		}
		flag = 1
	}
	progress := utils.NewProgress(false)
	spin := progress.AddCountSpinner("Removing entries")
	for i := 0; i < ctx.Args().Len(); i++ {
		path := ctx.Args().Get(i)
		p, err := filepath.Abs(path)
		if err != nil {
			logger.Errorf("abs of %s: %s", path, err)
			continue
		}
		d := filepath.Dir(p)
		name := filepath.Base(p)
		inode, err := utils.GetFileInode(d)
		if err != nil {
			return fmt.Errorf("lookup inode for %s: %s", d, err)
		}
		f, err := openController(d)
		if err != nil {
			logger.Errorf("Open control file for %s: %s", d, err)
			continue
		}
		wb := utils.NewBuffer(8 + 8 + 1 + uint32(len(name)) + 1 + 1)
		wb.Put32(meta.Rmr)
		wb.Put32(8 + 1 + uint32(len(name)) + 1 + 1)
		wb.Put64(inode)
		wb.Put8(uint8(len(name)))
		wb.Put([]byte(name))
		wb.Put8(flag)
		wb.Put8(uint8(numThreads))
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}
		if _, errno := readProgress(f, func(count, bytes uint64) {
			spin.SetCurrent(int64(count))
		}); errno != 0 {
			logger.Fatalf("RMR %s: %s", path, errno)
		}
		_ = f.Close()
	}
	progress.Done()
	return nil
}
