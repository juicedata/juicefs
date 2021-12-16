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
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func config(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})

	format, err := m.Load()
	if err != nil {
		return err
	}
	var msg strings.Builder
	for _, flag := range ctx.LocalFlagNames() {
		switch flag {
		case "capacity":
			msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.Capacity, ctx.Uint64(flag)))
			format.Capacity = ctx.Uint64(flag)
		case "inodes":
			msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.Inodes, ctx.Uint64(flag)))
			format.Inodes = ctx.Uint64(flag)
		case "bucket":
			msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.Bucket, ctx.String(flag)))
			format.Bucket = ctx.String(flag)
		case "access-key":
			msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.AccessKey, ctx.String(flag)))
			format.AccessKey = ctx.String(flag)
		case "secret-key":
			msg.WriteString(fmt.Sprintf("%10s: updated\n", flag))
			format.SecretKey = ctx.String(flag)
		case "trash-days":
			msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.TrashDays, ctx.Uint64(flag)))
			format.TrashDays = ctx.Int(flag)
		}
	}
	if msg.Len() == 0 {
		format.RemoveSecret()
		printJson(format)
		return nil
	}

	if err = m.Init(*format, false); err == nil {
		fmt.Println(msg.String()[:msg.Len()-1])
	}
	return err
}

func configFlags() *cli.Command {
	return &cli.Command{
		Name:      "config",
		Usage:     "change config of a volume",
		ArgsUsage: "META-URL",
		Action:    config,
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:  "capacity",
				Usage: "the limit for space in GiB",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Usage: "the limit for number of inodes",
			},
			&cli.StringFlag{
				Name:  "bucket",
				Usage: "A bucket URL to store data",
			},
			&cli.StringFlag{
				Name:  "access-key",
				Usage: "Access key for object storage",
			},
			&cli.StringFlag{
				Name:  "secret-key",
				Usage: "Secret key for object storage",
			},
			&cli.IntFlag{
				Name:  "trash-days",
				Usage: "number of days after which removed files will be permanently deleted",
			},
		},
	}
}
