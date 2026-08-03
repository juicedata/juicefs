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
	"sort"
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
$ juicefs warmup -f /tmp/filelist

# Warm a Lance dataset (parse manifest, warmup all data files automatically)
$ juicefs warmup --lance /mnt/jfs/data/mydataset.lance

# Warm only the Lance manifest file (metadata only, no data files)
$ juicefs warmup --lance --lance-manifest-only /mnt/jfs/data/mydataset.lance

# Warm a specific Lance dataset version
$ juicefs warmup --lance --lance-version 5 /mnt/jfs/data/mydataset.lance

# Warm a Lance dataset including index files
$ juicefs warmup --lance --lance-include-indices /mnt/jfs/data/mydataset.lance`,
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
			&cli.BoolFlag{
				Name:  "lance",
				Usage: "treat paths as Lance dataset directories, parse manifest and warmup related data files",
			},
			&cli.StringFlag{
				Name:  "lance-version",
				Usage: "specific Lance dataset version to warmup (default: latest)",
			},
			&cli.BoolFlag{
				Name:  "lance-manifest-only",
				Usage: "only warmup Lance manifest files, not data files",
			},
			&cli.BoolFlag{
				Name:  "lance-include-indices",
				Usage: "also warmup Lance index files under _indices/",
			},
			&cli.StringFlag{
				Name:  "lance-columns",
				Usage: "comma-separated column names to warmup column-level metadata (V2 format only)",
			},
			&cli.BoolFlag{
				Name:  "lance-column-data",
				Usage: "also warmup column data pages in addition to metadata",
			},
		},
	}
}

const batchMax = 10240

const maxInterval = 300
const minInterval = 1

var interval int

func readControl(cf *os.File, resp []byte) int {
	if interval <= 0 {
		interval = 10
	}
	for {
		if n, err := cf.Read(resp); err == nil {
			interval = max(interval/2, minInterval)
			return n
		} else if err == io.EOF {
			interval = min(interval*2, maxInterval)
			time.Sleep(time.Millisecond * time.Duration(interval))
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
func sendCommand(cf *os.File, action vfs.CacheAction, batch []string, threads uint, background bool, dspin *utils.DoubleSpinner, lanceCfg *vfs.LanceWarmupConfig) *vfs.CacheResponse {
	paths := strings.Join(batch, "\n")
	var back uint8
	if background {
		back = 1
	}
	// Base body: pathsLen(4) + paths + threads(2) + background(1) + action(1)
	bodyLen := uint32(4 + len(paths) + 2 + 1 + 1)
	// Optional lance config: lance_flag(1) + manifest_only(1) + include_indices(1) + version_len(2) + version
	if lanceCfg != nil {
		bodyLen += 1 + 1 + 1 + 2 + uint32(len(lanceCfg.Version))
		// Optional columns: columns_len(2) + columns_data + include_data_pages(1)
		if len(lanceCfg.Columns) > 0 {
			colData := strings.Join(lanceCfg.Columns, ",")
			bodyLen += 2 + uint32(len(colData)) + 1
		}
	}
	headerLen := uint32(8)
	wb := utils.NewBuffer(headerLen + bodyLen)
	wb.Put32(meta.FillCache)
	wb.Put32(bodyLen)

	wb.Put32(uint32(len(paths)))
	wb.Put([]byte(paths))
	wb.Put16(uint16(threads))
	wb.Put8(back)
	wb.Put8(uint8(action))

	if lanceCfg != nil {
		wb.Put8(1) // lance flag: 1 = lance mode enabled
		var manifestOnly uint8
		if lanceCfg.ManifestOnly {
			manifestOnly = 1
		}
		wb.Put8(manifestOnly)
		var includeIndices uint8
		if lanceCfg.IncludeIndices {
			includeIndices = 1
		}
		wb.Put8(includeIndices)
		wb.Put16(uint16(len(lanceCfg.Version)))
		wb.Put([]byte(lanceCfg.Version))

		// Optional: column names
		if len(lanceCfg.Columns) > 0 {
			colData := strings.Join(lanceCfg.Columns, ",")
			wb.Put16(uint16(len(colData)))
			wb.Put([]byte(colData))
			var includeData uint8
			if lanceCfg.IncludeDataPages {
				includeData = 1
			}
			wb.Put8(includeData)
		}
	}

	if _, err := cf.Write(wb.Bytes()); err != nil {
		logger.Fatalf("Write message: %s", err)
	}

	resp := &vfs.CacheResponse{}
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

	err := json.Unmarshal(data, resp)
	if err != nil {
		logger.Fatalf("unmarshal error: %s", err)
	}

	return resp
}

func warmup(ctx *cli.Context) error {
	setup0(ctx, 0, 0)

	evict, check := ctx.Bool("evict"), ctx.Bool("check")
	if evict && check {
		logger.Fatalf("--check and --evict can't be used together")
	}

	var paths []string
	for _, p := range ctx.Args().Slice() {
		if abs, err := filepath.Abs(p); err == nil {
			paths = append(paths, abs)
		} else {
			logger.Fatalf("Failed to get absolute path of %q: %s", p, err)
		}
	}
	if fname := ctx.String("file"); fname != "" {
		fd, err := os.Open(fname)
		if err != nil {
			logger.Fatalf("Failed to open file %q: %s", fname, err)
		}
		defer fd.Close()
		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			if p := strings.TrimSpace(scanner.Text()); p != "" {
				if abs, e := filepath.Abs(p); e == nil {
					paths = append(paths, abs)
				} else {
					logger.Warnf("Skipped path %q because it fails to get absolute path: %s", p, e)
				}
			}
		}
		if err = scanner.Err(); err != nil {
			logger.Fatalf("Reading file %q failed with error: %s", fname, err)
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
			logger.Fatalf("lookup inode for %q: %s", mp, err)
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

	lanceMode := ctx.Bool("lance")
	var lanceCfg *vfs.LanceWarmupConfig
	if lanceMode {
		lanceCfg = &vfs.LanceWarmupConfig{
			Version:          ctx.String("lance-version"),
			ManifestOnly:     ctx.Bool("lance-manifest-only"),
			IncludeIndices:   ctx.Bool("lance-include-indices"),
			IncludeDataPages: ctx.Bool("lance-column-data"),
		}
		if cols := ctx.String("lance-columns"); cols != "" {
			lanceCfg.Columns = strings.Split(cols, ",")
			for i, c := range lanceCfg.Columns {
				lanceCfg.Columns[i] = strings.TrimSpace(c)
			}
			logger.Infof("Lance column warmup: columns=%v, include-data=%v",
				lanceCfg.Columns, lanceCfg.IncludeDataPages)
		}
		if lanceCfg.Version != "" {
			logger.Infof("Lance mode: version=%s, manifest-only=%v, include-indices=%v",
				lanceCfg.Version, lanceCfg.ManifestOnly, lanceCfg.IncludeIndices)
		} else {
			logger.Infof("Lance mode: latest version, manifest-only=%v, include-indices=%v",
				lanceCfg.ManifestOnly, lanceCfg.IncludeIndices)
		}
	}

	background := ctx.Bool("background")
	start := len(mp)
	batch := make([]string, 0, batchMax)
	progress := utils.NewProgress(background)
	dspin := progress.AddDoubleSpinnerTwo(fmt.Sprintf("%s file", action), fmt.Sprintf("%s size", action))
	total := &vfs.CacheResponse{Locations: make(map[string]uint64)}
	for _, path := range paths {
		if mp == "/" {
			inode, err := utils.GetFileInode(path)
			if err != nil {
				logger.Errorf("lookup inode for %q: %s", mp, err)
				continue
			}
			batch = append(batch, fmt.Sprintf("inode:%d", inode))
		} else if strings.HasPrefix(path, mp) {
			batch = append(batch, path[start:])
		} else {
			logger.Errorf("Path %q is not under mount point %q", path, mp)
			continue
		}
		if len(batch) >= batchMax {
			resp := sendCommand(controller, action, batch, threads, background, dspin, lanceCfg)
			total.Add(resp)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		resp := sendCommand(controller, action, batch, threads, background, dspin, lanceCfg)
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
			if len(total.Locations) > 0 {
				var result = [][]string{
					{"Location", "Size", "Percentage"},
				}
				var locs []string
				for loc := range total.Locations {
					locs = append(locs, loc)
				}
				sort.Strings(locs)
				for _, loc := range locs {
					size := total.Locations[loc]
					result = append(result, []string{loc, humanize.IBytes(size), fmt.Sprintf("%.1f%%", float64(size)*100/float64(bytes))})
				}
				printResult(result, 0, false)
			}
			pct := 0.0
			if bytes != 0 {
				pct = float64(uint64(bytes)-total.MissBytes) * 100 / float64(bytes)
			}
			logger.Infof("%s: %d files checked, %s of %s (%2.1f%%) cached", action, count,
				humanize.IBytes(uint64(bytes)-total.MissBytes),
				humanize.IBytes(uint64(bytes)),
				pct)
		}
	}
	return nil
}
