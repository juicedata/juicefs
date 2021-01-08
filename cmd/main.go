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
	"log"
	_ "net/http/pprof"
	"os"

	_ "github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

var logger = utils.GetLogger("juicefs")

func main() {
	cli.VersionFlag = &cli.BoolFlag{
		Name: "version", Aliases: []string{"V"},
		Usage: "print only the version",
	}
	app := &cli.App{
		Name:      "juicefs",
		Usage:     "A POSIX filesystem built on redis and object storage.",
		Version:   Version(),
		Copyright: "AGPLv3",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"v"},
				Usage:   "enable debug log",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "only warning and errors",
			},
			&cli.BoolFlag{
				Name:  "trace",
				Usage: "enable trace log",
			},
			&cli.BoolFlag{
				Name:  "nosyslog",
				Usage: "disable syslog",
			},
		},
		Commands: []*cli.Command{
			formatFlags(),
			mountFlags(),
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
