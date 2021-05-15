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
	"runtime"
	"strconv"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func infoFlags() *cli.Command {
	return &cli.Command{
		Name:      "info",
		Usage:     "show internal information for paths or inodes",
		ArgsUsage: "PATH or INODE",
		Action:    info,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "inode",
				Aliases: []string{"i"},
				Usage:   "use inode instead of path (current dir should be inside JuiceFS)",
			},
		},
	}
}

func info(ctx *cli.Context) error {
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	if ctx.Args().Len() < 1 {
		logger.Infof("DIR or FILE is needed")
		return nil
	}
	for i := 0; i < ctx.Args().Len(); i++ {
		path := ctx.Args().Get(i)
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

		f := openController(d)
		if f == nil {
			logger.Errorf("%s is not inside JuiceFS", path)
			continue
		}

		wb := utils.NewBuffer(8 + 8)
		wb.Put32(meta.Info)
		wb.Put32(8)
		wb.Put64(inode)
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}

		data := make([]byte, 4)
		n, err := f.Read(data)
		if err != nil {
			logger.Fatalf("read size: %d %s", n, err)
		}
		if n == 1 && data[0] == byte(syscall.EINVAL&0xff) {
			logger.Fatalf("info is not supported, please upgrade and mount again")
		}
		r := utils.ReadBuffer(data)
		size := r.Get32()
		data = make([]byte, size)
		n, err = f.Read(data)
		if err != nil {
			logger.Fatalf("read info: %s", err)
		}
		fmt.Println(path, ":")
		fmt.Println(string(data[:n]))
		_ = f.Close()
	}

	return nil
}
