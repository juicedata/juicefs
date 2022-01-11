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
			&cli.BoolFlag{
				Name:    "recursive",
				Aliases: []string{"r"},
				Usage:   "get summary of directories recursively (NOTE: it may take a long time for huge trees)",
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
	var recursive uint8
	if ctx.Bool("recursive") {
		recursive = 1
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

		wb := utils.NewBuffer(8 + 9)
		wb.Put32(meta.Info)
		wb.Put32(9)
		wb.Put64(inode)
		wb.Put8(recursive)
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
