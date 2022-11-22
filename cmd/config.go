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

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/urfave/cli/v2"
)

func cmdConfig() *cli.Command {
	return &cli.Command{
		Name:      "config",
		Action:    config,
		Category:  "ADMIN",
		Usage:     "Change configuration of a volume",
		ArgsUsage: "META-URL",
		Description: `
Only flags explicitly specified are changed.

Examples:
# Show the current configurations
$ juicefs config redis://localhost

# Change volume "quota"
$ juicefs config redis://localhost --inode 10000000 --capacity 1048576

# Change maximum days before files in trash are deleted
$ juicefs config redis://localhost --trash-days 7

# Limit client version that is allowed to connect
$ juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0`,
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:  "capacity",
				Usage: "hard quota of the volume limiting its usage of space in GiB",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Usage: "hard quota of the volume limiting its number of inodes",
			},
			&cli.StringFlag{
				Name:  "bucket",
				Usage: "the bucket URL of object storage to store data",
			},
			&cli.StringFlag{
				Name:  "access-key",
				Usage: "access key for object storage",
			},
			&cli.StringFlag{
				Name:  "secret-key",
				Usage: "secret key for object storage",
			},
			&cli.StringFlag{
				Name:  "session-token",
				Usage: "session token for object storage",
			},
			&cli.BoolFlag{
				Name:  "encrypt-secret",
				Usage: "encrypt the secret key if it was previously stored in plain format",
			},
			&cli.IntFlag{
				Name:  "trash-days",
				Usage: "number of days after which removed files will be permanently deleted",
			},
			&cli.StringFlag{
				Name:  "min-client-version",
				Usage: "minimum client version allowed to connect",
			},
			&cli.StringFlag{
				Name:  "max-client-version",
				Usage: "maximum client version allowed to connect",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "skip sanity check and force update the configurations",
			},
		},
	}
}

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
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})

	format, err := m.Load(false)
	if err != nil {
		return err
	}
	if len(ctx.LocalFlagNames()) == 0 {
		fmt.Println(format)
		return nil
	}

	var quota, storage, trash, clientVer bool
	var msg strings.Builder
	encrypted := format.KeyEncrypted
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
				if format.Storage == "file" {
					if p, err := filepath.Abs(new); err == nil {
						new = p + "/"
					} else {
						logger.Fatalf("Failed to get absolute path of %s: %s", new, err)
					}
				}
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
		case "secret-key": // always update
			msg.WriteString(fmt.Sprintf("%10s: updated\n", flag))
			if err := format.Decrypt(); err != nil && strings.Contains(err.Error(), "secret was removed") {
				logger.Warnf("decrypt secrets: %s", err)
			}
			format.SecretKey = ctx.String(flag)
			storage = true
		case "session-token": // always update
			msg.WriteString(fmt.Sprintf("%10s: updated\n", flag))
			if err := format.Decrypt(); err != nil && strings.Contains(err.Error(), "secret was removed") {
				logger.Warnf("decrypt secrets: %s", err)
			}
			format.SessionToken = ctx.String(flag)
			storage = true
		case "trash-days":
			if new := ctx.Int(flag); new != format.TrashDays {
				if new < 0 {
					return fmt.Errorf("Invalid trash days: %d", new)
				}
				msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.TrashDays, new))
				format.TrashDays = new
				trash = true
			}
		case "min-client-version":
			if new := ctx.String(flag); new != format.MinClientVersion {
				if version.Parse(new) == nil {
					return fmt.Errorf("Invalid version string: %s", new)
				}
				msg.WriteString(fmt.Sprintf("%s: %s -> %s\n", flag, format.MinClientVersion, new))
				format.MinClientVersion = new
				clientVer = true
			}
		case "max-client-version":
			if new := ctx.String(flag); new != format.MaxClientVersion {
				if version.Parse(new) == nil {
					return fmt.Errorf("Invalid version string: %s", new)
				}
				msg.WriteString(fmt.Sprintf("%s: %s -> %s\n", flag, format.MaxClientVersion, new))
				format.MaxClientVersion = new
				clientVer = true
			}
		}
	}
	if msg.Len() == 0 {
		fmt.Println("Nothing changed.")
		return nil
	}

	if !ctx.Bool("force") {
		if storage {
			blob, err := createStorage(*format)
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
		if clientVer && format.CheckVersion() != nil {
			warn("Clients with the same version of this will be rejected after modification.")
			if !userConfirmed() {
				return fmt.Errorf("Aborted.")
			}
		}
	}

	if encrypted || ctx.Bool("encrypt-secret") {
		if err = format.Encrypt(); err != nil {
			logger.Fatalf("Format encrypt: %s", err)
		}
	}
	if err = m.Init(format, false); err == nil {
		fmt.Println(msg.String()[:msg.Len()-1])
	}
	return err
}
