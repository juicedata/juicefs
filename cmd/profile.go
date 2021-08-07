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
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
)

var findDigits = regexp.MustCompile(`\d+`)

type profiler struct {
	file      *os.File
	replay    bool
	tty       bool
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

func printLines(lines []string, tty bool) {
	if tty {
		fmt.Print("\033[2J\033[1;1H") // clear screen
		fmt.Printf("\033[92m%s\n\033[0m", lines[0])
		fmt.Printf("\033[97m%s\n\033[0m", lines[1])
		fmt.Printf("\033[94m%s\n\033[0m", lines[2])
		if len(lines) > 3 {
			for _, l := range lines[3:] {
				fmt.Printf("\033[93m%s\n\033[0m", l)
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
	printLines(output, p.tty)
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
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		logger.Fatalln("Mount point or log file must be provided!")
	}
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
		if inode != 1 {
			logger.Fatalf("Path %s is not a mount point!", logPath)
		}
		logPath = path.Join(logPath, ".accesslog")
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
		tty:       isatty.IsTerminal(os.Stdout.Fd()),
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
		fmt.Scanln(&input)
		if prof.tty {
			fmt.Print("\033[1A\033[K") // move cursor back
		}
		if prof.replay {
			prof.pause <- true // pause/continue
		}
	}
}

func profileFlags() *cli.Command {
	return &cli.Command{
		Name:      "profile",
		Usage:     "analyze access log",
		Action:    profile,
		ArgsUsage: "MOUNTPOINT/LOGFILE",
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
