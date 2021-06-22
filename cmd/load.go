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

func load(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-ADDR is needed")
	}
	var buf []byte
	var err error
	if ctx.Args().Len() == 1 || ctx.Args().Get(1) == "--" {
		buf, err = ioutil.ReadAll(os.Stdin)
	} else {
		buf, err = ioutil.ReadFile(ctx.Args().Get(1))
	}
	if err != nil {
		return err
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	return m.LoadMeta(buf)
}

func loadFlags() *cli.Command {
	return &cli.Command{
		Name:      "load",
		Usage:     "load metadata from a previously dumped JSON file",
		ArgsUsage: "META-ADDR [FILE]",
		Action:    load,
	}
}
