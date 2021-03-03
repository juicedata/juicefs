// +build !windows

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
	"os"
	"syscall"
	"time"

	"github.com/juicedata/godaemon"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func makeDaemon(name, mp string) error {
	onExit := func(stage int) error {
		if stage != 0 {
			return nil
		}
		for {
			time.Sleep(time.Millisecond * 50)
			st, err := os.Stat(mp)
			if err == nil {
				if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == 1 {
					logger.Infof("\033[92mOK\033[0m, %s is ready at %s", name, mp)
					break
				}
			}
		}
		return nil
	}
	_, _, err := godaemon.MakeDaemon(&godaemon.DaemonAttr{OnExit: onExit})
	return err
}

func mount_flags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    "d",
			Aliases: []string{"background"},
			Usage:   "run in background",
		},
		&cli.BoolFlag{
			Name:  "no-syslog",
			Usage: "disable syslog",
		},
		&cli.StringFlag{
			Name:  "o",
			Usage: "other FUSE options",
		},
		&cli.Float64Flag{
			Name:  "attr-cache",
			Value: 1.0,
			Usage: "attributes cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "entry-cache",
			Value: 1.0,
			Usage: "file entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "dir-entry-cache",
			Value: 1.0,
			Usage: "dir entry cache timeout in seconds",
		},
		&cli.BoolFlag{
			Name:  "enable-xattr",
			Usage: "enable extended attributes (xattr)",
		},
	}
}

func mount_main(conf *vfs.Config, m meta.Meta, store chunk.ChunkStore, c *cli.Context) {
	logger.Infof("Mounting volume %s at %s ...", conf.Format.Name, conf.Mountpoint)
	if c.Bool("background") && os.Getenv("JFS_FOREGROUND") == "" {
		// The default log to syslog is only in daemon mode.
		utils.InitLoggers(!c.Bool("no-syslog"))
		err := makeDaemon(conf.Format.Name, conf.Mountpoint)
		if err != nil {
			logger.Fatalf("Failed to make daemon: %s", err)
		}
	}
	err := fuse.Serve(conf, c.String("o"), c.Float64("attr-cache"), c.Float64("entry-cache"), c.Float64("dir-entry-cache"), c.Bool("enable-xattr"))
	if err != nil {
		logger.Fatalf("fuse: %s", err)
	}
}
