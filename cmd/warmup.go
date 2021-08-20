/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

const batchMax = 10240

// send fill-cache command to controller file
func sendCommand(cf *os.File, batch []string, count int, threads uint, background bool) {
	paths := strings.Join(batch[:count], "\n")
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
		logger.Infof("Warm-up cache for %d paths in backgroud", count)
		return
	}
	var errs = make([]byte, 1)
	if n, err := cf.Read(errs); err != nil || n != 1 {
		logger.Fatalf("Read message: %d %s", n, err)
	}
	if errs[0] != 0 {
		logger.Fatalf("Warm up failed: %d", errs[0])
	}
	// logger.Infof("%d paths are warmed up", count)
}

func warmup(ctx *cli.Context) error {
	fname := ctx.String("file")
	paths := ctx.Args().Slice()
	if fname != "" {
		fd, err := os.Open(fname)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		defer fd.Close()
		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			if p := strings.TrimSpace(scanner.Text()); p != "" {
				paths = append(paths, p)
			}
		}
		if err := scanner.Err(); err != nil {
			logger.Fatalf("Reading file %s failed with error: %s", fname, err)
		}
	}
	if len(paths) == 0 {
		logger.Infof("Nothing to warm up")
		return nil
	}

	// find mount point
	first, err := filepath.Abs(paths[0])
	if err != nil {
		logger.Fatalf("Failed to get abs of %s: %s", paths[0], err)
	}
	st, err := os.Stat(first)
	if err != nil {
		logger.Fatalf("Failed to stat path %s: %s", first, err)
	}
	var mp string
	if st.IsDir() {
		mp = first
	} else {
		mp = filepath.Dir(first)
	}
	for ; mp != "/"; mp = filepath.Dir(mp) {
		inode, err := utils.GetFileInode(mp)
		if err != nil {
			logger.Fatalf("Failed to lookup inode for %s: %s", mp, err)
		}
		if inode == 1 {
			break
		}
	}
	if mp == "/" {
		logger.Fatalf("Path %s is not inside JuiceFS", first)
	}

	controller := openController(mp)
	if controller == nil {
		logger.Fatalf("Failed to open control file under %s", mp)
	}
	defer controller.Close()

	threads := ctx.Uint("threads")
	background := ctx.Bool("background")
	start := len(mp)
	batch := make([]string, batchMax)
	progress, bar := utils.NewDynProgressBar("warming up paths: ", background)
	bar.SetTotal(int64(len(paths)), false)
	var index int
	for _, path := range paths {
		if strings.HasPrefix(path, mp) {
			batch[index] = path[start:]
			index++
		} else {
			logger.Warnf("Path %s is not under mount point %s", path, mp)
			continue
		}
		if index >= batchMax {
			sendCommand(controller, batch, index, threads, background)
			bar.IncrBy(index)
			index = 0
		}
	}
	if index > 0 {
		sendCommand(controller, batch, index, threads, background)
		bar.IncrBy(index)
	}
	bar.SetTotal(0, true)
	progress.Wait()

	return nil
}

func warmupFlags() *cli.Command {
	return &cli.Command{
		Name:      "warmup",
		Usage:     "build cache for target directories/files",
		ArgsUsage: "[PATH ...]",
		Action:    warmup,
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
