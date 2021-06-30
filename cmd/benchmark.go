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
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
)

type Benchmark struct {
	filenamePrefix string
	fileSizeMiB    float64
	blockSizeMiB   float64
	count          int
	threads        int
}

func writeFiles(prefix string, fCount, bSize, bCount int, progress chan<- bool) {
	for i := 0; i < fCount; i++ {
		fname := fmt.Sprintf("%s.%d", prefix, i)
		fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		buf := make([]byte, bSize)
		rand.Read(buf)
		for j := 0; j < bCount; j++ {
			if _, err = fp.Write(buf); err != nil {
				logger.Fatalf("Failed to write file %s: %s", fname, err)
			}
			if j == bCount-1 {
				_ = fp.Close()
			}
			progress <- true
		}
	}
}

func readFiles(prefix string, fCount, bSize, bCount int, progress chan<- bool) {
	for i := 0; i < fCount; i++ {
		fname := fmt.Sprintf("%s.%d", prefix, i)
		fp, err := os.Open(fname)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		buf := make([]byte, bSize)
		for j := 0; j < bCount; j++ {
			if n, err := fp.Read(buf); err != nil || n != bSize {
				logger.Fatalf("Failed to read file %s: %d %s", fname, n, err)
			}
			progress <- true
		}
		fp.Close()
	}
}

func statFiles(prefix string, fCount int, progress chan<- bool) {
	for i := 0; i < fCount; i++ {
		fname := fmt.Sprintf("%s.%d", prefix, i)
		if _, err := os.Stat(fname); err != nil {
			logger.Fatalf("Failed to stat file %s: %s", fname, err)
		}
		progress <- true
	}
}

func (bm *Benchmark) ReadFileTest() float64 {
	blockCount := int(bm.fileSizeMiB / bm.blockSizeMiB)
	blockSize := int(bm.blockSizeMiB * (1 << 20))
	progress := make(chan bool, bm.threads)
	start := time.Now()
	for i := 0; i < bm.threads; i++ {
		prefix := fmt.Sprintf("%s.%d", bm.filenamePrefix, i)
		go readFiles(prefix, bm.count, blockSize, blockCount, progress)
	}
	totalBlocks := bm.threads * bm.count * blockCount
	for count := 0; count < totalBlocks; count++ {
		<-progress
		if count%100 == 0 && isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Printf("\rreading files: %.2f %%", float64(count*100)/float64(totalBlocks))
		}
	}
	return time.Since(start).Seconds()
}

func (bm *Benchmark) WriteFileTest() float64 {
	blockCount := int(bm.fileSizeMiB / bm.blockSizeMiB)
	blockSize := int(bm.blockSizeMiB * (1 << 20))
	progress := make(chan bool, bm.threads)
	start := time.Now()
	for i := 0; i < bm.threads; i++ {
		prefix := fmt.Sprintf("%s.%d", bm.filenamePrefix, i)
		go writeFiles(prefix, bm.count, blockSize, blockCount, progress)
	}
	totalBlocks := bm.threads * bm.count * blockCount
	for count := 0; count < totalBlocks; count++ {
		<-progress
		if count%100 == 0 && isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Printf("\rwriting files: %.2f %%", float64(count*100)/float64(totalBlocks))
		}
	}
	return time.Since(start).Seconds()
}

func (bm *Benchmark) StatFileTest() float64 {
	progress := make(chan bool, bm.threads)
	start := time.Now()
	for i := 0; i < bm.threads; i++ {
		prefix := fmt.Sprintf("%s.%d", bm.filenamePrefix, i)
		go statFiles(prefix, bm.count, progress)
	}
	totalFiles := bm.threads * bm.count
	for count := 0; count < totalFiles; count++ {
		<-progress
		if count%100 == 0 && isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Printf("\rstating files: %.2f %%", float64(count*100)/float64(totalFiles))
		}
	}
	return time.Since(start).Seconds()
}

func newBenchmark(filenamePrefix string, fileSize, blockSize float64, count, threads int) *Benchmark {
	bm := &Benchmark{
		filenamePrefix: filenamePrefix,
		count:          count,
		threads:        threads,
	}
	if fileSize < blockSize {
		bm.fileSizeMiB = fileSize
		bm.blockSizeMiB = fileSize
	} else {
		bm.fileSizeMiB = blockSize * math.Ceil(fileSize/blockSize)
		bm.blockSizeMiB = blockSize
	}
	return bm
}

func readStats(path string) map[string]float64 {
	f, err := os.Open(path)
	if err != nil {
		logger.Warnf("open %s: %s", path, err)
		return nil
	}
	d, err := ioutil.ReadAll(f)
	if err != nil {
		logger.Warnf("read %s: %s", path, err)
		return nil
	}
	stats := make(map[string]float64)
	lines := strings.Split(string(d), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			stats[fields[0]], err = strconv.ParseFloat(fields[1], 64)
			if err != nil {
				logger.Warnf("parse %s: %s", fields[1], err)
			}
		}
	}
	return stats
}

func benchmark(c *cli.Context) error {
	setLoggerLevel(c)

	var purgeCmdArgs []string
	if runtime.GOOS == "darwin" {
		purgeCmdArgs = append(purgeCmdArgs, "purge")
	} else if runtime.GOOS == "linux" {
		purgeCmdArgs = append(purgeCmdArgs, "/bin/sh", "-c", "echo 3 > /proc/sys/vm/drop_caches")
	} else {
		logger.Fatal("Currently only support Linux/macOS")
	}
	dest := "/jfs"
	blockSize := c.Float64("block-size")
	bigFileSize := c.Float64("big-file-size")
	smallFileSize := c.Float64("small-file-size")
	smallFileCount := c.Int("small-file-count")
	threads := c.Int("threads")

	if c.NArg() > 0 {
		dest = c.Args().Get(0)
	}

	var statPath string
	var stats map[string]float64
	for mp := dest; mp != "/"; mp = filepath.Dir(mp) {
		if _, err := os.Stat(filepath.Join(mp, ".stats")); err == nil {
			statPath = filepath.Join(mp, ".stats")
			stats = readStats(statPath)
			break
		}
	}

	dest = filepath.Join(dest, fmt.Sprintf("__juicefs_benchmark_%d__", time.Now().UnixNano()))
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		err = os.MkdirAll(dest, 0755)
		if err != nil {
			return err
		}
	}
	var purgeCmd *exec.Cmd
	if os.Getuid() != 0 {
		purgeCmd = exec.Command("sudo", purgeCmdArgs...)
	} else {
		purgeCmd = exec.Command(purgeCmdArgs[0], purgeCmdArgs[1:]...)
	}

	bm := newBenchmark(filepath.Join(dest, "bigfile"), bigFileSize, blockSize, 1, threads)
	cost := bm.WriteFileTest()
	fmt.Printf("\rWritten %d * 1 big files (%.2f MiB): (%.2f MiB/s)\n", bm.threads, bm.fileSizeMiB, bm.fileSizeMiB*float64(bm.count*bm.threads)/cost)

	if os.Getuid() != 0 {
		fmt.Println("Cleaning kernel cache, may ask for root privilege...")
	}
	if err := purgeCmd.Run(); err != nil {
		return err
	}
	if os.Getuid() != 0 {
		fmt.Println("Kernel cache cleaned")
	}

	cost = bm.ReadFileTest()
	fmt.Printf("\rRead %d * 1 big file (%.2f MiB): (%.2f MiB/s)\n", bm.threads, bm.fileSizeMiB, bm.fileSizeMiB*float64(bm.count*bm.threads)/cost)

	bm = newBenchmark(filepath.Join(dest, "smallfile"), smallFileSize, blockSize, smallFileCount, threads)
	cost = bm.WriteFileTest()
	fmt.Printf("\rWritten %d * %d small files (%.2f KiB): %.1f files/s, %.2f ms for each file\n", bm.threads, bm.count, bm.fileSizeMiB*1024, float64(bm.threads*bm.count)/cost, cost*1000/float64(bm.threads*bm.count))

	purgeCmd = exec.Command(purgeCmd.Path, purgeCmd.Args[1:]...)
	if err := purgeCmd.Run(); err != nil {
		return err
	}

	cost = bm.ReadFileTest()
	fmt.Printf("\rRead %d * %d small files (%.2f KiB): %.1f files/s, %.2f ms for each file\n", bm.threads, bm.count, bm.fileSizeMiB*1024, float64(bm.threads*bm.count)/cost, cost*1000/float64(bm.threads*bm.count))

	purgeCmd = exec.Command(purgeCmd.Path, purgeCmd.Args[1:]...)
	if err := purgeCmd.Run(); err != nil {
		return err
	}

	cost = bm.StatFileTest()
	fmt.Printf("\rStated %d * %d files: %.1f files/s, %.2f ms for each file\n", bm.threads, bm.count, float64(bm.threads*bm.count)/cost, cost*1000/float64(bm.threads*bm.count))

	rmrCmd := exec.Command("rm", "-rf", dest)
	if err := rmrCmd.Run(); err != nil {
		return err
	}

	if stats != nil {
		stats2 := readStats(statPath)
		st := func(n string) float64 {
			return stats2["juicefs_"+n] - stats["juicefs_"+n]
		}
		show := func(title, name string) {
			fmt.Printf("%s: %.0f, avg: %.2f ms\n", title, st(name+"_total"),
				st(name+"_sum")/st(name+"_total")*1000)
		}
		show("FUSE operation", "fuse_ops_durations_histogram_seconds")
		show("Update meta", "transaction_durations_histogram_seconds")
		show("Put object", "object_request_durations_histogram_seconds_PUT")
		show("Get object first byte", "object_request_durations_histogram_seconds_GET")
		show("Delete object", "object_request_durations_histogram_seconds_DELETE")
		show("Write into cache", "blockcache_write_hist_seconds")
		show("Read from cache", "blockcache_read_hist_seconds")
		fmt.Printf("Used: %.1fs, CPU: %.1f%%, MEM: %.1f MiB\n", st("uptime"),
			st("cpu_usage")*100/st("uptime"), stats2["juicefs_memory"]/1024/1024)
	}
	return nil
}

func benchmarkFlags() *cli.Command {
	return &cli.Command{
		Name:      "bench",
		Usage:     "run benchmark to read/write/stat big/small files",
		Action:    benchmark,
		ArgsUsage: "PATH",
		Flags: []cli.Flag{
			&cli.Float64Flag{
				Name:  "block-size",
				Value: 1,
				Usage: "block size in MiB",
			},
			&cli.Float64Flag{
				Name:  "big-file-size",
				Value: 1024,
				Usage: "size of big file in MiB",
			},
			&cli.Float64Flag{
				Name:  "small-file-size",
				Value: 0.1,
				Usage: "size of small file in MiB",
			},
			&cli.IntFlag{
				Name:  "small-file-count",
				Value: 100,
				Usage: "number of small files",
			},
			&cli.IntFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   1,
				Usage:   "number of concurrent threads",
			},
		},
	}
}
