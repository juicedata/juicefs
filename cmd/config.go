/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func warn(format string, a ...interface{}) {
	fmt.Printf("\033[1;33mWARNING\033[0m: "+format+"\n", a...)
}

func userConfirmed() bool {
	fmt.Print("Proceed anyway? [y/N]: ")
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
				warn("New quota is too small (used / quota): %d / %d bytes, %d / %d inodes.",
					usedSpace, format.Capacity, iused, format.Inodes)
				if !userConfirmed() {
					return fmt.Errorf("Aborted.")
				}
			}
		}
		if trash && format.TrashDays == 0 {
			warn("The current trash will be emptied and future removed files will purged immediately.")
			if !userConfirmed() {
				return fmt.Errorf("Aborted.")
			}
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
