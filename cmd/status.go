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

type sections struct {
	Setting  *meta.Format
	Sessions []*meta.Session
}

func printJson(v interface{}) {
	output, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}
	fmt.Println(string(output))
}

func status(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})

	if sid := ctx.Uint64("session"); sid != 0 {
		s, err := m.GetSession(sid)
		if err != nil {
			logger.Fatalf("get session: %s", err)
		}
		printJson(s)
		return nil
	}

	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	format.RemoveSecret()

	sessions, err := m.ListSessions()
	if err != nil {
		logger.Fatalf("list sessions: %s", err)
	}

	printJson(&sections{format, sessions})
	return nil
}

func statusFlags() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "show status of JuiceFS",
		ArgsUsage: "META-URL",
		Action:    status,
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:    "session",
				Aliases: []string{"s"},
				Usage:   "show detailed information (sustained inodes, locks) of the specified session (sid)",
			},
		},
	}
}
