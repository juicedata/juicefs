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
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func userConfirmed(prompt string) bool {
	fmt.Println(prompt)
	fmt.Print("Still proceed? [y/N]: ")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if text := strings.ToLower(scanner.Text()); text == "y" || text == "yes" {
			return true
		} else if text == "" || text == "n" || text == "no" {
			return false
		} else {
			fmt.Print("Please input y(yes) or n(no): ")
		}
	}
	return false
}

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
	if len(ctx.LocalFlagNames()) == 0 {
		format.RemoveSecret()
		printJson(format)
		return nil
	}

	var quota, storage, trash bool
	var msg strings.Builder
	for _, flag := range ctx.LocalFlagNames() {
		switch flag {
		case "capacity":
			if new := ctx.Uint64(flag); new != format.Capacity>>30 {
				msg.WriteString(fmt.Sprintf("%10s: %d GiB -> %d GiB\n", flag, format.Capacity>>30, new))
				format.Capacity = new << 30
				quota = true
			}
		case "inodes":
			if new := ctx.Uint64(flag); new != format.Inodes {
				msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.Inodes, new))
				format.Inodes = new
				quota = true
			}
		case "bucket":
			if new := ctx.String(flag); new != format.Bucket {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.Bucket, new))
				format.Bucket = new
				storage = true
			}
		case "access-key":
			if new := ctx.String(flag); new != format.AccessKey {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.AccessKey, new))
				format.AccessKey = new
				storage = true
			}
		case "secret-key":
			if new := ctx.String(flag); new != format.SecretKey {
				msg.WriteString(fmt.Sprintf("%10s: updated\n", flag))
				format.SecretKey = new
				storage = true
			}
		case "trash-days":
			if new := ctx.Int(flag); new != format.TrashDays {
				msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.TrashDays, new))
				format.TrashDays = new
				trash = true
			}
		}
	}
	if msg.Len() == 0 {
		fmt.Println("Nothing changed.")
		return nil
	}

	if !ctx.Bool("force") {
		if storage {
			blob, err := createStorage(format)
			if err != nil {
				return err
			}
			if err = test(blob); err != nil {
				return err
			}
		}
		if quota {
			var totalSpace, availSpace, iused, iavail uint64
			_ = m.StatFS(meta.Background, &totalSpace, &availSpace, &iused, &iavail)
			usedSpace := totalSpace - availSpace
			if format.Capacity > 0 && usedSpace >= format.Capacity ||
				format.Inodes > 0 && iused >= format.Inodes {
				if !userConfirmed(fmt.Sprintf("New quota is too small (used / quota): %d / %d bytes, %d / %d inodes.",
					usedSpace, format.Capacity, iused, format.Inodes)) {
					return fmt.Errorf("Aborted.")
				}
			}
		}
		if trash && format.TrashDays == 0 &&
			!userConfirmed("The current trash will be emptied and future removed files will purged immediately.") {
			return fmt.Errorf("Aborted.")
		}
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
			&cli.BoolFlag{
				Name:  "force",
				Usage: "skip sanity check and force update the configurations",
			},
		},
	}
}
