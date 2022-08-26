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

func cmdSetquota() *cli.Command {
	return &cli.Command{
		Name:      "quota",
		Category:  "TOOL",
		Usage:     "Manage the directory quotas",
		ArgsUsage: "SUBCOMMAND PATH/INODE",
		Subcommands: []*cli.Command{
			{
				Name:   "set",
				Usage:  "Set quota for dir",
				Action: setquota,
			},
			{
				Name:   "del",
				Usage:  "Del quota for dir",
				Action: delquota,
			},
			{
				Name:   "ls",
				Usage:  "Get quota for dir",
				Action: getquota,
			},
			{
				Name:   "fsck",
				Usage:  "Check consistency of directory quota",
				Action: fsckquota,
			},
		},
		Description: `
 This command provides a faster way to actively build cache for the target files. It reads all objects
 of the files and then write them into local cache directory.
 
 Examples:
 # Show quota of a directory specified by path
 $ juicefs quota get /mnt/jfs/foo
 # Show quota of a directory specified by inode
 $ juicefs quota get -i 2

 # Set quota
 $ juicefs quota set /mnt/jfs/foo --capacity 10 --inodes 10000

 # Delete quota
 $ juicefs quota del /mnt/jfs

 # List all quotas
 $ juicefs quota ls`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "inode",
				Aliases: []string{"i"},
				Usage:   "use inode instead of path (current dir should be inside JuiceFS)",
			},
			&cli.Uint64Flag{
				Name:  "capacity",
				Usage: "hard quota of the directory limiting its usage of space in GiB (default: 0)",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Usage: "hard quota of the directory limiting its number of inodes (default: 0)",
			},
		},
	}
}

func setquota(ctx *cli.Context) error {
	setup(ctx, 1)
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	var capacity, inodes uint64
	var set_capacity, set_inodes uint8
	capacity = ctx.Uint64("capacity")
	inodes = ctx.Uint64("inodes")
	for _, flag := range ctx.FlagNames() {
		switch flag {
		case "capacity":
			set_capacity = 1

		case "inodes":
			set_inodes = 1
		}
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
		if inode < uint64(meta.RootInode) {
			logger.Fatalf("inode number shouldn't be less than %d", meta.RootInode)
		}
		f := openController(d)
		if f == nil {
			logger.Errorf("%s is not inside JuiceFS", path)
			continue
		}
		wb := utils.NewBuffer(4 + 4 + 8 + 8 + 8 + 1 + 1)
		wb.Put32(meta.SetQuota)
		wb.Put32(8 + 8 + 8 + 1 + 1)
		wb.Put64(inode)
		wb.Put64(capacity)
		wb.Put64(inodes)
		wb.Put8(set_capacity)
		wb.Put8(set_inodes)
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}
		data := make([]byte, 4)
		n := readControl(f, data)
		if n == 1 && data[0] == byte(syscall.EINVAL&0xff) {
			logger.Fatalf("info is not supported, please upgrade and mount again")
		}
		_ = f.Close()
	}
	return nil
}

func delquota(ctx *cli.Context) error {
	setup(ctx, 0)
	//Todo
	fmt.Printf("remove -----\n")
	return nil
}

func getquota(ctx *cli.Context) error {
	setup(ctx, 0)
	//Todo
	fmt.Printf("get -----\n")
	return nil
}

func fsckquota(ctx *cli.Context) error {
	setup(ctx, 1)
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
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
		if inode < uint64(meta.RootInode) {
			logger.Fatalf("inode number shouldn't be less than %d", meta.RootInode)
		}
		f := openController(d)
		if f == nil {
			logger.Errorf("%s is not inside JuiceFS", path)
			continue
		}
		wb := utils.NewBuffer(4 + 4 + 8)
		wb.Put32(meta.FsckQuota)
		wb.Put32(8)
		wb.Put64(inode)
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}
		data := make([]byte, 4)
		n := readControl(f, data)
		if n == 1 && data[0] == byte(syscall.EINVAL&0xff) {
			logger.Fatalf("info is not supported, please upgrade and mount again")
		}
		_ = f.Close()
	}
	return nil
}
