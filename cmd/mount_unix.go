//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"bytes"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/juicedata/godaemon"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func checkMountpoint(name, mp, logPath string, background bool) {
	for i := 0; i < 20; i++ {
		time.Sleep(time.Millisecond * 500)
		st, err := os.Stat(mp)
		if err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == uint64(meta.RootInode) {
				logger.Infof("\033[92mOK\033[0m, %s is ready at %s", name, mp)
				return
			}
		}
		_, _ = os.Stdout.WriteString(".")
		_ = os.Stdout.Sync()
	}
	_, _ = os.Stdout.WriteString("\n")
	if background {
		logger.Fatalf("The mount point is not ready in 10 seconds, please check the log (%s) or re-mount in foreground", logPath)
	} else {
		logger.Fatalf("The mount point is not ready in 10 seconds, exit it")
	}
}

func makeDaemon(c *cli.Context, name, mp string, m meta.Meta) error {
	var attrs godaemon.DaemonAttr
	logfile := c.String("log")
	attrs.OnExit = func(stage int) error {
		if stage != 0 {
			return nil
		}
		checkMountpoint(name, mp, logfile, true)
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
		attrs.Stdout, err = os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Errorf("open log file %s: %s", logfile, err)
		}
	}
	if godaemon.Stage() <= 1 {
		err := m.Shutdown()
		if err != nil {
			logger.Errorf("shutdown: %s", err)
		}
	}
	_, _, err := godaemon.MakeDaemon(&attrs)
	return err
}

func mount_flags() []cli.Flag {
	var defaultLogDir = "/var/log"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultLogDir = path.Join(homeDir, ".juicefs")
	}
	selfFlags := []cli.Flag{
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
		&cli.BoolFlag{
			Name:  "enable-xattr",
			Usage: "enable extended attributes (xattr)",
		},
		&cli.BoolFlag{
			Name:  "update-fstab",
			Usage: "add / update entry in /etc/fstab, will create a symlink at /sbin/mount.juicefs if not existing",
		},
	}
	return append(selfFlags, cacheFlags(1.0)...)
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

func mount_main(v *vfs.VFS, c *cli.Context) {
	if os.Getuid() == 0 && os.Getpid() != 1 {
		disableUpdatedb()
	}

	conf := v.Conf
	conf.AttrTimeout = time.Millisecond * time.Duration(c.Float64("attr-cache")*1000)
	conf.EntryTimeout = time.Millisecond * time.Duration(c.Float64("entry-cache")*1000)
	conf.DirEntryTimeout = time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000)
	logger.Infof("Mounting volume %s at %s ...", conf.Format.Name, conf.Meta.MountPoint)
	err := fuse.Serve(v, c.String("o"), c.Bool("enable-xattr"))
	if err != nil {
		logger.Fatalf("fuse: %s", err)
	}
}
