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
	"runtime"

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
	}
}

func openController(mp string) *os.File {
	st, err := os.Stat(mp)
	if err != nil {
		logger.Fatal(err)
	}
	if !st.IsDir() {
		mp = filepath.Dir(mp)
	}
	for ; mp != "/"; mp = filepath.Dir(mp) {
		f, err := os.OpenFile(filepath.Join(mp, ".control"), os.O_RDWR, 0)
		if err == nil {
			return f
		}
		if !os.IsNotExist(err) {
			logger.Fatal(err)
		}
	}
	logger.Fatalf("Path %s is not inside JuiceFS", mp)
	panic("unreachable")
}

func rmr(ctx *cli.Context) error {
	setup(ctx, 1)
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	progress := utils.NewProgress(false, true)
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
		f := openController(d)
		if f == nil {
			logger.Errorf("%s is not inside JuiceFS", path)
			continue
		}
		wb := utils.NewBuffer(8 + 8 + 1 + uint32(len(name)))
		wb.Put32(meta.Rmr)
		wb.Put32(8 + 1 + uint32(len(name)))
		wb.Put64(inode)
		wb.Put8(uint8(len(name)))
		wb.Put([]byte(name))
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}
		if errno := readProgress(f, func(count, bytes uint64) {
			spin.SetCurrent(int64(count))
		}); errno != 0 {
			logger.Fatalf("RMR %s: %s", path, errno)
		}
		_ = f.Close()
	}
	progress.Done()
	return nil
}
