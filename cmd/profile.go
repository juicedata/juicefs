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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdProfile() *cli.Command {
	return &cli.Command{
		Name:      "profile",
		Action:    profile,
		Category:  "INSPECTOR",
		Usage:     "Show profiling of operations completed in JuiceFS",
		ArgsUsage: "MOUNTPOINT/LOGFILE",
		Description: `
This is a tool that analyzes access log of JuiceFS and shows an overview of recently completed operations.

Examples:
# Monitor real time operations
$ juicefs profile /mnt/jfs

# Replay an access log
$ cat /mnt/jfs/.accesslog > /tmp/juicefs.accesslog
# Press Ctrl-C to stop the "cat" command after some time
$ juicefs profile /tmp/juicefs.accesslog

# Analyze an access log and print the total statistics immediately
$ juicefs profile /tmp/juicefs.accesslog --interval 0

Details: https://juicefs.com/docs/community/fault_diagnosis_and_analysis#profile`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "uid",
				Aliases: []string{"u"},
				Usage:   "track only specified UIDs(separated by comma ,)",
			},
			&cli.StringFlag{
				Name:    "gid",
				Aliases: []string{"g"},
				Usage:   "track only specified GIDs(separated by comma ,)",
			},
			&cli.StringFlag{
				Name:    "pid",
				Aliases: []string{"p"},
				Usage:   "track only specified PIDs(separated by comma ,)",
			},
			&cli.Int64Flag{
				Name:  "interval",
				Value: 2,
				Usage: "flush interval in seconds; set it to 0 when replaying a log file to get an immediate result",
			},
		},
	}
}

var findDigits = regexp.MustCompile(`\d+`)

type profiler struct {
	file      *os.File
	replay    bool
	colorful  bool
	interval  time.Duration
	uids      []string
	gids      []string
	pids      []string
	entryChan chan *logEntry // one line
	statsChan chan map[string]*stat
	pause     chan bool
	/* --- for replay --- */
	printTime chan time.Time
	done      chan bool
}

type stat struct {
	count int
	total int // total latency in 'us'
}

type keyStat struct {
	key  string
	sPtr *stat
}

type logEntry struct {
	ts            time.Time
	uid, gid, pid string
	op            string
	latency       int // us
}

func parseLine(line string) *logEntry {
	if len(line) < 3 { // dummy line: "#"
		return nil
	}
	fields := strings.Fields(line)
	if len(fields) < 5 {
		logger.Warnf("Log line is invalid: %s", line)
		return nil
	}
	ts, err := time.Parse("2006.01.02 15:04:05.000000", strings.Join([]string{fields[0], fields[1]}, " "))
	if err != nil {
		logger.Warnf("Failed to parse log line: %s: %s", line, err)
		return nil
	}
	ids := findDigits.FindAllString(fields[2], 3) // e.g: [uid:0,gid:0,pid:36674]
	if len(ids) != 3 {
		logger.Warnf("Log line is invalid: %s", line)
		return nil
	}
	latStr := fields[len(fields)-1] // e.g: <0.000003>
	latFloat, err := strconv.ParseFloat(latStr[1:len(latStr)-1], 64)
	if err != nil {
		logger.Warnf("Failed to parse log line: %s: %s", line, err)
		return nil
	}
	return &logEntry{
		ts:      ts,
		uid:     ids[0],
		gid:     ids[1],
		pid:     ids[2],
		op:      fields[3],
		latency: int(latFloat * 1000000.0),
	}
}

func (p *profiler) reader() {
	scanner := bufio.NewScanner(p.file)
	for scanner.Scan() {
		p.entryChan <- parseLine(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		logger.Fatalf("Reading log file failed with error: %s", err)
	}
	close(p.entryChan)
	if p.replay {
		p.done <- true
	}
}

func (p *profiler) isValid(entry *logEntry) bool {
	valid := func(f []string, e string) bool {
		if len(f) == 1 && f[0] == "" {
			return true
		}
		for _, v := range f {
			if v == e {
				return true
			}
		}
		return false
	}
	return valid(p.uids, entry.uid) && valid(p.gids, entry.gid) && valid(p.pids, entry.pid)
}

func (p *profiler) counter() {
	var edge time.Time
	stats := make(map[string]*stat)
	for {
		select {
		case entry := <-p.entryChan:
			if entry == nil {
				break
			}
			if !p.isValid(entry) {
				break
			}
			if p.replay {
				if edge.IsZero() {
					edge = entry.ts.Add(p.interval)
				}
				for ; entry.ts.After(edge); edge = edge.Add(p.interval) {
					p.statsChan <- stats
					p.printTime <- edge
					stats = make(map[string]*stat)
				}
			}
			value, ok := stats[entry.op]
			if !ok {
				value = &stat{}
				stats[entry.op] = value
			}
			value.count++
			value.total += entry.latency
		case p.statsChan <- stats:
			if p.replay {
				p.printTime <- edge
				edge = edge.Add(p.interval)
			}
			stats = make(map[string]*stat)
		}
	}
}

func (p *profiler) fastCounter() {
	var start, last time.Time
	stats := make(map[string]*stat)
	for entry := range p.entryChan {
		if entry == nil {
			continue
		}
		if !p.isValid(entry) {
			continue
		}
		if start.IsZero() {
			start = entry.ts
		}
		last = entry.ts
		value, ok := stats[entry.op]
		if !ok {
			value = &stat{}
			stats[entry.op] = value
		}
		value.count++
		value.total += entry.latency
	}
	p.statsChan <- stats
	p.printTime <- start
	p.printTime <- last
}

func colorize1(msg string, color int) string {
	return fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, color, msg, RESET_SEQ)
}

func printLines(lines []string, colorful bool) {
	if colorful {
		fmt.Print(CLEAR_SCREEM)
		fmt.Println(colorize1(lines[0], GREEN))
		fmt.Println(colorize1(lines[1], YELLOW))
		fmt.Println(colorize1(lines[2], BLUE))
		if len(lines) > 3 {
			for _, l := range lines[3:] {
				fmt.Println(colorize1(l, BLACK))
			}
		}
	} else {
		fmt.Println(lines[0])
		for _, l := range lines[2:] {
			fmt.Println(l)
		}
		fmt.Println()
	}
}

func (p *profiler) flush(timeStamp time.Time, keyStats []keyStat, done bool) {
	var head string
	if p.replay {
		if done {
			head = "(replay done)"
		} else {
			head = "(replaying)"
		}
	}
	output := make([]string, 3)
	output[0] = fmt.Sprintf("> JuiceFS Profiling %13s  Refresh: %.0f seconds %20s",
		head, p.interval.Seconds(), timeStamp.Format("2006-01-02T15:04:05"))
	output[2] = fmt.Sprintf("%-14s %10s %15s %18s %14s", "Operation", "Count", "Average(us)", "Total(us)", "Percent(%)")
	for _, s := range keyStats {
		output = append(output, fmt.Sprintf("%-14s %10d %15.0f %18d %14.1f",
			s.key, s.sPtr.count, float64(s.sPtr.total)/float64(s.sPtr.count), s.sPtr.total, float64(s.sPtr.total)/float64(p.interval.Microseconds())*100.0))
	}
	if p.replay {
		output[1] = fmt.Sprintln("\n[enter]Pause/Continue")
	}
	printLines(output, p.colorful)
}

func (p *profiler) flusher() {
	var paused, done bool
	ticker := time.NewTicker(p.interval)
	ts := time.Now()
	p.flush(ts, nil, false)
	for {
		select {
		case t := <-ticker.C:
			stats := <-p.statsChan
			if paused { // ticker event might be passed long ago
				paused = false
				ticker.Stop()
				ticker = time.NewTicker(p.interval)
				t = time.Now()
			}
			if done {
				ticker.Stop()
			}
			if p.replay {
				ts = <-p.printTime
			} else {
				ts = t
			}
			keyStats := make([]keyStat, 0, len(stats))
			for k, s := range stats {
				keyStats = append(keyStats, keyStat{k, s})
			}
			sort.Slice(keyStats, func(i, j int) bool { // reversed
				return keyStats[i].sPtr.total > keyStats[j].sPtr.total
			})
			p.flush(ts, keyStats, done)
			if done {
				os.Exit(0)
			}
		case paused = <-p.pause:
			fmt.Printf("\n\033[97mPaused. Press [enter] to continue.\n\033[0m")
			<-p.pause
		case done = <-p.done:
		}
	}
}

func profile(ctx *cli.Context) error {
	setup(ctx, 1)
	logPath := ctx.Args().First()
	st, err := os.Stat(logPath)
	if err != nil {
		logger.Fatalf("Failed to stat path %s: %s", logPath, err)
	}
	var replay bool
	if st.IsDir() { // mount point
		inode, err := utils.GetFileInode(logPath)
		if err != nil {
			logger.Fatalf("Failed to lookup inode for %s: %s", logPath, err)
		}
		if inode != uint64(meta.RootInode) {
			logger.Fatalf("Path %s is not a mount point!", logPath)
		}
		if p := filepath.Join(logPath, ".jfs.accesslog"); utils.Exists(p) {
			logPath = p
		} else {
			logPath = filepath.Join(logPath, ".accesslog")
		}
	} else { // log file to be replayed
		replay = true
	}
	nodelay := ctx.Int64("interval") == 0
	if nodelay && !replay {
		logger.Fatalf("Interval must be > 0 for real time mode!")
	}
	file, err := os.Open(logPath)
	if err != nil {
		logger.Fatalf("Failed to open log file %s: %s", logPath, err)
	}
	defer file.Close()

	prof := profiler{
		file:      file,
		replay:    replay,
		colorful:  utils.SupportANSIColor(os.Stdout.Fd()),
		interval:  time.Second * time.Duration(ctx.Int64("interval")),
		uids:      strings.Split(ctx.String("uid"), ","),
		gids:      strings.Split(ctx.String("gid"), ","),
		pids:      strings.Split(ctx.String("pid"), ","),
		entryChan: make(chan *logEntry, 16),
		statsChan: make(chan map[string]*stat),
		pause:     make(chan bool),
	}
	if prof.replay {
		prof.printTime = make(chan time.Time)
		prof.done = make(chan bool)
	}

	go prof.reader()
	if nodelay {
		go prof.fastCounter()
		stats := <-prof.statsChan
		start := <-prof.printTime
		last := <-prof.printTime
		keyStats := make([]keyStat, 0, len(stats))
		for k, s := range stats {
			keyStats = append(keyStats, keyStat{k, s})
		}
		sort.Slice(keyStats, func(i, j int) bool { // reversed
			return keyStats[i].sPtr.total > keyStats[j].sPtr.total
		})
		prof.replay = false
		prof.interval = last.Sub(start)
		prof.flush(last, keyStats, <-prof.done)
		return nil
	}

	go prof.counter()
	go prof.flusher()
	var input string
	for {
		_, _ = fmt.Scanln(&input)
		if prof.colorful {
			fmt.Print("\033[1A\033[K") // move cursor back
		}
		if prof.replay {
			prof.pause <- true // pause/continue
		}
	}
}
