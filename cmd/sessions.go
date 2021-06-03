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
	"encoding/json"
	"fmt"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func sessions(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-ADDR is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	ss, err := m.ListSessions()
	if err != nil {
		logger.Fatal("list sessions: %s", err)
	}
	for _, s := range ss {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			logger.Fatalf("json: %s", err)
		}
		fmt.Println(string(data))
	}
	return nil
}

func sessionsFlags() *cli.Command {
	return &cli.Command{
		Name:      "sessions",
		Usage:     "list sessions of JuiceFS",
		ArgsUsage: "META-ADDR",
		Action:    sessions,
		/*
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:    "session",
					Aliases: []string{"s"},
					Usage:   "list client sessions",
				},
			},
		*/
	}
}
