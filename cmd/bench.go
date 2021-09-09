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
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

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
	fsize, bsize     int      // file/block size in Bytes
	fcount, bcount   int      // file/block count
	wbar, rbar, sbar *mpb.Bar // progress bar for write/read/stat
}

type benchmark struct {
	tty        bool
	big, small *benchCase
	threads    int
	tmpdir     string
}

func (bc *benchCase) writeFiles(index int) {
	for i := 0; i < bc.fcount; i++ {
		fname := fmt.Sprintf("%s/%s.%d.%d", bc.bm.tmpdir, bc.name, index, i)
		fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		buf := make([]byte, bc.bsize)
		_, _ = rand.Read(buf)
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
		fname := fmt.Sprintf("%s/%s.%d.%d", bc.bm.tmpdir, bc.name, index, i)
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
		fname := fmt.Sprintf("%s/%s.%d.%d", bc.bm.tmpdir, bc.name, index, i)
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

// blockSize, bigSize in MiB; smallSize in KiB
func newBenchmark(tmpdir string, blockSize, bigSize, smallSize, smallCount, threads int) *benchmark {
	bm := &benchmark{threads: threads, tmpdir: tmpdir}
	if bigSize > 0 {
		bm.big = bm.newCase("bigfile", bigSize<<20, 1, blockSize<<20)
	}
	if smallSize > 0 && smallCount > 0 {
		bm.small = bm.newCase("smallfile", smallSize<<10, smallCount, blockSize<<20)
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
	if bm.tty {
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

func (bm *benchmark) printResult(result [][3]string) {
	var rawmax, max [3]int
	for _, l := range result {
		for i := 0; i < 3; i++ {
			if len(l[i]) > rawmax[i] {
				rawmax[i] = len(l[i])
			}
		}
	}
	max = rawmax
	if bm.tty {
		max[1] -= 11 // no color chars
		max[2] -= 11
	}

	var b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteByte('+')
		b.WriteString(strings.Repeat("-", max[i]+2))
	}
	b.WriteByte('+')
	divider := b.String()
	fmt.Println(divider)

	b.Reset()
	header := []string{"ITEM", "VALUE", "COST"}
	for i := 0; i < 3; i++ {
		b.WriteString(" | ")
		b.WriteString(padding(header[i], max[i], ' '))
	}
	b.WriteString(" |")
	fmt.Println(b.String()[1:])
	fmt.Println(divider)

	for _, l := range result {
		b.Reset()
		for i := 0; i < 3; i++ {
			b.WriteString(" | ")
			if spaces := rawmax[i] - len(l[i]); spaces > 0 {
				b.WriteString(strings.Repeat(" ", spaces))
			}
			b.WriteString(l[i])
		}
		b.WriteString(" |")
		fmt.Println(b.String()[1:])
	}
	fmt.Println(divider)
}

func bench(ctx *cli.Context) error {
	setLoggerLevel(ctx)

	/* --- Pre-check --- */
	if ctx.Uint("block-size") == 0 || ctx.Uint("threads") == 0 {
		return os.ErrInvalid
	}
	tmpdir := "/jfs"
	if ctx.NArg() > 0 {
		tmpdir = ctx.Args().First()
	}
	tmpdir = filepath.Join(tmpdir, fmt.Sprintf("__juicefs_benchmark_%d__", time.Now().UnixNano()))
	bm := newBenchmark(tmpdir, int(ctx.Uint("block-size")), int(ctx.Uint("big-file-size")),
		int(ctx.Uint("small-file-size")), int(ctx.Uint("small-file-count")), int(ctx.Uint("threads")))
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
	default:
		logger.Fatal("Currently only support Linux/macOS")
	}

	/* --- Prepare --- */
	if _, err := os.Stat(bm.tmpdir); os.IsNotExist(err) {
		if err = os.MkdirAll(bm.tmpdir, 0755); err != nil {
			logger.Fatalf("Failed to create %s: %s", bm.tmpdir, err)
		}
	}
	var statsPath string
	for mp := filepath.Dir(bm.tmpdir); mp != "/"; mp = filepath.Dir(mp) {
		if _, err := os.Stat(filepath.Join(mp, ".stats")); err == nil {
			statsPath = filepath.Join(mp, ".stats")
			break
		}
	}
	dropCaches := func() {
		if err := exec.Command(purgeArgs[0], purgeArgs[1:]...).Run(); err != nil {
			logger.Warnf("Failed to clean kernel caches: %s", err)
		}
	}
	if os.Getuid() != 0 {
		fmt.Println("Cleaning kernel cache, may ask for root privilege...")
	}
	dropCaches()
	var progress *mpb.Progress
	bm.tty = isatty.IsTerminal(os.Stdout.Fd())
	if bm.tty {
		progress = mpb.New(mpb.WithWidth(64))
	} else {
		progress = mpb.New(mpb.WithWidth(64), mpb.WithOutput(nil))
	}
	addBar := func(name string, total int) *mpb.Bar {
		return progress.AddBar(int64(total),
			mpb.PrependDecorators(
				decor.Name(name+" ", decor.WCSyncWidth),
				decor.CountersNoUnit("%d / %d"),
			),
			mpb.AppendDecorators(
				decor.OnComplete(decor.Percentage(decor.WC{W: 5}), "done"),
			),
		)
	}
	if b := bm.big; b != nil {
		total := bm.threads * b.fcount * b.bcount
		b.wbar, b.rbar = addBar("WriteBig", total), addBar("ReadBig", total)
	}
	if s := bm.small; s != nil {
		total := bm.threads * s.fcount * s.bcount
		s.wbar, s.rbar, s.sbar = addBar("WriteSmall", total), addBar("ReadSmall", total), addBar("Stat", bm.threads*s.fcount)
	}

	/* --- Run Benchmark --- */
	var stats map[string]float64
	if statsPath != "" {
		stats = readStats(statsPath)
	}
	var result [][3]string
	if b := bm.big; b != nil {
		cost := b.run("write")
		line := [3]string{"Write big file"}
		line[1], line[2] = bm.colorize("bigwr", float64((b.fsize>>20)*b.fcount*bm.threads)/cost, cost/float64(b.fcount), 2)
		line[1] += " MiB/s"
		line[2] += " s/file"
		result = append(result, line)
		dropCaches()

		cost = b.run("read")
		line[0] = "Read big file"
		line[1], line[2] = bm.colorize("bigrd", float64((b.fsize>>20)*b.fcount*bm.threads)/cost, cost/float64(b.fcount), 2)
		line[1] += " MiB/s"
		line[2] += " s/file"
		result = append(result, line)
	}
	if s := bm.small; s != nil {
		cost := s.run("write")
		line := [3]string{"Write small file"}
		line[1], line[2] = bm.colorize("smallwr", float64(s.fcount*bm.threads)/cost, cost*1000/float64(s.fcount), 1)
		line[1] += " files/s"
		line[2] += " ms/file"
		result = append(result, line)
		dropCaches()

		cost = s.run("read")
		line[0] = "Read small file"
		line[1], line[2] = bm.colorize("smallrd", float64(s.fcount*bm.threads)/cost, cost*1000/float64(s.fcount), 1)
		line[1] += " files/s"
		line[2] += " ms/file"
		result = append(result, line)
		dropCaches()

		cost = s.run("stat")
		line[0] = "Stat file"
		line[1], line[2] = bm.colorize("stat", float64(s.fcount*bm.threads)/cost, cost*1000/float64(s.fcount), 1)
		line[1] += " files/s"
		line[2] += " ms/file"
		result = append(result, line)
	}
	progress.Wait()

	/* --- Clean-up --- */
	if err := exec.Command("rm", "-rf", bm.tmpdir).Run(); err != nil {
		logger.Warnf("Failed to cleanup %s: %s", bm.tmpdir, err)
	}

	/* --- Report --- */
	fmt.Println("Benchmark finished!")
	fmt.Printf("BlockSize: %d MiB, BigFileSize: %d MiB, SmallFileSize: %d KiB, SmallFileCount: %d, NumThreads: %d\n",
		ctx.Uint("block-size"), ctx.Uint("big-file-size"), ctx.Uint("small-file-size"), ctx.Uint("small-file-count"), ctx.Uint("threads"))
	if stats != nil {
		stats2 := readStats(statsPath)
		diff := func(item string) float64 {
			return stats2["juicefs_"+item] - stats["juicefs_"+item]
		}
		show := func(title, nick, item string) {
			count := diff(item + "_total")
			var cost float64
			if count > 0 {
				cost = diff(item+"_sum") * 1000 / count
			}
			line := [3]string{title}
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
		if bm.tty {
			greenSeq := fmt.Sprintf("%s%dm", COLOR_SEQ, GREEN)
			fmtString = fmt.Sprintf("Time used: %s%%.1f%s s, CPU: %s%%.1f%s%%%%, Memory: %s%%.1f%s MiB\n",
				greenSeq, RESET_SEQ, greenSeq, RESET_SEQ, greenSeq, RESET_SEQ)
		} else {
			fmtString = "Time used: %.1f s, CPU: %.1f%%, Memory: %.1f MiB\n"
		}
		fmt.Printf(fmtString, diff("uptime"), diff("cpu_usage")*100/diff("uptime"), stats2["juicefs_memory"]/1024/1024)
	}
	bm.printResult(result)
	return nil
}

func benchFlags() *cli.Command {
	return &cli.Command{
		Name:      "bench",
		Usage:     "run benchmark to read/write/stat big/small files",
		Action:    bench,
		ArgsUsage: "PATH",
		Flags: []cli.Flag{
			&cli.UintFlag{
				Name:  "block-size",
				Value: 1,
				Usage: "block size in MiB",
			},
			&cli.UintFlag{
				Name:  "big-file-size",
				Value: 1024,
				Usage: "size of big file in MiB",
			},
			&cli.UintFlag{
				Name:  "small-file-size",
				Value: 128,
				Usage: "size of small file in KiB",
			},
			&cli.UintFlag{
				Name:  "small-file-count",
				Value: 100,
				Usage: "number of small files",
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
