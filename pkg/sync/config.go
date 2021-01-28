/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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

package sync

import (
	"github.com/urfave/cli/v2"
)

type Config struct {
	Start       string
	End         string
	Threads     int
	HTTPPort    int
	Update      bool
	ForceUpdate bool
	Perms       bool
	Dry         bool
	DeleteSrc   bool
	DeleteDst   bool
	Dirs        bool
	Exclude     []string
	Include     []string
	Manager     string
	Workers     []string
	BWLimit     int
	NoHTTPS     bool
	Verbose     bool
	Quiet       bool
}

func NewConfigFromCli(c *cli.Context) *Config {
	return &Config{
		Start:       c.String("start"),
		End:         c.String("end"),
		Threads:     c.Int("threads"),
		Update:      c.Bool("update"),
		ForceUpdate: c.Bool("force-update"),
		Perms:       c.Bool("perms"),
		Dirs:        c.Bool("dirs"),
		Dry:         c.Bool("dry"),
		DeleteSrc:   c.Bool("delete-src"),
		DeleteDst:   c.Bool("delete-dst"),
		Exclude:     c.StringSlice("exclude"),
		Include:     c.StringSlice("include"),
		Workers:     c.StringSlice("worker"),
		Manager:     c.String("manager"),
		BWLimit:     c.Int("bwlimit"),
		NoHTTPS:     c.Bool("no-https"),
		Verbose:     c.Bool("verbose"),
		Quiet:       c.Bool("quiet"),
	}
}
