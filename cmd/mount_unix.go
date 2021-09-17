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
	"bytes"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/juicedata/godaemon"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func checkMountpoint(name, mp string) {
	for i := 0; i < 20; i++ {
		time.Sleep(time.Millisecond * 500)
		st, err := os.Stat(mp)
		if err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == 1 {
				logger.Infof("\033[92mOK\033[0m, %s is ready at %s", name, mp)
				return
			}
		}
		os.Stdout.WriteString(".")
		os.Stdout.Sync()
	}
	os.Stdout.WriteString("\n")
	logger.Fatalf("fail to mount after 10 seconds, please mount in foreground")
}

func makeDaemon(c *cli.Context, name, mp string) error {
	var attrs godaemon.DaemonAttr
	attrs.OnExit = func(stage int) error {
		if stage != 0 {
			return nil
		}
		checkMountpoint(name, mp)
		return nil
	}

	// the current dir will be changed to root in daemon,
	// so the mount point has to be an absolute path.
	if godaemon.Stage() == 0 {
		for i, a := range os.Args {
			if a == mp {
				amp, err := filepath.Abs(mp)
				if err == nil {
					os.Args[i] = amp
				} else {
					logger.Warnf("abs of %s: %s", mp, err)
				}
			}
		}
		var err error
		logfile := c.String("log")
		attrs.Stdout, err = os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Errorf("open log file %s: %s", logfile, err)
		}
	}
	_, _, err := godaemon.MakeDaemon(&attrs)
	return err
}

func mount_flags() []cli.Flag {
	var defaultLogDir = "/var/log"
	switch runtime.GOOS {
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultLogDir = path.Join(homeDir, ".juicefs")
	}
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
			Name:  "log",
			Value: path.Join(defaultLogDir, "juicefs.log"),
			Usage: "path of log file when running in background",
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

func disableUpdatedb() {
	path := "/etc/updatedb.conf"
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return
	}
	fstype := "fuse.juicefs"
	if bytes.Contains(data, []byte(fstype)) {
		return
	}
	// assume that fuse.sshfs is already in PRUNEFS
	knownFS := "fuse.sshfs"
	p1 := bytes.Index(data, []byte("PRUNEFS"))
	p2 := bytes.Index(data, []byte(knownFS))
	if p1 > 0 && p2 > p1 {
		var nd []byte
		nd = append(nd, data[:p2]...)
		nd = append(nd, fstype...)
		nd = append(nd, ' ')
		nd = append(nd, data[p2:]...)
		err = ioutil.WriteFile(path, nd, 0644)
		if err != nil {
			logger.Warnf("update %s: %s", path, err)
		} else {
			logger.Infof("Add %s into PRUNEFS of %s", fstype, path)
		}
	}
}

func mount_main(conf *vfs.Config, m meta.Meta, store chunk.ChunkStore, c *cli.Context) {
	if os.Getuid() == 0 && os.Getpid() != 1 {
		disableUpdatedb()
	}

	conf.AttrTimeout = time.Millisecond * time.Duration(c.Float64("attr-cache")*1000)
	conf.EntryTimeout = time.Millisecond * time.Duration(c.Float64("entry-cache")*1000)
	conf.DirEntryTimeout = time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000)
	logger.Infof("Mounting volume %s at %s ...", conf.Format.Name, conf.Mountpoint)
	err := fuse.Serve(conf, c.String("o"), c.Bool("enable-xattr"))
	if err != nil {
		logger.Fatalf("fuse: %s", err)
	}
}
