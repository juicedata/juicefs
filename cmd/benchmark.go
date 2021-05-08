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
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"math"
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
	filenames      []string
	done           int
}

func (bm *Benchmark) writeOneFile(filename string, buf []byte, blockCount int) ([]float64, error) {
	timeTaken := make([]float64, 0, blockCount)
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0777)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	for i := 0; i < blockCount; i++ {
		start := time.Now()
		if _, err = file.Write(buf); err != nil {
			return nil, err
		}
		if i == blockCount-1 {
			file.Close()
		}
		cost := time.Since(start)
		timeTaken = append(timeTaken, cost.Seconds())
		bm.done += 1
		if isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Printf("\rwriting files: %.2f %%", float64(bm.done*100)/float64(bm.count*blockCount))
		}
	}
	return timeTaken, nil
}

func (bm *Benchmark) readOneFile(filename string, blockSize int, blockCount int) ([]float64, error) {
	timeTaken := make([]float64, 0, blockCount)
	file, err := os.OpenFile(filename, os.O_RDONLY, 0777)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, blockSize)
	for {
		start := time.Now()
		_, err = file.Read(buf)
		if err != nil {
			break
		}
		cost := time.Since(start)
		timeTaken = append(timeTaken, cost.Seconds())
		if isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Printf("\rreading files: %.2f %%", float64(bm.done*100)/float64(bm.count*blockCount))
		}

		bm.done += 1
	}
	return timeTaken, nil
}

func (bm *Benchmark) statOneFile(filename string) float64 {
	start := time.Now()
	_, _ = os.Stat(filename)
	bm.done += 1
	if isatty.IsTerminal(os.Stdout.Fd()) {
		fmt.Printf("\rstating files: %.2f %%", float64(bm.done*100)/float64(bm.count))
	}
	return time.Since(start).Seconds()
}

func (bm *Benchmark) ReadFileTest() ([]float64, float64) {
	blockCount := int(math.Ceil(bm.fileSizeMiB / bm.blockSizeMiB))
	blockSize := int(bm.blockSizeMiB * (1 << 20))
	timeTaken := make([]float64, 0, len(bm.filenames))
	bm.done = 0

	for _, filename := range bm.filenames {
		if costs, err := bm.readOneFile(filename, blockSize, blockCount); err == nil {
			timeTaken = append(timeTaken, costs...)
		} else {
			logger.Fatalf("Failed to read file %s: %s", filename, err)
		}
	}
	totalSizeMiB := bm.fileSizeMiB * float64(len(bm.filenames))
	return timeTaken, totalSizeMiB
}

func (bm *Benchmark) WriteFileTest() ([]float64, float64) {
	blockCount := int(math.Ceil(bm.fileSizeMiB / bm.blockSizeMiB))
	blockSize := int(math.Min(bm.blockSizeMiB, bm.fileSizeMiB) * (1 << 20))
	timeTaken := make([]float64, 10)
	bm.done = 0
	buf := make([]byte, blockSize)
	_, _ = rand.Read(buf)

	for i := 0; i < bm.count; i++ {
		filename := fmt.Sprintf("%s.%d", bm.filenamePrefix, i)
		if costs, err := bm.writeOneFile(filename, buf, blockCount); err == nil {
			timeTaken = append(timeTaken, costs...)
			bm.filenames = append(bm.filenames, filename)
		} else {
			logger.Fatalf("Failed to write file %s: %s", filename, err)
		}
	}
	totalSizeMiB := bm.fileSizeMiB * float64(bm.count)
	return timeTaken, totalSizeMiB

}

func (bm *Benchmark) StatFileTest() []float64 {
	bm.done = 0
	timeTaken := make([]float64, 0, len(bm.filenames))
	for _, filename := range bm.filenames {
		timeTaken = append(timeTaken, bm.statOneFile(filename))
	}
	return timeTaken
}

func newBenchmark(filenamePrefix string, fileSizeMiB float64, blockSize float64, count int) *Benchmark {
	return &Benchmark{
		filenamePrefix: filenamePrefix,
		fileSizeMiB:    fileSizeMiB,
		blockSizeMiB:   blockSize,
		count:          count,
		done:           0,
	}
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
		purgeCmdArgs = append([]string{"sudo"}, purgeCmdArgs...)
		purgeCmd = exec.Command("sudo", purgeCmdArgs...)
	} else {
		purgeCmd = exec.Command(purgeCmdArgs[0], purgeCmdArgs[1:]...)
	}

	sum := func(timeCost []float64) float64 {
		var s float64
		for _, t := range timeCost {
			s += t
		}
		return s
	}

	bigFileTest := newBenchmark(filepath.Join(dest, "bigfile"), bigFileSize, blockSize, 1)
	timeTaken, totalSizeMiB := bigFileTest.WriteFileTest()

	fmt.Printf("\rWritten a big file (%.2f MiB): (%.2f MiB/s)\n", bigFileTest.fileSizeMiB, totalSizeMiB/sum(timeTaken))

	if os.Getuid() != 0 {
		fmt.Println("Cleaning kernel cache, may ask for root privilege...")
	}
	if err := purgeCmd.Run(); err != nil {
		return err
	}
	if os.Getuid() != 0 {
		fmt.Println("Kernel cache cleaned")
	}
	timeTaken, totalSizeMiB = bigFileTest.ReadFileTest()
	fmt.Printf("\rRead a big file (%.2f MiB): (%.2f MiB/s)\n", bigFileTest.fileSizeMiB, totalSizeMiB/sum(timeTaken))

	smallFileTest := newBenchmark(filepath.Join(dest, "smallfile"), smallFileSize, blockSize, smallFileCount)
	timeTaken, _ = smallFileTest.WriteFileTest()
	fmt.Printf("\rWritten %d small files (%.2f KiB): %.1f files/s, %.1f ms for each file\n", smallFileTest.count, smallFileTest.fileSizeMiB*1024, float64(smallFileTest.count)/sum(timeTaken), sum(timeTaken)*1000/float64(smallFileTest.count))

	purgeCmd = exec.Command(purgeCmd.Path, purgeCmd.Args[1:]...)
	if err := purgeCmd.Run(); err != nil {
		return err
	}

	timeTaken, _ = smallFileTest.ReadFileTest()
	fmt.Printf("\rRead %d small files (%.2f KiB): %.1f files/s, %.1f ms for each file\n", smallFileTest.count, smallFileTest.fileSizeMiB*1024, float64(smallFileTest.count)/sum(timeTaken), sum(timeTaken)*1000/float64(smallFileTest.count))

	purgeCmd = exec.Command(purgeCmd.Path, purgeCmd.Args[1:]...)
	if err := purgeCmd.Run(); err != nil {
		return err
	}

	timeTaken = smallFileTest.StatFileTest()
	fmt.Printf("\rStated %d files: %.1f files/s, %.1f ms for each file\n", smallFileTest.count, float64(smallFileTest.count)/sum(timeTaken), sum(timeTaken)*1000/float64(smallFileTest.count))

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
			fmt.Printf("%s: %.0f, avg: %.1f ms\n", title, st(name+"_total"),
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
		},
	}
}
