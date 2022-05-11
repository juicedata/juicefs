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
