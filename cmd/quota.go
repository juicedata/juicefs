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
		Usage:           "manage the quota of a dir",
		ArgsUsage:       "META-URL PATH",
		HideHelpCommand: true,
		Description: `
Examples:
$ juicefs quota set redis://localhost /dir1 --inodes 100
$ juicefs quota get redis://localhost /dir1`,
		Subcommands: []*cli.Command{
			{
				Name:      "set",
				Usage:     "Set quota for dir",
				ArgsUsage: "META-URL PATH",
				Action:    quota,
			},
			{
				Name:      "get",
				Usage:     "Get quota for dir",
				ArgsUsage: "META-URL PATH",
				Action:    quota,
			},
			{
				Name:      "del",
				Usage:     "Del quota for dir",
				ArgsUsage: "META-URL PATH",
				Action:    quota,
			},
			{
				Name:      "ls",
				Usage:     "List quotas",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
			{
				Name:      "check",
				Usage:     "Check consistency of directory quota",
				ArgsUsage: "META-URL PATH",
				Action:    quota,
			},
		},
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:  "capacity",
				Usage: "hard quota of the volume limiting its usage of space in GiB",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Usage: "hard quota of the volume limiting its number of inodes",
			},
		},
	}
}

func quota(c *cli.Context) error {
	setup(c, 2)
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

	removePassword(c.Args().Get(0))
	m := meta.NewClient(c.Args().Get(0), nil)
	p := c.Args().Get(1)
	var q meta.Quota
	if cmd == meta.QuotaSet {
		q.MaxSpace, q.MaxInodes = -1, -1 // negative means no change
		if c.IsSet("capacity") {
			q.MaxSpace = int64(c.Uint64("capacity")) << 30
		}
		if c.IsSet("inodes") {
			q.MaxInodes = int64(c.Uint64("inodes"))
		}
	}
	if st := m.HandleQuota(meta.Background, cmd, p, &q); st != 0 {
		return st
	}
	// FIXME: need a better way to do print
	if q.MaxSpace != 0 || q.MaxInodes != 0 {
		for i := &q; i != nil; i = i.Parent {
			fmt.Printf("Quota: %+v\n", *i)
		}
	}
	return nil
}
