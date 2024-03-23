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
	"sort"

	"github.com/dustin/go-humanize"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"

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
$ juicefs quota list redis://localhost
$ juicefs quota delete redis://localhost --path /dir1`,
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
				Name:      "delete",
				Aliases:   []string{"del"},
				Usage:     "Delete quota of a directory",
				ArgsUsage: "META-URL",
				Action:    quota,
			},
			{
				Name:      "list",
				Aliases:   []string{"ls"},
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
			&cli.StringFlag{
				Name:  "capacity",
				Usage: "hard quota of the directory limiting its usage of space in GiB",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Usage: "hard quota of the directory limiting its number of inodes",
			},
			&cli.BoolFlag{
				Name:  "repair",
				Usage: "repair inconsistent quota",
			},
			&cli.BoolFlag{
				Name:  "strict",
				Usage: "calculate total usage of directory in strict mode (NOTE: may be slow for huge directory)",
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
	case "delete":
		cmd = meta.QuotaDel
	case "list":
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
	_, err := m.Load(true)
	if err != nil {
		logger.Fatalf("Load setting: %s", err)
	}
	qs := make(map[string]*meta.Quota)
	var strict, repair bool
	if cmd == meta.QuotaSet {
		strict = c.Bool("strict")
		q := &meta.Quota{MaxSpace: -1, MaxInodes: -1} // negative means no change
		if c.IsSet("capacity") {
			q.MaxSpace = int64(utils.ParseBytes(c, "capacity", 'G'))
		}
		if c.IsSet("inodes") {
			q.MaxInodes = int64(c.Uint64("inodes"))
		}
		qs[dpath] = q
	}
	if cmd == meta.QuotaCheck {
		strict = c.Bool("strict")
		repair = c.Bool("repair")
	}
	if err := m.HandleQuota(meta.Background, cmd, dpath, qs, strict, repair); err != nil {
		return err
	} else if len(qs) == 0 {
		return nil
	}

	result := make([][]string, 1, len(qs)+1)
	result[0] = []string{"Path", "Size", "Used", "Use%", "Inodes", "IUsed", "IUse%"}
	paths := make([]string, 0, len(qs))
	for p := range qs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		q := qs[p]
		if q.UsedSpace < 0 {
			logger.Warnf("Used space of %s is negative (%d), please run `juicefs quota check` to fix it", p, q.UsedSpace)
			q.UsedSpace = 0
		}
		if q.UsedInodes < 0 {
			logger.Warnf("Used inodes of %s is negative (%d), please run `juicefs quota check` to fix it", p, q.UsedInodes)
			q.UsedInodes = 0
		}
		used := humanize.IBytes(uint64(q.UsedSpace))
		var size, usedR string
		if q.MaxSpace > 0 {
			size = humanize.IBytes(uint64(q.MaxSpace))
			usedR = fmt.Sprintf("%d%%", q.UsedSpace*100/q.MaxSpace)
		} else {
			size = "unlimited"
		}
		iused := humanize.Comma(q.UsedInodes)
		var itotal, iusedR string
		if q.MaxInodes > 0 {
			itotal = humanize.Comma(q.MaxInodes)
			iusedR = fmt.Sprintf("%d%%", q.UsedInodes*100/q.MaxInodes)
		} else {
			itotal = "unlimited"
		}
		result = append(result, []string{p, size, used, usedR, itotal, iused, iusedR})
	}
	printResult(result, 0, false)
	return nil
}
