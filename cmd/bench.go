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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdBench() *cli.Command {
	return &cli.Command{
		Name:      "bench",
		Action:    bench,
		Category:  "TOOL",
		Usage:     "Run benchmarks on a path",
		ArgsUsage: "PATH",
		Description: `
Run basic benchmarks on the target PATH to test if it works as expected. Results are colored with
green/yellow/red to indicate whether they are in a normal range. If you see any red value, please
double check relevant configuration before further test.

Examples:
# Run benchmarks with 4 threads
$ juicefs bench /mnt/jfs -p 4

# Run benchmarks of only small files
$ juicefs bench /mnt/jfs --big-file-size 0

Details: https://juicefs.com/docs/community/performance_evaluation_guide#juicefs-bench`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "block-size",
				Value: "1M",
				Usage: "size of each IO block in MiB",
			},
			&cli.StringFlag{
				Name:  "big-file-size",
				Value: "1G",
				Usage: "size of each big file in MiB",
			},
			&cli.StringFlag{
				Name:  "small-file-size",
				Value: "128K",
				Usage: "size of each small file in KiB",
			},
			&cli.UintFlag{
				Name:  "small-file-count",
				Value: 100,
				Usage: "number of small files per thread",
			},
			&cli.UintFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   1,
				Usage:   "number of concurrent threads",
			},
		},
	}
}

var resultRange = map[string][4]float64{
	"bigwr":   {100, 200, 10, 50},
	"bigrd":   {100, 200, 10, 50},
	"smallwr": {12.5, 20, 50, 80},
	"smallrd": {50, 100, 10, 20},
	"stat":    {20, 1000, 1, 5},
	"fuse":    {0, 0, 0.5, 2},
	"meta":    {0, 0, 2, 5},
	"put":     {0, 0, 100, 200},
	"get":     {0, 0, 100, 200},
	"delete":  {0, 0, 30, 100},
	"cachewr": {0, 0, 10, 20},
	"cacherd": {0, 0, 1, 5},
}

type benchCase struct {
	bm               *benchmark
	name             string
	fsize, bsize     int        // file/block size in Bytes
	fcount, bcount   int        // file/block count
	wbar, rbar, sbar *utils.Bar // progress bar for write/read/stat
}

type benchmark struct {
	colorful   bool
	big, small *benchCase
	threads    int
	tmpdir     string
}

func (bc *benchCase) writeFiles(index int) {
	for i := 0; i < bc.fcount; i++ {
		fname := filepath.Join(bc.bm.tmpdir, fmt.Sprintf("%s.%d.%d", bc.name, index, i))
		fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		buf := make([]byte, bc.bsize)
		utils.RandRead(buf)
		for j := 0; j < bc.bcount; j++ {
			if _, err = fp.Write(buf); err != nil {
				logger.Fatalf("Failed to write file %s: %s", fname, err)
			}
			bc.wbar.Increment()
		}
		_ = fp.Close()
	}
}

func (bc *benchCase) readFiles(index int) {
	for i := 0; i < bc.fcount; i++ {
		fname := filepath.Join(bc.bm.tmpdir, fmt.Sprintf("%s.%d.%d", bc.name, index, i))
		fp, err := os.Open(fname)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		buf := make([]byte, bc.bsize)
		for j := 0; j < bc.bcount; j++ {
			if n, err := fp.Read(buf); err != nil || n != bc.bsize {
				logger.Fatalf("Failed to read file %s: %d %s", fname, n, err)
			}
			bc.rbar.Increment()
		}
		_ = fp.Close()
	}
}

func (bc *benchCase) statFiles(index int) {
	for i := 0; i < bc.fcount; i++ {
		fname := filepath.Join(bc.bm.tmpdir, fmt.Sprintf("%s.%d.%d", bc.name, index, i))
		if _, err := os.Stat(fname); err != nil {
			logger.Fatalf("Failed to stat file %s: %s", fname, err)
		}
		bc.sbar.Increment()
	}
}

func (bc *benchCase) run(test string) float64 {
	var fn func(int)
	switch test {
	case "write":
		fn = bc.writeFiles
	case "read":
		fn = bc.readFiles
	case "stat":
		fn = bc.statFiles
	} // default: fatal
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < bc.bm.threads; i++ {
		index := i
		wg.Add(1)
		go func() {
			fn(index)
			wg.Done()
		}()
	}
	wg.Wait()
	return time.Since(start).Seconds()
}

func newBenchmark(tmpdir string, blockSize, bigSize, smallSize, smallCount, threads int) *benchmark {
	bm := &benchmark{threads: threads, tmpdir: tmpdir}
	if bigSize > 0 {
		bm.big = bm.newCase("bigfile", bigSize, 1, blockSize)
	}
	if smallSize > 0 && smallCount > 0 {
		bm.small = bm.newCase("smallfile", smallSize, smallCount, blockSize)
	}
	return bm
}

func (bm *benchmark) newCase(name string, fsize, fcount, bsize int) *benchCase {
	bc := &benchCase{
		bm:     bm,
		name:   name,
		fsize:  fsize,
		fcount: fcount,
		bsize:  bsize,
	}
	if fsize <= bsize {
		bc.bcount = 1
		bc.bsize = fsize
	} else {
		bc.bcount = (fsize-1)/bsize + 1
		bc.fsize = bc.bcount * bsize
	}
	return bc
}

func (bm *benchmark) colorize(item string, value, cost float64, prec int) (string, string) {
	svalue := strconv.FormatFloat(value, 'f', prec, 64)
	scost := strconv.FormatFloat(cost, 'f', 2, 64)
	if bm.colorful {
		r, ok := resultRange[item]
		if !ok {
			logger.Fatalf("Invalid item: %s", item)
		}
		if item == "smallwr" || item == "smallrd" || item == "stat" {
			r[0] *= float64(bm.threads)
			r[1] *= float64(bm.threads)
		}
		var color int
		if value > r[1] { // max
			color = GREEN
		} else if value > r[0] { // min
			color = YELLOW
		} else {
			color = RED
		}
		svalue = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, color, svalue, RESET_SEQ)
		if cost < r[2] { // min
			color = GREEN
		} else if cost < r[3] { // max
			color = YELLOW
		} else {
			color = RED
		}
		scost = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, color, scost, RESET_SEQ)
	}
	return svalue, scost
}

func printResult(result [][]string, leftAlign int, colorful bool) {
	if len(result) < 2 {
		logger.Fatalf("result must not be empty")
	}
	colNum := len(result[0])
	rawmax, max := make([]int, colNum), make([]int, colNum)
	for _, l := range result {
		for i := 0; i < colNum; i++ {
			if len(l[i]) > rawmax[i] {
				rawmax[i] = len(l[i])
			}
		}
	}
	copy(max, rawmax)
	if colorful {
		for i := 1; i < colNum; i++ {
			max[i] -= 11
		}
	}

	var b strings.Builder
	for i := 0; i < colNum; i++ {
		b.WriteByte('+')
		b.WriteString(strings.Repeat("-", max[i]+2))
	}
	b.WriteByte('+')
	divider := b.String()
	fmt.Println(divider)

	b.Reset()
	header := result[0]
	for i := 0; i < colNum; i++ {
		b.WriteString(" | ")
		b.WriteString(padding(header[i], max[i], ' '))
	}
	b.WriteString(" |")
	fmt.Println(b.String()[1:])
	fmt.Println(divider)

	for _, l := range result[1:] {
		b.Reset()
		for i := 0; i < colNum; i++ {
			b.WriteString(" | ")
			if i == leftAlign {
				b.WriteString(l[i])
			}
			if spaces := rawmax[i] - len(l[i]); spaces > 0 {
				b.WriteString(strings.Repeat(" ", spaces))
			}
			if i != leftAlign {
				b.WriteString(l[i])
			}
		}
		b.WriteString(" |")
		fmt.Println(b.String()[1:])
	}
	fmt.Println(divider)
}

func bench(ctx *cli.Context) error {
	setup(ctx, 1)
	/* --- Pre-check --- */
	blockSize := utils.ParseBytes(ctx, "block-size", 'M')
	if blockSize == 0 || ctx.Uint("threads") == 0 {
		return os.ErrInvalid
	}
	tmpdir, err := filepath.Abs(ctx.Args().First())
	if err != nil {
		logger.Fatalf("Failed to get absolute path of %s: %s", ctx.Args().First(), err)
	}
	bigSize := utils.ParseBytes(ctx, "big-file-size", 'M')
	smallSize := utils.ParseBytes(ctx, "small-file-size", 'K')
	tmpdir = filepath.Join(tmpdir, fmt.Sprintf("__juicefs_benchmark_%d__", time.Now().UnixNano()))
	bm := newBenchmark(tmpdir, int(blockSize), int(bigSize), int(smallSize),
		int(ctx.Uint("small-file-count")), int(ctx.Uint("threads")))
	if bm.big == nil && bm.small == nil {
		return os.ErrInvalid
	}
	var purgeArgs []string
	if os.Getuid() != 0 {
		purgeArgs = append(purgeArgs, "sudo")
	}
	switch runtime.GOOS {
	case "darwin":
		purgeArgs = append(purgeArgs, "purge")
	case "linux":
		purgeArgs = append(purgeArgs, "/bin/sh", "-c", "echo 3 > /proc/sys/vm/drop_caches")
	case "windows":
		break
	default:
		logger.Fatal("Currently only support Linux/MacOS/Windows")
	}

	/* --- Prepare --- */
	if _, err := os.Stat(bm.tmpdir); os.IsNotExist(err) {
		if err = os.MkdirAll(bm.tmpdir, 0777); err != nil {
			logger.Fatalf("Failed to create %s: %s", bm.tmpdir, err)
		}
	}
	mp, _ := findMountpoint(bm.tmpdir)
	dropCaches := func() {
		if os.Getenv("SKIP_DROP_CACHES") != "true" && runtime.GOOS != "windows" {
			if err := exec.Command(purgeArgs[0], purgeArgs[1:]...).Run(); err != nil {
				logger.Warnf("Failed to clean kernel caches: %s", err)
			}
		} else {
			logger.Warnf("Clear cache operation has been skipped")
		}
	}
	if os.Getuid() != 0 {
		fmt.Println("Cleaning kernel cache, may ask for root privilege...")
	}
	dropCaches()
	bm.colorful = utils.SupportANSIColor(os.Stdout.Fd())
	progress := utils.NewProgress(false)
	/* --- Run Benchmark --- */
	var stats map[string]float64
	if mp != "" {
		stats = readStats(mp)
	}
	var result [][]string
	result = append(result, []string{"ITEM", "VALUE", "COST"})
	if b := bm.big; b != nil {
		total := int64(bm.threads * b.fcount * b.bcount)
		b.wbar = progress.AddCountBar("Write big blocks", total)
		cost := b.run("write")
		b.wbar.Done()
		line := make([]string, 3)
		line[0] = "Write big file"
		line[1], line[2] = bm.colorize("bigwr", float64(b.fsize)/1024/1024*float64(b.fcount*bm.threads)/cost, cost/float64(b.fcount), 2)
		line[1] += " MiB/s"
		line[2] += " s/file"
		result = append(result, line)
		dropCaches()

		b.rbar = progress.AddCountBar("Read big blocks", total)
		cost = b.run("read")
		b.rbar.Done()
		line = make([]string, 3)
		line[0] = "Read big file"
		line[1], line[2] = bm.colorize("bigrd", float64(b.fsize)/1024/1024*float64(b.fcount*bm.threads)/cost, cost/float64(b.fcount), 2)
		line[1] += " MiB/s"
		line[2] += " s/file"
		result = append(result, line)
	}
	if s := bm.small; s != nil {
		total := int64(bm.threads * s.fcount * s.bcount)
		s.wbar = progress.AddCountBar("Write small blocks", total)
		cost := s.run("write")
		s.wbar.Done()
		line := make([]string, 3)
		line[0] = "Write small file"
		line[1], line[2] = bm.colorize("smallwr", float64(s.fcount*bm.threads)/cost, cost*1000/float64(s.fcount), 1)
		line[1] += " files/s"
		line[2] += " ms/file"
		result = append(result, line)
		dropCaches()

		s.rbar = progress.AddCountBar("Read small blocks", total)
		cost = s.run("read")
		s.rbar.Done()
		line = make([]string, 3)
		line[0] = "Read small file"
		line[1], line[2] = bm.colorize("smallrd", float64(s.fcount*bm.threads)/cost, cost*1000/float64(s.fcount), 1)
		line[1] += " files/s"
		line[2] += " ms/file"
		result = append(result, line)
		dropCaches()

		s.sbar = progress.AddCountBar("Stat small files", int64(bm.threads*s.fcount))
		cost = s.run("stat")
		s.sbar.Done()
		line = make([]string, 3)
		line[0] = "Stat file"
		line[1], line[2] = bm.colorize("stat", float64(s.fcount*bm.threads)/cost, cost*1000/float64(s.fcount), 1)
		line[1] += " files/s"
		line[2] += " ms/file"
		result = append(result, line)
	}
	progress.Done()

	/* --- Clean-up --- */
	if err := exec.Command("rm", "-rf", bm.tmpdir).Run(); err != nil {
		logger.Warnf("Failed to cleanup %s: %s", bm.tmpdir, err)
	}

	/* --- Report --- */
	fmt.Println("Benchmark finished!")
	fmt.Printf("BlockSize: %s, BigFileSize: %s, SmallFileSize: %s, SmallFileCount: %d, NumThreads: %d\n",
		humanize.IBytes(blockSize), humanize.IBytes(bigSize), humanize.IBytes(smallSize),
		ctx.Uint("small-file-count"), ctx.Uint("threads"))
	if stats != nil {
		stats2 := readStats(mp)
		diff := func(item string) float64 {
			return stats2["juicefs_"+item] - stats["juicefs_"+item]
		}
		show := func(title, nick, item string) {
			count := diff(item + "_total")
			var cost float64
			if count > 0 {
				cost = diff(item+"_sum") * 1000 / count
			}
			line := make([]string, 3)
			line[0] = title
			line[1], line[2] = bm.colorize(nick, count, cost, 0)
			line[1] += " operations"
			line[2] += " ms/op"
			result = append(result, line)
		}
		show("FUSE operation", "fuse", "fuse_ops_durations_histogram_seconds")
		show("Update meta", "meta", "transaction_durations_histogram_seconds")
		show("Put object", "put", "object_request_durations_histogram_seconds_PUT")
		show("Get object", "get", "object_request_durations_histogram_seconds_GET")
		show("Delete object", "delete", "object_request_durations_histogram_seconds_DELETE")
		show("Write into cache", "cachewr", "blockcache_write_hist_seconds")
		show("Read from cache", "cacherd", "blockcache_read_hist_seconds")
		var fmtString string
		if bm.colorful {
			greenSeq := fmt.Sprintf("%s%dm", COLOR_SEQ, GREEN)
			fmtString = fmt.Sprintf("Time used: %s%%.1f%s s, CPU: %s%%.1f%s%%%%, Memory: %s%%.1f%s MiB\n",
				greenSeq, RESET_SEQ, greenSeq, RESET_SEQ, greenSeq, RESET_SEQ)
		} else {
			fmtString = "Time used: %.1f s, CPU: %.1f%%, Memory: %.1f MiB\n"
		}
		fmt.Printf(fmtString, diff("uptime"), diff("cpu_usage")*100/diff("uptime"), stats2["juicefs_memory"]/1024/1024)
	}
	printResult(result, -1, bm.colorful)
	return nil
}
