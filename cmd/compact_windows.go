//go:build windows
// +build windows

/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
		Name:     "compact",
		Action:   compact,
		Category: "TOOL",
		Usage:    "Trigger compaction of chunks, not supported for Windows",
	}
}

func compact(ctx *cli.Context) error {
	logger.Warnf("not supported for Windows.")
	return nil
}
