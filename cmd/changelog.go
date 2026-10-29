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
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

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
$ juicefs changelog replay redis://src redis://dst --dry-run`,
		Subcommands: []*cli.Command{
			{
				Name:      "replay",
				Usage:     "Replay the changelog of a volume on another volume",
				ArgsUsage: "SRC-META-URL DST-META-URL",
				Action:    changelogReplay,
				Description: `
Replay the changelog of SRC-META-URL on DST-META-URL.

Examples:
# Start from the backup used to create the destination
$ juicefs changelog replay redis://src redis://dst --backup /tmp/meta.bin

# Resume from the replay progress log in the current directory
$ juicefs changelog replay redis://src redis://dst

# Store the replay progress log in a custom directory
$ juicefs changelog replay redis://src redis://dst --log-dir /tmp --backup /tmp/meta.bin

# Start from a specific version when no progress has been saved
$ juicefs changelog replay redis://src redis://dst --from 100

# Only parse and validate entries
$ juicefs changelog replay redis://src redis://dst --dry-run --from 100`,
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "log-dir",
				Value: ".",
				Usage: "directory for replay progress log files named changelog-replay-<volume UUID/destination hash>.log",
			},
			&cli.StringFlag{
				Name:  "backup",
				Usage: "path to the baseline JSON or binary backup (overrides --from)",
			},
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
				Usage: "only parse and validate the changelog, do not write to the destination",
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

func changelogReplay(ctx *cli.Context) (err error) {
	setup(ctx, 2)
	srcUri, dstUri := ctx.Args().Get(0), ctx.Args().Get(1)
	removePassword(srcUri)
	removePassword(dstUri)

	if srcUri == dstUri {
		return fmt.Errorf("source and destination metadata URLs must be different")
	}
	src := meta.NewClient(srcUri, nil)
	srcFormat, err := src.Load(true)
	if err != nil {
		return err
	}
	if !srcFormat.ChangeLog {
		return fmt.Errorf("changelog is not enabled on the source, use `juicefs config %s --changelog` to enable it", srcUri)
	}
	dryRun := ctx.Bool("dry-run")
	signalCtx, stop := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	scanCtx := meta.WrapContext(signalCtx)
	defer scanCtx.Cancel()
	var replayer *meta.Replayer
	var progress *meta.ReplayProgress
	var logPath string
	if !dryRun {
		replayHash := fmt.Sprintf("%x", sha256.Sum256([]byte(srcFormat.UUID+"\x00"+dstUri)))
		logPath = filepath.Join(ctx.String("log-dir"), "changelog-replay-"+replayHash+".log")
		replayer, err = meta.NewReplayer(dstUri, src, !ctx.Bool("follow"))
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := replayer.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close replayer: %w", closeErr))
			}
		}()
		progress, err = replayer.LoadProgress(scanCtx, logPath)
		if errors.Is(err, context.Canceled) {
			logger.Infof("Changelog replay stopped while loading progress")
			return nil
		}
		if err != nil {
			return fmt.Errorf("load replay progress log %q: %w", logPath, err)
		}
		if progress != nil {
			logger.Infof("Read replay progress log %s at version %d with %d rewind entries", logPath, progress.Version, len(progress.Seen))
		}
	}
	opt := &meta.ChangelogScanOption{From: ctx.Int64("from"), Follow: ctx.Bool("follow"), Seen: make(map[uint64]struct{})}
	if progress != nil {
		opt.From, opt.Seen = progress.Version, progress.Seen
		for _, flag := range []string{"from", "backup"} {
			if ctx.IsSet(flag) {
				logger.Warnf("Ignoring --%s because replay progress is already saved at version %d", flag, progress.Version)
			}
		}
	} else if path := ctx.String("backup"); path != "" {
		format, err := func() (*meta.Format, error) {
			r, err := open(path, "", "")
			if err != nil {
				return nil, err
			}
			defer r.Close()
			format, from, seen, err := meta.LoadChangelogBaseline(r)
			if err != nil {
				return nil, err
			}
			opt.From, opt.Seen = from, seen
			logger.Infof("Read backup lastChangelog %d with %d rewind entries", from, len(seen))
			return format, nil
		}()
		if err != nil {
			return fmt.Errorf("read changelog baseline from backup: %w", err)
		}
		if format.UUID != srcFormat.UUID {
			return fmt.Errorf("backup volume UUID %q does not match the source %q", format.UUID, srcFormat.UUID)
		}
	}
	if opt.From <= 0 {
		return fmt.Errorf("provide the backup used to create the destination with --backup, or an explicit positive --from")
	}
	if progress == nil && !dryRun {
		if err := replayer.InitProgressLog(logPath, opt.From, opt.Seen); err != nil {
			return fmt.Errorf("initialize replay progress log: %w", err)
		}
	}
	opt.Seen[uint64(opt.From)] = struct{}{}

	logger.Infof("Start changelog replay: from=%d follow=%t dry-run=%t", opt.From, opt.Follow, dryRun)
	lastVersion := opt.From
	replayCtx := meta.WrapContext(context.WithoutCancel(signalCtx))
	defer replayCtx.Cancel()
	err = src.ScanChangelog(scanCtx, opt, func(ver int64, entry string) error {
		if err := scanCtx.Err(); err != nil {
			return err
		}
		logger.Debugf("Changelog entry: version=%d entry=%q", ver, entry)
		e, err := meta.ParseChangeEntry(ver, entry)
		op := "unknown"
		if err == nil {
			op = e.Op
			err = e.Validate()
		}
		if err == nil && !dryRun {
			err = replayer.Replay(replayCtx, e)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			logger.Errorf("Failed to replay changelog: version=%d operation=%s error=%s", ver, op, err)
			return err
		} else if dryRun {
			logger.Infof("Validated changelog: version=%d operation=%s dry-run=true", ver, op)
		}
		lastVersion = max(lastVersion, ver)
		return nil
	})
	if errors.Is(err, context.Canceled) {
		logger.Infof("Changelog replay stopped: last processed version=%d", lastVersion)
		return nil
	}
	if err == nil {
		logger.Infof("Changelog replay finished: last processed version=%d", lastVersion)
	}
	return err
}
