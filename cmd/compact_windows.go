//go:build windows
// +build windows

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"github.com/urfave/cli/v2"
)

func cmdCompact() *cli.Command {
	return &cli.Command{
		Name:      "compact",
		Action:    compact,
		Category:  "ADMIN",
		Usage:     "Trigger compaction of slices, not supported for Windows",
		ArgsUsage: "META-URL",
		Description: `
 Examples:
 # compact with path
 $ juicefs compact --path /mnt/jfs/foo redis://localhost

 # max depth of 5
 $ juicefs summary --path /mnt/jfs/foo --depth 5 /mnt/jfs/foo redis://localhost
 `,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "path",
				Required: true,
				Usage:    "path to be compact",
			},
			&cli.UintFlag{
				Name:    "depth",
				Aliases: []string{"d"},
				Value:   2,
				Usage:   "dir depth to be scan",
			},
			&cli.IntFlag{
				Name:    "compact-concurrency",
				Aliases: []string{"cc"},
				Value:   10,
				Usage:   "compact concurrency",
			},
			&cli.IntFlag{
				Name:    "delete-concurrency",
				Aliases: []string{"dc"},
				Value:   10,
				Usage:   "delete concurrency",
			},
		},
	}
}

func compact(ctx *cli.Context) error {
	logger.Warnf("not supported for Windows.")
	return nil
}
