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

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
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
		err = f.Sync()
		if err != nil {
			logger.Fatalf("sync control file: %s", err)
		}
		var resp vfs.InfoResponse
		err = resp.Decode(f)
		if err != nil {
			logger.Fatalf("read info: %s", err)
		}
		_ = f.Close()
		fmt.Println(path, ":")
		fmt.Printf("  inode: %d\n", resp.Ino)
		fmt.Printf("  files: %d\n", resp.Summary.Files)
		fmt.Printf("   dirs: %d\n", resp.Summary.Dirs)
		fmt.Printf(" length: %s\n", utils.FormatBytes(resp.Summary.Length))
		fmt.Printf("   size: %s\n", utils.FormatBytes(resp.Summary.Size))
		switch len(resp.Paths) {
		case 0:
			fmt.Printf("   path: %s\n", "unknown")
		case 1:
			fmt.Printf("   path: %s\n", resp.Paths[0])
		default:
			fmt.Printf("  paths:\n")
			for _, p := range resp.Paths {
				fmt.Printf("\t%s\n", p)
			}
		}
		if len(resp.Chunks) > 0 {
			fmt.Println(" chunks:")
			results := make([][]string, 0, 1+len(resp.Chunks))
			results = append(results, []string{"chunkIndex", "sliceId", "size", "offset", "length"})
			for _, c := range resp.Chunks {
				results = append(results, []string{
					strconv.FormatUint(c.ChunkIndex, 10),
					strconv.FormatUint(c.Id, 10),
					strconv.FormatUint(uint64(c.Size), 10),
					strconv.FormatUint(uint64(c.Off), 10),
					strconv.FormatUint(uint64(c.Len), 10),
				})
			}
			printResult(results, -1, false)
		}
		if len(resp.Objects) > 0 {
			fmt.Println(" objects:")
			results := make([][]string, 0, 1+len(resp.Objects))
			results = append(results, []string{"chunkIndex", "objectName", "size", "offset", "length"})
			for _, o := range resp.Objects {
				results = append(results, []string{
					strconv.FormatUint(o.ChunkIndex, 10),
					o.Key,
					strconv.FormatUint(uint64(o.Size), 10),
					strconv.FormatUint(uint64(o.Off), 10),
					strconv.FormatUint(uint64(o.Len), 10),
				})
			}
			printResult(results, 1, false)
		}
		if len(resp.FLocks) > 0 {
			fmt.Println(" flocks:")
			results := make([][]string, 0, 1+len(resp.FLocks))
			results = append(results, []string{"Sid", "Owner", "Type"})
			for _, l := range resp.FLocks {
				results = append(results, []string{
					strconv.FormatUint(l.Sid, 10),
					strconv.FormatUint(l.Owner, 10),
					l.Type,
				})
			}
			printResult(results, 0, false)
		}
		if len(resp.PLocks) > 0 {
			fmt.Println(" plocks:")
			results := make([][]string, 0, 1+len(resp.PLocks))
			results = append(results, []string{"Sid", "Owner", "Type", "Pid", "Start", "End"})
			for _, l := range resp.PLocks {
				results = append(results, []string{
					strconv.FormatUint(l.Sid, 10),
					strconv.FormatUint(l.Owner, 10),
					ltypeToString(l.Type),
					strconv.FormatUint(uint64(l.Pid), 10),
					strconv.FormatUint(l.Start, 10),
					strconv.FormatUint(l.End, 10),
				})
			}
			printResult(results, 0, false)
		}
	}

	return nil
}

func ltypeToString(t uint32) string {
	switch t {
	case meta.F_RDLCK:
		return "R"
	case meta.F_WRLCK:
		return "W"
	default:
		return "UNKNOWN"
	}
}
