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
	"errors"
	"fmt"
	"os/signal"
	"reflect"
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

# Start from a specific version
$ juicefs changelog replay redis://src redis://dst --from 100

# Only parse and validate entries
$ juicefs changelog replay redis://src redis://dst --dry-run --from 100`,
			},
		},
		Flags: []cli.Flag{
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

func changelogReplay(ctx *cli.Context) error {
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
	conf := meta.DefaultConf()
	conf.NoCompact = true
	conf.NoBGJob = true
	conf.MaxDeletes = 0
	conf.ReadOnly = dryRun
	dst := meta.NewClient(dstUri, conf)
	dstFormat, err := dst.Load(true)
	if err != nil {
		return err
	}
	if err := validateCompatibility(srcFormat, dstFormat); err != nil {
		return err
	}
	opt := &meta.ChangelogScanOption{From: ctx.Int64("from"), Follow: ctx.Bool("follow")}
	if path := ctx.String("backup"); path != "" {
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
			return format, nil
		}()
		if err != nil {
			return fmt.Errorf("read changelog baseline from backup: %w", err)
		}
		if format.UUID != srcFormat.UUID {
			return fmt.Errorf("backup volume UUID %q does not match the source %q", format.UUID, srcFormat.UUID)
		}
		logger.Infof("Start from backup lastChangelog %d with %d rewind entries", opt.From, len(opt.Seen))
	}

	signalCtx, stop := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Infof("Start changelog replay: from=%d follow=%t dry-run=%t", opt.From, opt.Follow, dryRun)
	replayCtx := meta.WrapContext(signalCtx)
	if !dryRun {
		if err := dst.NewSession(false); err != nil {
			return fmt.Errorf("initialize destination metadata client: %w", err)
		}
		defer dst.CloseSession()
	}
	lastVersion := opt.From
	err = src.ScanChangelog(replayCtx, opt, func(ver int64, entry string) error {
		if err := replayCtx.Err(); err != nil {
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
			err = meta.Replay(replayCtx, dst, e)
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
		lastVersion = ver
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

// validateCompatibility requires the destination to be a copy of the source.
func validateCompatibility(src, dst *meta.Format) error {
	s, d := *src, *dst
	for _, f := range []*meta.Format{&s, &d} {
		f.SecretKey, f.SessionToken = "", ""
		if err := f.Decrypt(); err != nil {
			return fmt.Errorf("decrypt volume encryption key: %w", err)
		}
		// Fields that may differ between a volume and its copy.
		f.AccessKey = ""
		f.UploadLimit, f.DownloadLimit = 0, 0
		f.MinClientVersion, f.MaxClientVersion = "", ""
		f.ChangeLog, f.ChangeLogMaxAge, f.ChangeLogMaxLines = false, 0, 0
	}
	if !reflect.DeepEqual(s, d) {
		return fmt.Errorf("destination is not a copy of the source:\nsource: %s\ndestination: %s", &s, &d)
	}
	return nil
}
