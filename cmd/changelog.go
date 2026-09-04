/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func cmdChangelog() *cli.Command {
	return &cli.Command{
		Name:            "changelog",
		Action:          changelog,
		Category:        "INSPECTOR",
		Usage:           "Tail the changelog of a volume",
		ArgsUsage:       "META-URL",
		HideHelpCommand: true,
		Description: `
Show the changelog of metadata operations on the volume. This requires the changelog feature
to be enabled via "juicefs config META-URL --changelog".

Examples:
$ juicefs changelog redis://localhost

# Start tailing from a specific version
$ juicefs changelog redis://localhost --from 100

# Validate that every entry can be parsed, without writing to the destination
$ juicefs changelog apply redis://src redis://dst --dry-run`,
		Subcommands: []*cli.Command{
			{
				Name:      "apply",
				Usage:     "Apply the changelog of a volume to another volume",
				ArgsUsage: "SRC-META-URL DST-META-URL",
				Action:    changelogApply,
				Description: `
Apply metadata changes recorded in the changelog of SRC-META-URL to DST-META-URL, to keep the
latter as an incrementally synchronized copy of the former.

The destination volume must be a copy of the source created with "juicefs dump --binary" and
"juicefs load", and both volumes must share the same object storage: the changelog only carries
metadata, never file data.

Examples:
$ juicefs changelog apply redis://src redis://dst --from 100
$ juicefs changelog apply redis://src redis://dst --dry-run --from 100`,
			},
		},
		Flags: []cli.Flag{
			&cli.Int64Flag{
				Name:  "from",
				Usage: "show changelog from this version (0 means from the latest)",
			},
			&cli.BoolFlag{
				Name:  "follow",
				Value: true,
				Usage: "keep waiting for new entries after catching up",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "apply: only parse and validate the changelog, do not write to the destination",
			},
			&cli.Int64Flag{
				Name:  "limit",
				Usage: "apply: stop after this number of entries (0 means unlimited)",
			},
			&cli.StringFlag{
				Name:  "on-error",
				Value: "stop",
				Usage: "apply: what to do with an entry that cannot be parsed or applied: stop or skip",
			},
		},
	}
}

func changelog(ctx *cli.Context) error {
	setup(ctx, 1)
	metaUri := ctx.Args().Get(0)
	removePassword(metaUri)

	m := meta.NewClient(metaUri, nil)
	if format, err := m.Load(true); err != nil {
		return err
	} else if !format.ChangeLog {
		return fmt.Errorf("changelog is not enabled, use `juicefs config %s --changelog` to enable it", metaUri)
	}

	opt := &meta.ChangelogScanOption{From: ctx.Int64("from"), Follow: ctx.Bool("follow")}
	return m.ScanChangelog(meta.WrapContext(ctx.Context), opt, func(ver int64, entry string) error {
		fmt.Printf("%d: %s\n", ver, entry)
		return nil
	})
}

type changelogApplyStats struct {
	start    time.Time
	entries  int64
	firstVer int64
	lastVer  int64
	lastTime time.Time
	ops      map[string]int64
	failed   int64
	unknown  map[string]int64
}

func (s *changelogApplyStats) record(e *meta.ChangeEntry) {
	if s.entries == 0 {
		s.firstVer = e.Ver
	}
	s.entries++
	s.lastVer = e.Ver
	s.lastTime = e.Time
	s.ops[e.Op]++
}

func (s *changelogApplyStats) report() {
	elapsed := time.Since(s.start)
	fmt.Printf("\nScanned %d entries in %s", s.entries, elapsed.Truncate(time.Millisecond))
	if s.entries > 0 {
		fmt.Printf(" (version %d..%d, last entry at %s)", s.firstVer, s.lastVer, s.lastTime.Format(time.RFC3339Nano))
	}
	fmt.Println()

	ops := make([]string, 0, len(s.ops))
	for op := range s.ops {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool { return s.ops[ops[i]] > s.ops[ops[j]] })
	for _, op := range ops {
		fmt.Printf("  %-28s %d\n", op, s.ops[op])
	}
	if len(s.unknown) > 0 {
		fmt.Printf("Unknown operations (%d kinds):\n", len(s.unknown))
		for op, n := range s.unknown {
			fmt.Printf("  %-28s %d\n", op, n)
		}
	}
	if s.failed > 0 {
		fmt.Printf("Failed entries: %d\n", s.failed)
	}
}

func changelogApply(ctx *cli.Context) error {
	setup(ctx, 2)
	srcUri, dstUri := ctx.Args().Get(0), ctx.Args().Get(1)
	removePassword(srcUri)
	removePassword(dstUri)

	onError := ctx.String("on-error")
	if onError != "stop" && onError != "skip" {
		return fmt.Errorf("invalid --on-error %q, expect stop or skip", onError)
	}
	src := meta.NewClient(srcUri, nil)
	srcFormat, err := src.Load(true)
	if err != nil {
		return err
	}
	if !srcFormat.ChangeLog {
		return fmt.Errorf("changelog is not enabled on the source, use `juicefs config %s --changelog` to enable it", srcUri)
	}
	dst := meta.NewClient(dstUri, nil)
	dstFormat, err := dst.Load(true)
	if err != nil {
		return err
	}
	if err := checkChangelogApplyFormat(srcFormat, dstFormat); err != nil {
		return err
	}

	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopped)

	limit := ctx.Int64("limit")
	stats := &changelogApplyStats{start: time.Now(), ops: make(map[string]int64), unknown: make(map[string]int64)}
	defer stats.report()

	nextLog := time.Now().Add(time.Minute)
	applyCtx := meta.WrapContext(ctx.Context)
	opt := &meta.ChangelogScanOption{From: ctx.Int64("from"), Follow: ctx.Bool("follow")}
	return src.ScanChangelog(applyCtx, opt, func(ver int64, entry string) error {
		select {
		case <-stopped:
			return meta.ErrChangelogStop
		default:
		}
		e, err := meta.ParseChangeEntry(ver, entry)
		if err == nil {
			if err = e.Validate(); errors.Is(err, meta.ErrUnknownChangelogOp) {
				stats.unknown[e.Op]++
			}
		}
		if err == nil && !ctx.Bool("dry-run") {
			err = meta.Apply(applyCtx, dst, e)
		}
		if err != nil {
			stats.failed++
			logger.Errorf("changelog %d: %s", ver, err)
			if onError == "stop" {
				return err
			}
		} else {
			stats.record(e)
		}
		if time.Now().After(nextLog) {
			nextLog = time.Now().Add(time.Minute)
			logger.Infof("Scanned %d entries, at version %d", stats.entries, stats.lastVer)
		}
		if limit > 0 && stats.entries+stats.failed >= limit {
			return meta.ErrChangelogStop
		}
		return nil
	})
}

func checkChangelogApplyFormat(src, dst *meta.Format) error {
	for _, f := range []struct {
		name     string
		src, dst interface{}
	}{
		{"block size", src.BlockSize, dst.BlockSize},
		{"compression", src.Compression, dst.Compression},
		{"shards", src.Shards, dst.Shards},
		{"hash prefix", src.HashPrefix, dst.HashPrefix},
		{"meta version", src.MetaVersion, dst.MetaVersion},
		{"encrypt algorithm", src.EncryptAlgo, dst.EncryptAlgo},
	} {
		if f.src != f.dst {
			return fmt.Errorf("%s of the destination (%v) does not match the source (%v)", f.name, f.dst, f.src)
		}
	}
	if src.Storage != dst.Storage || src.Bucket != dst.Bucket {
		logger.Warnf("Source and destination use different object storage (%s/%s vs %s/%s); the changelog only carries metadata, so the destination would reference blocks it does not have",
			src.Storage, src.Bucket, dst.Storage, dst.Bucket)
	}
	return nil
}
