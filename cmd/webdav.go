//go:build !nowebdav
// +build !nowebdav

/*
 *  * JuiceFS, Copyright 2022 Juicedata, Inc.
 *  *
 *  * Licensed under the Apache License, Version 2.0 (the "License");
 *  * you may not use this file except in compliance with the License.
 *  * You may obtain a copy of the License at
 *  *
 *  *     http://www.apache.org/licenses/LICENSE-2.0
 *  *
 *  * Unless required by applicable law or agreed to in writing, software
 *  * distributed under the License is distributed on an "AS IS" BASIS,
 *  * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  * See the License for the specific language governing permissions and
 *  * limitations under the License.
 *
 */

package main

import (
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/urfave/cli/v2"
)

func cmdWebDav() *cli.Command {
	selfFlags := []cli.Flag{
		&cli.BoolFlag{
			Name:  "gzip",
			Usage: "compress served files via gzip",
		},
		&cli.BoolFlag{
			Name:  "disallowList",
			Usage: "disallow list a directory",
		},
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "path for JuiceFS access log",
		},
	}
	compoundFlags := [][]cli.Flag{
		clientFlags(),
		selfFlags,
		cacheFlags(0),
		shareInfoFlags(),
	}

	return &cli.Command{
		Name:      "webdav",
		Category:  "SERVICE",
		Usage:     "start a webdav server",
		ArgsUsage: "META-URL ADDRESS",
		Flags:     expandFlags(compoundFlags),
		Action:    webdavSvc,
	}
}

func webdavSvc(c *cli.Context) error {
	setLoggerLevel(c)
	if c.Args().Len() < 1 {
		logger.Fatalf("meta url are required")
	}
	metaUrl := c.Args().Get(0)
	if c.Args().Len() < 2 {
		logger.Fatalf("listen address is required")
	}
	listenAddr := c.Args().Get(1)
	m, store, conf := initForSvc(c, "webdav", metaUrl)
	jfs, err := fs.NewFileSystem(conf, m, store)
	if err != nil {
		logger.Fatalf("initialize failed: %s", err)
	}
	fs.StartHTTPServer(jfs, listenAddr, c.Bool("gzip"), c.Bool("disallowList"))
	return m.CloseSession()
}
