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
	"compress/gzip"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/DataDog/zstd"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdDump() *cli.Command {
	return &cli.Command{
		Name:      "dump",
		Action:    dump,
		Category:  "ADMIN",
		Usage:     "Dump metadata into a file",
		ArgsUsage: "META-URL [FILE]",
		Description: `
Supports two formats: JSON format and binary format.
1. Dump metadata of the volume in JSON format so users are able to see its content in an easy way.
Output of this command can be loaded later into an empty database, serving as a method to backup
metadata or to change metadata engine.

Examples:
$ juicefs dump redis://localhost meta-dump.json
$ juicefs dump redis://localhost meta-dump.json.gz

# Dump only a subtree of the volume to STDOUT
$ juicefs dump redis://localhost --subdir /dir/in/jfs

2. Binary format is more compact, faster, and memory-efficient.

Examples:
$ juicefs dump redis://localhost meta-dump.bin --binary

Details: https://juicefs.com/docs/community/metadata_dump_load`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "subdir",
				Usage: "only dump a sub-directory",
			},
			&cli.BoolFlag{
				Name:  "keep-secret-key",
				Usage: "keep secret keys intact (WARNING: Be careful as they may be leaked)",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number of threads to dump metadata",
			},
			&cli.BoolFlag{
				Name:  "fast",
				Usage: "speedup dump by load all metadata into memory (only works with JSON format and DB/KV engine)",
			},
			&cli.BoolFlag{
				Name:  "skip-trash",
				Usage: "skip files in trash",
			},
			&cli.BoolFlag{
				Name:  "binary",
				Usage: "dump metadata into a binary file (different from original JSON format, subdir/fast/skip-trash will be ignored)",
			},
		},
	}
}

func dumpMeta(m meta.Meta, dst string, threads int, keepSecret, fast, skipTrash, isBinary bool) (err error) {
	var w io.WriteCloser
	if dst == "" {
		w = os.Stdout
	} else {
		tmp := dst + ".tmp"
		fp, e := os.Create(tmp)
		if e != nil {
			return e
		}
		defer func() {
			err = errors.Join(err, fp.Close())
			if err == nil {
				err = os.Rename(tmp, dst)
			} else {
				_ = os.Remove(tmp)
			}
		}()

		if strings.HasSuffix(dst, ".gz") {
			w, _ = gzip.NewWriterLevel(fp, gzip.BestSpeed)
			defer func() {
				err = errors.Join(err, w.Close())
			}()
		} else if strings.HasSuffix(dst, ".zstd") {
			w = zstd.NewWriterLevel(fp, zstd.BestSpeed)
			defer func() {
				err = errors.Join(err, w.Close())
			}()
		} else {
			w = fp
		}
	}
	if isBinary {
		progress := utils.NewProgress(false)
		defer progress.Done()

		bars := make(map[string]*utils.Bar)
		for _, name := range meta.SegType2Name {
			bars[name] = progress.AddCountSpinner(name)
		}

		return m.DumpMetaV2(meta.Background(), w, &meta.DumpOption{
			KeepSecret: keepSecret,
			Threads:    threads,
			Progress: func(name string, cnt int) {
				bars[name].IncrBy(cnt)
			},
		})
	}
	return m.DumpMeta(w, 1, threads, keepSecret, fast, skipTrash)
}

func dump(ctx *cli.Context) error {
	setup(ctx, 1)
	metaUri := ctx.Args().Get(0)
	var dst string
	if ctx.Args().Len() > 1 {
		dst = ctx.Args().Get(1)
	}
	removePassword(metaUri)

	metaConf := meta.DefaultConf()
	metaConf.Subdir = ctx.String("subdir")
	m := meta.NewClient(metaUri, metaConf)
	if _, err := m.Load(true); err != nil {
		return err
	}
	if st := m.Chroot(meta.Background(), metaConf.Subdir); st != 0 {
		return st
	}

	threads := ctx.Int("threads")
	if threads <= 0 {
		logger.Warnf("Invalid threads number %d, reset to 1", threads)
		threads = 1
	}

	err := dumpMeta(m, dst, threads, ctx.Bool("keep-secret-key"), ctx.Bool("fast"), ctx.Bool("skip-trash"), ctx.Bool("binary"))
	if err == nil {
		if dst == "" {
			dst = "STDOUT"
		}
		logger.Infof("Dump metadata into %s succeed", dst)
	}
	return err
}
