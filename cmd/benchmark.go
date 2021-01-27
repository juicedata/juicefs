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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0o777)
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
	file, err := os.OpenFile(filename, os.O_RDONLY, 0o777)
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
	dest := c.String("dest")
	blockSize := c.Float64("block-size")
	bigFileSize := c.Float64("bigfile-file-size")
	smallFileSize := c.Float64("smallfile-file-size")
	smallFileCount := c.Int("smallfile-count")

	if c.NArg() > 0 {
		dest = c.Args().Get(0)
	}

	dest = filepath.Join(dest, fmt.Sprintf("__juicefs_benchmark_%d__", time.Now().UnixNano()))
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		err = os.MkdirAll(dest, 0o755)
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
	return nil
}

func benchmarkFlags() *cli.Command {
	return &cli.Command{
		Name:   "benchmark",
		Usage:  "run benchmark, including read/write/stat big/small files",
		Action: benchmark,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "dest",
				Value: "/jfs/benchmark",
				Usage: "path to run benchmark",
			},
			&cli.Float64Flag{
				Name:  "block-size",
				Value: 1,
				Usage: "block size in MiB",
			},
			&cli.Float64Flag{
				Name:  "bigfile-file-size",
				Value: 1024,
				Usage: "size of big file in MiB",
			},
			&cli.Float64Flag{
				Name:  "smallfile-file-size",
				Value: 0.1,
				Usage: "size of small file in MiB",
			},
			&cli.IntFlag{
				Name:  "smallfile-count",
				Value: 100,
				Usage: "number of small files",
			},
		},
	}
}
