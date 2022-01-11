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

package main

import (
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

func mount_main(v *vfs.VFS, c *cli.Context) {
	winfsp.Serve(v, c.String("o"), c.Float64("file-cache-to"), c.Bool("as-root"), c.Int("delay-close"))
}

func checkMountpoint(name, mp string) {
}
