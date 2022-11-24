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
	"io"
	"os"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func cmdDump() *cli.Command {
	return &cli.Command{
		Name:      "dump",
		Action:    dump,
		Category:  "ADMIN",
		Usage:     "Dump metadata into a JSON file",
		ArgsUsage: "META-URL [FILE]",
		Description: `
Dump metadata of the volume in JSON format so users are able to see its content in an easy way.
Output of this command can be loaded later into an empty database, serving as a method to backup
metadata or to change metadata engine.

Examples:
$ juicefs dump redis://localhost meta-dump.json
$ juicefs dump redis://localhost meta-dump.json.gz

# Dump only a subtree of the volume to STDOUT
$ juicefs dump redis://localhost --subdir /dir/in/jfs

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
		},
	}
}

func dump(ctx *cli.Context) (err error) {
	setup(ctx, 1)
	metaUri := ctx.Args().Get(0)
	dst := ctx.Args().Get(1)
	removePassword(metaUri)
	var w io.WriteCloser
	if ctx.Args().Len() == 1 {
		w = os.Stdout
		dst = "STDOUT"
	} else {
		fp, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		defer func() {
			e := fp.Close()
			if err == nil {
				err = e
			}
		}()
		if strings.HasSuffix(dst, ".gz") {
			zw := gzip.NewWriter(fp)
			defer func() {
				e := zw.Close()
				if err == nil {
					err = e
				}
			}()
			w = zw
		} else {
			w = fp
		}
	}
	metaConf := &meta.Config{Retries: 10, Strict: true, Subdir: ctx.String("subdir")}
	m := meta.NewClient(metaUri, metaConf)
	if _, err := m.Load(true); err != nil {
		return err
	}
	if st := m.Chroot(meta.Background, metaConf.Subdir); st != 0 {
		return st
	}
	if err := m.DumpMeta(w, 1, ctx.Bool("keep-secret-key")); err != nil {
		return err
	}
	logger.Infof("Dump metadata into %s succeed", dst)
	return nil
}
