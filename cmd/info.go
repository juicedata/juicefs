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
	"strconv"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdInfo() *cli.Command {
	return &cli.Command{
		Name:      "info",
		Action:    info,
		Category:  "INSPECTOR",
		Usage:     "Show internal information of a path or inode",
		ArgsUsage: "PATH/INODE",
		Description: `
It is used to inspect internal metadata values of the target file.

Examples:
$ Check a path
$ juicefs info /mnt/jfs/foo

# Check an inode
$ cd /mnt/jfs
$ juicefs info -i 100`,
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
			&cli.BoolFlag{
				Name:  "raw",
				Usage: "show internal raw information",
			},
		},
	}
}

func info(ctx *cli.Context) error {
	setup(ctx, 1)
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	var recursive, raw uint8
	if ctx.Bool("recursive") {
		recursive = 1
	}
	if ctx.Bool("raw") {
		raw = 1
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

		wb := utils.NewBuffer(8 + 10)
		wb.Put32(meta.Info)
		wb.Put32(10)
		wb.Put64(inode)
		wb.Put8(recursive)
		wb.Put8(raw)
		_, err = f.Write(wb.Bytes())
		if err != nil {
			logger.Fatalf("write message: %s", err)
		}
		data := make([]byte, 4)
		n := readControl(f, data)
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
		resp := string(data[:n])
		var p int
		if p = strings.Index(resp, "chunks:\n"); p > 0 {
			p += 8
			raw = 1 // legacy clients always return chunks
		} else if p = strings.Index(resp, "objects:\n"); p > 0 {
			p += 9
		}
		if p <= 0 {
			fmt.Println(resp)
		} else {
			fmt.Println(resp[:p-1])
			if len(resp[p:]) > 0 {
				printChunks(resp[p:], raw == 1)
			}
		}
		_ = f.Close()
	}

	return nil
}

func printChunks(resp string, raw bool) {
	cs := strings.Split(resp, "\n")
	result := make([][]string, len(cs))
	result[0] = []string{"chunkIndex", "objectName", "size", "offset", "length"}
	leftAlign := 1
	if raw {
		result[0][1] = "sliceId"
		leftAlign = -1
	}
	for i := 1; i < len(result); i++ {
		result[i] = make([]string, 5) // len(result[0])
	}

	for i, c := range cs[:len(cs)-1] { // remove the last empty string
		ps := strings.Split(c, "\t")[1:] // remove the first empty string
		for j, p := range ps {
			if j == 0 {
				p = p[:len(p)-1] // remove the last ':'
			}
			result[i+1][j] = p
		}
	}
	printResult(result, leftAlign, false)
}
