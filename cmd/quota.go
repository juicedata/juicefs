/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"fmt"

	"github.com/juicedata/juicefs/pkg/meta"

	"github.com/urfave/cli/v2"
)

func cmdQuota() *cli.Command {
	return &cli.Command{
		Name:            "quota",
		Category:        "ADMIN",
		Usage:           "Manage directory quotas",
		ArgsUsage:       "META-URL",
		HideHelpCommand: true,
		Description: `
Examples:
$ juicefs quota set redis://localhost --path /dir1 --capacity 1 --inodes 100
$ juicefs quota get redis://localhost --path /dir1
$ juicefs quota del redis://localhost --path /dir1
$ juicefs quota ls redis://localhost`,
		Subcommands: []*cli.Command{
			{
				Name:      "set",
				Usage:     "Set quota to a directory",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
			{
				Name:      "get",
				Usage:     "Get quota of a directory",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
			{
				Name:      "del",
				Usage:     "Delete quota of a directory",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
			{
				Name:      "ls",
				Usage:     "List all directory quotas",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
			{
				Name:      "check",
				Usage:     "Check quota consistency of a directory",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "path",
				Usage: "full path of the directory within the volume",
			},
			&cli.Uint64Flag{
				Name:  "capacity",
				Usage: "hard quota of the directory limiting its usage of space in GiB",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Usage: "hard quota of the directory limiting its number of inodes",
			},
		},
	}
}

func quota(c *cli.Context) error {
	setup(c, 1)
	var cmd uint8
	switch c.Command.Name {
	case "set":
		cmd = meta.QuotaSet
	case "get":
		cmd = meta.QuotaGet
	case "del":
		cmd = meta.QuotaDel
	case "ls":
		cmd = meta.QuotaList
	case "check":
		cmd = meta.QuotaCheck
	default:
		logger.Fatalf("Invalid quota command: %s", c.Command.Name)
	}
	dpath := c.String("path")
	if dpath == "" && cmd != meta.QuotaList {
		logger.Fatalf("Please specify the directory with `--path <dir>` option")
	}
	removePassword(c.Args().Get(0))

	m := meta.NewClient(c.Args().Get(0), nil)
	qs := make(map[string]*meta.Quota)
	if cmd == meta.QuotaSet {
		q := &meta.Quota{MaxSpace: -1, MaxInodes: -1} // negative means no change
		if c.IsSet("capacity") {
			q.MaxSpace = int64(c.Uint64("capacity")) << 30
		}
		if c.IsSet("inodes") {
			q.MaxInodes = int64(c.Uint64("inodes"))
		}
		qs[dpath] = q
	}
	if err := m.HandleQuota(meta.Background, cmd, dpath, qs); err != nil {
		return err
	}

	for p, q := range qs {
		// FIXME: need a better way to do print
		fmt.Printf("%s: %+v\n", p, *q)
	}
	return nil
}
