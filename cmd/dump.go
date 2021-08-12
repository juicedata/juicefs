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
	"io"
	"os"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func dump(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	var fp io.WriteCloser
	if ctx.Args().Len() == 1 {
		fp = os.Stdout
	} else {
		var err error
		fp, err = os.OpenFile(ctx.Args().Get(1), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		defer fp.Close()
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true, Subdir: ctx.String("subdir")})
	if err := m.DumpMeta(fp); err != nil {
		return err
	}
	logger.Infof("Dump metadata into %s succeed", ctx.Args().Get(1))
	return nil
}

func dumpFlags() *cli.Command {
	return &cli.Command{
		Name:      "dump",
		Usage:     "dump metadata into a JSON file",
		ArgsUsage: "META-URL [FILE]",
		Action:    dump,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "subdir",
				Usage: "only dump a sub-directory.",
			},
		},
	}
}
