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
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
)

const (
	BLACK = 30 + iota
	RED
	GREEN
	YELLOW
	BLUE
	MAGENTA
	// CYAN
	// GRAY
)

const (
	RESET_SEQ      = "\033[0m"
	COLOR_SEQ      = "\033[1;" // %dm
	COLOR_DARK_SEQ = "\033[0;" // %dm
	UNDERLINE_SEQ  = "\033[4m"
	// BOLD_SEQ       = "\033[1m"
)

const defaultSchema = `{
	"alloc": "go_memstats_alloc_bytes",
	"sys":   "go_memstats_sys_bytes",
	"Fop":   "juicefs_fuse_ops_durations_histogram_seconds",
	"Frd":   "juicefs_fuse_read_size_bytes",
	"Fwr":   "juicefs_fuse_written_size_bytes",
	"Mtx":   "juicefs_transaction_durations_histogram_seconds",
	"Crd":   "juicefs_blockcache_read_hist_seconds",
	"Cwr":   "juicefs_blockcache_write_hist_seconds",
	"mem":   "juicefs_memory"
}`

type statsWatcher struct {
	mp            string
	tty           bool
	interval      time.Duration
	schema        []*section
	last, current map[string]float64
}

type item struct {
	nick string
	name string
	typ  string // gauge, counter, histogram, summary
}

type section struct {
	name      string
	header    string
	subHeader string
	items     []*item
}

func (w *statsWatcher) colorize(msg string, color int, dark bool, underline bool) string {
	if !w.tty {
		return msg
	}
	var cseq, useq string
	if dark {
		cseq = COLOR_DARK_SEQ
	} else {
		cseq = COLOR_SEQ
	}
	if underline {
		useq = UNDERLINE_SEQ
	}
	return fmt.Sprintf("%s%s%dm%s%s", useq, cseq, color, msg, RESET_SEQ)
}

func (w *statsWatcher) loadSchema(schema string, listOnly bool) {
	/* --- load all schema --- */
	f := openController(w.mp)
	if f == nil {
		logger.Fatalf("open controller file: %s", w.mp)
	}
	defer f.Close()
	wb := utils.NewBuffer(8)
	wb.Put32(meta.MetricSchema)
	wb.Put32(0)
	if _, err := f.Write(wb.Bytes()); err != nil {
		logger.Fatalf("write message: %s", err)
	}
	data := make([]byte, 4)
	if n, err := f.Read(data); err != nil || n != 4 {
		logger.Fatalf("read size: %d %s", n, err)
	}
	rb := utils.ReadBuffer(data)
	size := rb.Get32()
	data = make([]byte, size)
	if n, err := f.Read(data); err != nil || n != int(size) {
		logger.Fatalf("read data: expect %d got %d error %s", size, n, err)
	}
	schemaAll := make(map[string]string)
	if err := json.Unmarshal(data, &schemaAll); err != nil {
		logger.Fatalf("load all schema: %s %s", data, err)
	}
	if listOnly {
		schemaList := make([]*item, 0, len(schemaAll))
		for name, typ := range schemaAll {
			schemaList = append(schemaList, &item{name: name, typ: typ})
		}
		sort.Slice(schemaList, func(i, j int) bool { return schemaList[i].name < schemaList[j].name })
		fmt.Printf("%-60s Type\n", "Name")
		for _, s := range schemaList {
			fmt.Printf("%-60s %s\n", s.name, s.typ)
		}
		return
	}

	/* --- load interested --- */
	interested := make(map[string]string)
	if err := json.Unmarshal([]byte(schema), &interested); err != nil {
		logger.Fatalf("load schema: %s %s", schema, err)
	}
	sectionMap := make(map[string]*section)
	for nick, name := range interested {
		if len(nick) > 5 {
			logger.Fatalf("nick %s is too long (must be <= 5)", nick)
		}
		typ, ok := schemaAll[name]
		if !ok {
			logger.Fatalf("field %s not found", name)
		}
		sectionName := name
		if index := strings.IndexByte(name, '_'); index > 0 {
			sectionName = name[:index]
		}
		s, ok := sectionMap[sectionName]
		if !ok {
			s = &section{name: sectionName}
			sectionMap[sectionName] = s
		}
		s.items = append(s.items, &item{nick, name, typ})
	}

	/* --- format headers --- */
	sections := make([]*section, 0, len(sectionMap))
	for _, s := range sectionMap {
		sort.Slice(s.items, func(i, j int) bool { return s.items[i].nick < s.items[j].nick })
		var width int
		var b strings.Builder // subHeader
		for _, it := range s.items {
			width += 6 // 5 chars + 1 space
			if l := len(it.nick); l < 5 {
				_, _ = b.WriteString(strings.Repeat(" ", 5-l))
			}
			_, _ = b.WriteString(w.colorize(it.nick, BLUE, false, true))
			_ = b.WriteByte(' ')
			if it.typ == "histogram" {
				width += 6
				_, _ = b.WriteString("  ")
				_, _ = b.WriteString(w.colorize("avg", BLUE, false, true))
				_ = b.WriteByte(' ')
			}
		}
		s.subHeader = b.String()[:b.Len()-1]
		width -= 1
		pad := width - len(s.name)
		if pad < 0 {
			pad = 0
			s.name = s.name[0:width]
		}
		prefix := pad / 2
		s.header = fmt.Sprintf("%s%s%s", strings.Repeat("-", prefix), s.name, strings.Repeat("-", pad-prefix))
		s.header = w.colorize(s.header, BLUE, true, false)
		sections = append(sections, s)
	}

	sort.Slice(sections, func(i, j int) bool { return sections[i].name < sections[j].name })
	w.schema = sections
}

func (w *statsWatcher) loadStats() {
	w.last = w.current
	w.current = readStats(path.Join(w.mp, ".stats"))
}

func (w *statsWatcher) printHeader() {
	headers := make([]string, len(w.schema))
	subHeaders := make([]string, len(w.schema))
	for i, s := range w.schema {
		headers[i] = s.header
		subHeaders[i] = s.subHeader
	}
	fmt.Printf("%s\n", strings.Join(headers, " "))
	fmt.Printf("%s\n", strings.Join(subHeaders, w.colorize("|", BLUE, true, false)))
}

func (w *statsWatcher) formatValue(v float64) string {
	var ret string
	switch {
	case v < 0.0:
		logger.Fatalf("invalid value %f", v)
	case v == 0.0:
		return w.colorize("    0", BLACK, false, false)
	case v < 100.0 && v-float64(int(v)) != 0.0:
		ret = fmt.Sprintf("%5.2f", v)
	case v < 1000.0 && v-float64(int(v)) != 0.0:
		ret = fmt.Sprintf("%5.1f", v)
	case v < 10000.0:
		ret = fmt.Sprintf("%5.f", v)
	}
	if ret != "" {
		return w.colorize(ret, RED, false, false)
	}

	switch vi := uint64(v); {
	case vi>>10 < 10000:
		return w.colorize(fmt.Sprintf("%4dK", vi>>10), YELLOW, false, false)
	case vi>>20 < 10000:
		return w.colorize(fmt.Sprintf("%4dM", vi>>20), GREEN, false, false)
	case vi>>30 < 10000:
		ret = fmt.Sprintf("%4dG", vi>>30)
	case vi>>40 < 10000:
		ret = fmt.Sprintf("%4dT", vi>>40)
	default:
		ret = fmt.Sprintf("%4dP", vi>>50)
	}
	return w.colorize(ret, MAGENTA, false, false)
}

func (w *statsWatcher) printDiff() {
	values := make([]string, len(w.schema))
	for i, s := range w.schema {
		var b strings.Builder
		for _, it := range s.items {
			switch it.typ {
			case "gauge":
				_, _ = b.WriteString(w.formatValue(w.current[it.name]))
			case "counter":
				_, _ = b.WriteString(w.formatValue(w.current[it.name] - w.last[it.name]))
			case "histogram":
				count := w.current[it.name+"_total"] - w.last[it.name+"_total"]
				_, _ = b.WriteString(w.formatValue(count))
				_ = b.WriteByte(' ')
				if count != 0 {
					cost := w.current[it.name+"_sum"] - w.last[it.name+"_sum"]
					if strings.Contains(it.name, "_seconds") { // FIXME: seconds -> ms
						cost *= 1000
					}
					_, _ = b.WriteString(w.formatValue(cost / count))
				} else {
					_, _ = b.WriteString(w.formatValue(0.0))
				}
			case "summary":
			}
			_ = b.WriteByte(' ')
		}
		values[i] = b.String()[:b.Len()-1]
	}
	fmt.Printf("%s\n", strings.Join(values, w.colorize("|", BLUE, true, false)))
}

func stats(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		logger.Fatalln("mount point must be provided")
	}
	mp := ctx.Args().First()
	inode, err := utils.GetFileInode(mp)
	if err != nil {
		logger.Fatalf("lookup inode for %s: %s", mp, err)
	}
	if inode != 1 {
		logger.Fatalf("path %s is not a mount point", mp)
	}

	watcher := &statsWatcher{
		mp:       mp,
		tty:      !ctx.Bool("nocolor") && isatty.IsTerminal(os.Stdout.Fd()),
		interval: time.Second * time.Duration(ctx.Int64("interval")),
		last:     make(map[string]float64),
		current:  make(map[string]float64),
	}
	var count int
	schema := ctx.String("schema")
	if schema == "" {
		schema = defaultSchema
	}
	watcher.loadSchema(schema, ctx.Bool("list"))
	if ctx.Bool("list") {
		return nil
	}
	watcher.loadStats()
	for {
		if count%30 == 0 {
			watcher.printHeader()
		}
		count++
		time.Sleep(watcher.interval)
		watcher.loadStats()
		watcher.printDiff()
	}
}

func statsFlags() *cli.Command {
	return &cli.Command{
		Name:      "stats",
		Usage:     "show runtime stats",
		Action:    stats,
		ArgsUsage: "MOUNTPOINT",
		Flags: []cli.Flag{
			&cli.Int64Flag{
				Name:  "interval",
				Value: 1,
				Usage: "interval in seconds between each update",
			},
			&cli.StringFlag{
				Name:  "schema",
				Usage: "displayed schema defined as a JSON string",
			},
			&cli.BoolFlag{
				Name:  "nocolor",
				Usage: "disable colors",
			},
			&cli.BoolFlag{
				Name:    "list",
				Aliases: []string{"l"},
				Usage:   "list available schemas and exit",
			},
		},
	}
}
