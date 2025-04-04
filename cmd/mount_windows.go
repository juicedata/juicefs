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
	"os"
	"path/filepath"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/juicedata/juicefs/pkg/winfsp"
	"github.com/urfave/cli/v2"
)

func mountFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "o",
			Usage: "other FUSE options",
		},
		&cli.StringFlag{
			Name:  "log",
			Value: filepath.Join(getDefaultLogDir(), "juicefs.log"),
			Usage: "path of log file when running in background",
		},
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "Access log file",
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
		&cli.BoolFlag{
			Name:    "d",
			Aliases: []string{"background"},
			Usage:   "run in background(Windows: as a system service. support ONLY 1 volume mounting at the same time)",
		},
		&cli.BoolFlag{
			Name:  "show-dot-files",
			Usage: "If set, dot files will not be treated as hidden files",
		},
	}
}

func makeDaemon(c *cli.Context, conf *vfs.Config) error {
	logPath := c.String("log")
	if logPath != "" {
		if !filepath.IsAbs(logPath) {
			return cli.Exit("log path must be an absolute path", 1)
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
			return cli.Exit(err, 1)
		}
	}

	return winfsp.RunAsSystemSerivce(conf.Format.Name, c.Args().Get(1), logPath)
}

func makeDaemonForSvc(c *cli.Context, m meta.Meta, metaUrl, listenAddr string) error {
	logger.Warnf("Cannot run in background in Windows.")
	return nil
}

func getDaemonStage() int {
	return 0
}

func mountMain(v *vfs.VFS, c *cli.Context) {
	v.Conf.AccessLog = c.String("access-log")
	winfsp.Serve(v, c.String("o"), c.Float64("file-cache-to"), c.Bool("as-root"), c.Int("delay-close"), c.Bool("show-dot-files"))
}

func checkMountpoint(name, mp, logPath string, background bool) {}

func prepareMp(mp string) {}

func setFuseOption(c *cli.Context, format *meta.Format, vfsConf *vfs.Config) {}

func launchMount(mp string, conf *vfs.Config) error { return nil }

func installHandler(m meta.Meta, mp string, v *vfs.VFS, blob object.ObjectStorage) {}
