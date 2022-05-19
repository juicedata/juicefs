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
	"encoding/json"
	"fmt"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func cmdStatus() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Action:    status,
		Category:  "INSPECTOR",
		Usage:     "Show status of a volume",
		ArgsUsage: "META-URL",
		Description: `
It shows basic setting of the target volume, and a list of active sessions (including mount, SDK,
S3-gateway and WebDAV) that are connected with the metadata engine.

NOTE: Read-only session is not listed since it cannot register itself in the metadata.

Examples:
$ juicefs status redis://localhost`,
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:    "session",
				Aliases: []string{"s"},
				Usage:   "show detailed information (sustained inodes, locks) of the specified session (sid)",
			},
		},
	}
}

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
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	format.RemoveSecret()

	if sid := ctx.Uint64("session"); sid != 0 {
		s, err := m.GetSession(sid, true)
		if err != nil {
			logger.Fatalf("get session: %s", err)
		}
		printJson(s)
		return nil
	}

	sessions, err := m.ListSessions()
	if err != nil {
		logger.Fatalf("list sessions: %s", err)
	}

	printJson(&sections{format, sessions})
	return nil
}
