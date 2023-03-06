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
	"github.com/urfave/cli/v2"
)

func cmdCloneFunc() *cli.Command {
	return &cli.Command{
		Name:   "clone",
		Action: clone,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "cp",
				Usage: "create files with current uid,gid,umask (like 'cp')"},
		},
		Category:    "TOOL",
		Description: `This command can clone a file or directory without copying the underlying data.`,
	}
}
