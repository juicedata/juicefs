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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
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
			&cli.BoolFlag{
				Name:  "evict",
				Usage: "evict cached blocks",
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "check whether the data blocks are cached or not",
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
		} else if errors.Is(err, syscall.EBADF) {
			logger.Fatalf("JuiceFS client was restarted")
		} else {
			logger.Fatalf("Read message: %d %s", n, err)
		}
	}
}

func readProgress(cf *os.File, showProgress func(uint64, uint64)) (data []byte, errno syscall.Errno) {
	var resp = make([]byte, 2<<16)
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
			} else if off+5 < n && resp[off] == meta.CDATA {
				size := binary.BigEndian.Uint32(resp[off+1 : off+5])
				data = resp[off+5:]
				if size > uint32(len(resp[off+5:])) {
					tailData, err := io.ReadAll(cf)
					if err != nil {
						logger.Errorf("Read data error: %v", err)
						break END
					}
					data = append(data, tailData...)
				} else {
					data = data[:size]
				}
				break END
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
func sendCommand(cf *os.File, action vfs.CacheAction, batch []string, threads uint, background bool, dspin *utils.DoubleSpinner) vfs.CacheResponse {
	paths := strings.Join(batch, "\n")
	var back uint8
	if background {
		back = 1
	}
	headerLen, bodyLen := uint32(8), uint32(4+len(paths)+2+1+1)
	wb := utils.NewBuffer(headerLen + bodyLen)
	wb.Put32(meta.FillCache)
	wb.Put32(bodyLen)

	wb.Put32(uint32(len(paths)))
	wb.Put([]byte(paths))
	wb.Put16(uint16(threads))
	wb.Put8(back)
	wb.Put8(uint8(action))

	if _, err := cf.Write(wb.Bytes()); err != nil {
		logger.Fatalf("Write message: %s", err)
	}

	var resp vfs.CacheResponse
	if background {
		logger.Infof("%s for %d paths in background", action, len(batch))
		return resp
	}

	lastCnt, lastBytes := dspin.Current()
	data, errno := readProgress(cf, func(fileCount, totalBytes uint64) {
		dspin.SetCurrent(lastCnt+int64(fileCount), lastBytes+int64(totalBytes))
	})

	if errno != 0 {
		logger.Fatalf("%s failed: %s", action, errno)
	}

	err := json.Unmarshal(data, &resp)
	if err != nil {
		logger.Fatalf("unmarshal error: %s", err)
	}

	return resp
}

func warmup(ctx *cli.Context) error {
	setup(ctx, 0)

	evict, check := ctx.Bool("evict"), ctx.Bool("check")
	if evict && check {
		logger.Fatalf("--check and --evict can't be used together")
	}

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
		logger.Infof("no path")
		return nil
	}

	// find mount point
	first := paths[0]
	controller, err := openController(first)
	if err != nil {
		return fmt.Errorf("open control file for %s: %s", first, err)
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

	action := vfs.WarmupCache
	if evict {
		action = vfs.EvictCache
	} else if check {
		action = vfs.CheckCache
	}

	background := ctx.Bool("background")
	start := len(mp)
	batch := make([]string, 0, batchMax)
	progress := utils.NewProgress(background)
	dspin := progress.AddDoubleSpinnerTwo(fmt.Sprintf("%s file", action), fmt.Sprintf("%s size", action))
	total := &vfs.CacheResponse{}
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
			resp := sendCommand(controller, action, batch, threads, background, dspin)
			total.Add(resp)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		resp := sendCommand(controller, action, batch, threads, background, dspin)
		total.Add(resp)
	}
	progress.Done()

	if !background {
		count, bytes := dspin.Current()
		switch action {
		case vfs.WarmupCache:
			logger.Infof("%s: %d files (%s bytes)", action, count, humanize.IBytes(uint64(bytes)))
		case vfs.EvictCache:
			logger.Infof("%s: %d files (%s bytes)", action, count, humanize.IBytes(uint64(bytes)))
		case vfs.CheckCache:
			logger.Infof("%s: %d files checked, %s of %s (%2.1f%%) cached", action, count,
				humanize.IBytes(uint64(bytes)-total.MissBytes),
				humanize.IBytes(uint64(bytes)),
				float64(uint64(bytes)-total.MissBytes)*100/float64(bytes))
		}
	}
	return nil
}
