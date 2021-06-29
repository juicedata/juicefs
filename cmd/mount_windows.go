/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"strings"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/juicedata/juicefs/pkg/winfsp"
	"github.com/urfave/cli/v2"
)

func mount_flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "o",
			Usage: "other FUSE options",
		},
		&cli.BoolFlag{
			Name:  "as-root",
			Usage: "Access files as administrator",
		},
		&cli.Float64Flag{
			Name:  "file-cache-to",
			Value: 0.1,
			Usage: "Cache file attributes in seconds",
		},
		&cli.Float64Flag{
			Name:  "delay-close",
			Usage: "delay file closing in seconds.",
		},
	}
}

func makeDaemon(c *cli.Context, name, mp string) error {
	logger.Warnf("Cannot run in background in Windows.")
	return nil
}

func mount_main(conf *vfs.Config, m meta.Meta, store chunk.ChunkStore, c *cli.Context) {
	jfs, err := fs.NewFileSystem(conf, m, store)
	if err != nil {
		logger.Fatalf("Initialize failed: %s", err)
	}
	winfsp.Serve(conf, jfs, c.String("o"), c.Float64("file-cache-to"), c.Bool("as-root"), c.Int("delay-close"),
		strings.HasSuffix(conf.Mountpoint, ":"))
}

func checkMountpoint(name, mp string) {
}
