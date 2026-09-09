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
	"io"
	"os"
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

Use --backup to read lastChangelog and the KV rewind deduplication entries from the binary
backup used to create the destination. When specified, its lastChangelog overrides --from.

Examples:
$ juicefs changelog apply redis://src redis://dst --backup /tmp/meta.bin
$ juicefs changelog apply redis://src redis://dst --from 100
$ juicefs changelog apply redis://src redis://dst --dry-run --from 100`,
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "backup",
				Usage: "apply: path to the baseline binary backup (overrides --from)",
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
				Usage: "apply: only parse and validate the changelog, do not write to the destination",
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
	opt := &meta.ChangelogScanOption{From: ctx.Int64("from"), Follow: ctx.Bool("follow")}
	if path := ctx.String("backup"); path != "" {
		format, err := func() (*meta.Format, error) {
			r, err := open(path, "", "")
			if err != nil {
				return nil, err
			}
			defer r.Close()
			backupReader, ok := r.(*reader).compressR.(io.ReadSeeker)
			if !ok {
				fp, err := os.CreateTemp("", "juicefs-changelog-backup-*.bin")
				if err != nil {
					return nil, err
				}
				defer os.Remove(fp.Name())
				defer fp.Close()
				if _, err := io.Copy(fp, r); err != nil {
					return nil, fmt.Errorf("decompress backup: %w", err)
				}
				backupReader = fp
			}
			return opt.LoadBackup(backupReader)
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

	dryRun := ctx.Bool("dry-run")
	logger.Infof("Start changelog apply: from=%d follow=%t dry-run=%t on-error=%s", opt.From, opt.Follow, dryRun, onError)
	applyCtx := meta.WrapContext(signalCtx)
	err = src.ScanChangelog(applyCtx, opt, func(ver int64, entry string) error {
		if err := applyCtx.Err(); err != nil {
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
			err = meta.Apply(applyCtx, dst, e)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if onError == "stop" {
				logger.Errorf("Failed to apply changelog: version=%d operation=%s error=%s", ver, op, err)
				return err
			}
			logger.Warnf("Skipped changelog after error: version=%d operation=%s error=%s", ver, op, err)
		} else if dryRun {
			logger.Infof("Validated changelog: version=%d operation=%s dry-run=true", ver, op)
		}
		return nil
	})
	if errors.Is(err, context.Canceled) {
		logger.Infof("Changelog apply stopped")
		return nil
	}
	if err == nil {
		logger.Infof("Changelog apply finished")
	}
	return err
}

func checkChangelogApplyFormat(src, dst *meta.Format) error {
	if src.UUID == "" || dst.UUID == "" {
		return fmt.Errorf("source and destination must have a volume UUID")
	}
	for _, f := range []struct {
		name     string
		src, dst interface{}
	}{
		{"volume UUID", src.UUID, dst.UUID},
		{"volume name", src.Name, dst.Name},
		{"storage", src.Storage, dst.Storage},
		{"bucket", src.Bucket, dst.Bucket},
		{"storage class", src.StorageClass, dst.StorageClass},
		{"tiers", src.Tiers, dst.Tiers},
		{"block size", src.BlockSize, dst.BlockSize},
		{"compression", src.Compression, dst.Compression},
		{"shards", src.Shards, dst.Shards},
		{"hash prefix", src.HashPrefix, dst.HashPrefix},
		{"meta version", src.MetaVersion, dst.MetaVersion},
		{"encrypt algorithm", src.EncryptAlgo, dst.EncryptAlgo},
		{"trash days", src.TrashDays, dst.TrashDays},
		{"ACL", src.EnableACL, dst.EnableACL},
		{"dir stats", src.DirStats, dst.DirStats},
		{"user group quota", src.UserGroupQuota, dst.UserGroupQuota},
		{"capacity", src.Capacity, dst.Capacity},
		{"inodes", src.Inodes, dst.Inodes},
	} {
		if !reflect.DeepEqual(f.src, f.dst) {
			return fmt.Errorf("%s of the destination (%v) does not match the source (%v)", f.name, f.dst, f.src)
		}
	}
	srcCopy, dstCopy := *src, *dst
	for _, format := range []*meta.Format{&srcCopy, &dstCopy} {
		format.SecretKey, format.SessionToken = "", ""
		if format.EncryptKey == "removed" {
			return fmt.Errorf("volume encryption key was removed; restore it before applying changelogs")
		}
		if err := format.Decrypt(); err != nil {
			return fmt.Errorf("decrypt volume encryption key: %w", err)
		}
	}
	if srcCopy.EncryptKey != dstCopy.EncryptKey {
		return fmt.Errorf("encryption key of the destination does not match the source")
	}
	return nil
}
