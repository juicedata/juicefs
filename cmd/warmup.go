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
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdWarmup() *cli.Command {
	return &cli.Command{
		Name:      "warmup",
		Action:    warmup,
		Category:  "TOOL",
		Usage:     "Build cache for target directories/files",
		ArgsUsage: "[PATH ...]",
		Description: `
This command provides a faster way to actively build cache for the target files. It reads all objects
of the files and then write them into local cache directory.

Examples:
# Warm all files in datadir
$ juicefs warmup /mnt/jfs/datadir

# Warm only three files in datadir
$ cat /tmp/filelist
/mnt/jfs/datadir/f1
/mnt/jfs/datadir/f2
/mnt/jfs/datadir/f3
$ juicefs warmup -f /tmp/filelist`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "file",
				Aliases: []string{"f"},
				Usage:   "file containing a list of paths",
			},
			&cli.UintFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   50,
				Usage:   "number of concurrent workers",
			},
			&cli.BoolFlag{
				Name:    "background",
				Aliases: []string{"b"},
				Usage:   "run in background",
			},
		},
	}
}

const batchMax = 10240

func readControl(cf *os.File, resp []byte) int {
	for {
		if n, err := cf.Read(resp); err == nil {
			return n
		} else if err == io.EOF {
			time.Sleep(time.Millisecond * 300)
		} else {
			logger.Fatalf("Read message: %d %s", n, err)
		}
	}
}

func readProgress(cf *os.File, showProgress func(uint64, uint64)) (errno syscall.Errno) {
	var resp = make([]byte, 1024)
END:
	for {
		n := readControl(cf, resp)
		for off := 0; off < n; {
			if off+1 == n {
				errno = syscall.Errno(resp[off])
				break END
			} else if off+17 <= n && resp[off] == meta.CPROGRESS {
				showProgress(binary.BigEndian.Uint64(resp[off+1:off+9]), binary.BigEndian.Uint64(resp[off+9:off+17]))
				off += 17
			} else {
				logger.Errorf("Bad response off %d n %d: %v", off, n, resp)
				break
			}
		}
	}
	if errno != 0 && runtime.GOOS == "windows" {
		errno += 0x20000000
	}
	return
}

// send fill-cache command to controller file
func sendCommand(cf *os.File, batch []string, threads uint, background bool, dspin *utils.DoubleSpinner) {
	paths := strings.Join(batch, "\n")
	var back uint8
	if background {
		back = 1
	}
	wb := utils.NewBuffer(8 + 4 + 3 + uint32(len(paths)))
	wb.Put32(meta.FillCache)
	wb.Put32(4 + 3 + uint32(len(paths)))
	wb.Put32(uint32(len(paths)))
	wb.Put([]byte(paths))
	wb.Put16(uint16(threads))
	wb.Put8(back)
	if _, err := cf.Write(wb.Bytes()); err != nil {
		logger.Fatalf("Write message: %s", err)
	}
	if background {
		logger.Infof("Warm-up cache for %d paths in background", len(batch))
		return
	}
	if errno := readProgress(cf, func(count, bytes uint64) {
		dspin.SetCurrent(int64(count), int64(bytes))
	}); errno != 0 {
		logger.Fatalf("Warm up failed: %s", errno)
	}
}

func warmup(ctx *cli.Context) error {
	setup(ctx, 0)
	var paths []string
	for _, p := range ctx.Args().Slice() {
		if abs, err := filepath.Abs(p); err == nil {
			paths = append(paths, abs)
		} else {
			logger.Fatalf("Failed to get absolute path of %s: %s", p, err)
		}
	}
	if fname := ctx.String("file"); fname != "" {
		fd, err := os.Open(fname)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		defer fd.Close()
		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			if p := strings.TrimSpace(scanner.Text()); p != "" {
				if abs, e := filepath.Abs(p); e == nil {
					paths = append(paths, abs)
				} else {
					logger.Warnf("Skipped path %s because it fails to get absolute path: %s", p, e)
				}
			}
		}
		if err = scanner.Err(); err != nil {
			logger.Fatalf("Reading file %s failed with error: %s", fname, err)
		}
	}
	if len(paths) == 0 {
		logger.Infof("Nothing to warm up")
		return nil
	}

	// find mount point
	first := paths[0]
	controller := openController(first)
	if controller == nil {
		logger.Fatalf("open control file for %s", first)
	}
	defer controller.Close()

	mp := first
	for ; mp != "/"; mp = filepath.Dir(mp) {
		inode, err := utils.GetFileInode(mp)
		if err != nil {
			logger.Fatalf("lookup inode for %s: %s", mp, err)
		}
		if inode == uint64(meta.RootInode) {
			break
		}
	}

	threads := ctx.Uint("threads")
	if threads == 0 {
		logger.Warnf("threads should be larger than 0, reset it to 1")
		threads = 1
	}
	background := ctx.Bool("background")
	start := len(mp)
	batch := make([]string, 0, batchMax)
	progress := utils.NewProgress(background)
	dspin := progress.AddDoubleSpinner("Warming up")
	for _, path := range paths {
		if mp == "/" {
			inode, err := utils.GetFileInode(path)
			if err != nil {
				logger.Errorf("lookup inode for %s: %s", mp, err)
				continue
			}
			batch = append(batch, fmt.Sprintf("inode:%d", inode))
		} else if strings.HasPrefix(path, mp) {
			batch = append(batch, path[start:])
		} else {
			logger.Errorf("Path %s is not under mount point %s", path, mp)
			continue
		}
		if len(batch) >= batchMax {
			sendCommand(controller, batch, threads, background, dspin)
			batch = batch[0:]
		}
	}
	if len(batch) > 0 {
		sendCommand(controller, batch, threads, background, dspin)
	}
	progress.Done()
	if !background {
		count, bytes := dspin.Current()
		logger.Infof("Successfully warmed up %d files (%d bytes)", count, bytes)
	}

	return nil
}
