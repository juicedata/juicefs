/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"fmt"
	"io/ioutil"
	"os"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func importMeta(m meta.Meta, fname string) error {
	buf, err := ioutil.ReadFile(fname)
	if err != nil {
		return err
	}
	return m.LoadMeta(buf)
}

func exportMeta(m meta.Meta, fname string) error {
	fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer fp.Close()
	return m.DumpMeta(fp)
}

func backup(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-ADDR is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})

	if fname := ctx.String("import"); fname != "" {
		return importMeta(m, fname)
	} else {
		return exportMeta(m, ctx.String("export"))
	}
}

func backupFlags() *cli.Command {
	return &cli.Command{
		Name:      "backup",
		Usage:     "back up JuiceFS metadata in a standalone file",
		ArgsUsage: "META-ADDR",
		Action:    backup,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "export",
				Aliases: []string{"o"},
				Value:   "metadata-backup",
				Usage:   "export metadata to a file",
			},
			&cli.StringFlag{
				Name:    "import",
				Aliases: []string{"i"},
				Usage:   "import metadata from a file",
			},
		},
	}
}
