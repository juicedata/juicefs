/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func rmrFlags() *cli.Command {
	return &cli.Command{
		Name:      "rmr",
		Usage:     "remove directories recursively",
		ArgsUsage: "PATH ...",
		Action:    rmr,
	}
}

func openControler(path string) *os.File {
	f, err := os.OpenFile(filepath.Join(path, ".control"), os.O_RDWR, 0)
	if err != nil && path != "/" {
		return openControler(filepath.Dir(path))
	}
	return f
}

func rmr(ctx *cli.Context) error {
	if ctx.Args().Len() < 1 {
		logger.Infof("PATH is needed")
		return nil
	}
	for i := 0; i < ctx.Args().Len(); i++ {
		path := ctx.Args().Get(i)
		d := filepath.Dir(path)
		name := filepath.Base(path)
		inode, err := utils.GetFileInode(d)
		if err != nil {
			return fmt.Errorf("lookup inode for %s: %s", d, err)
		}
		f := openControler(d)
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
		var errs = make([]byte, 1)
		n, err := f.Read(errs)
		if err != nil || n != 1 {
			logger.Fatalf("read message: %d %s", n, err)
		}
		if errs[0] != 0 {
			logger.Fatalf("RMR %s: %s", path, syscall.Errno(errs[0]))
		}
		_ = f.Close()
	}
	return nil
}
