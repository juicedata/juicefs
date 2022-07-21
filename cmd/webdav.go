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

package cmd

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
		Action:    webdav,
		Category:  "SERVICE",
		Usage:     "Start a WebDAV server",
		ArgsUsage: "META-URL ADDRESS",
		Description: `
Examples:
$ juicefs webdav redis://localhost localhost:9007`,
		Flags: expandFlags(compoundFlags),
	}
}

func webdav(c *cli.Context) error {
	setup(c, 2)
	metaUrl := c.Args().Get(0)
	listenAddr := c.Args().Get(1)
	_, jfs := initForSvc(c, "webdav", metaUrl)
	fs.StartHTTPServer(jfs, listenAddr, c.Bool("gzip"), c.Bool("disallowList"))
	return jfs.Meta().CloseSession()
}
