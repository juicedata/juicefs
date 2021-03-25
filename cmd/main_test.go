/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"reflect"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestArgsOrder(t *testing.T) {
	var app = &cli.App{
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
			},
			&cli.Int64Flag{
				Name:    "key",
				Aliases: []string{"k"},
			},
		},
		Commands: []*cli.Command{
			{
				Name: "cmd",
				Flags: []cli.Flag{
					&cli.Int64Flag{
						Name: "k2",
					},
				},
			},
		},
	}

	var cases = [][]string{
		{"test", "cmd", "a", "-k2", "v2", "b", "--v"},
		{"test", "--v", "cmd", "-k2", "v2", "a", "b"},
		{"test", "cmd", "a", "-k2=v", "--h"},
		{"test", "cmd", "-k2=v", "--h", "a"},
	}
	for i := 0; i < len(cases); i += 2 {
		oreded := reorderOptions(app, cases[i])
		if !reflect.DeepEqual(cases[i+1], oreded) {
			t.Fatalf("expecte %v, but got %v", cases[i+1], oreded)
		}
	}
}
