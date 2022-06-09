/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	Links       bool
	Limit       int64
	Manager     string
	Workers     []string
	BWLimit     int
	NoHTTPS     bool
	Verbose     bool
	Quiet       bool
	CheckAll    bool
	CheckNew    bool
}

func NewConfigFromCli(c *cli.Context) *Config {
	if c.IsSet("limit") && c.Int64("limit") < 0 {
		logger.Fatal("limit should not be less than 0")
	}

	cfg := &Config{
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
		Links:       c.Bool("links"),
		Limit:       c.Int64("limit"),
		Workers:     c.StringSlice("worker"),
		Manager:     c.String("manager"),
		BWLimit:     c.Int("bwlimit"),
		NoHTTPS:     c.Bool("no-https"),
		Verbose:     c.Bool("verbose"),
		Quiet:       c.Bool("quiet"),
		CheckAll:    c.Bool("check-all"),
		CheckNew:    c.Bool("check-new"),
	}
	if cfg.Threads <= 0 {
		logger.Warnf("threads should be larger than 0, reset it to 1")
		cfg.Threads = 1
	}
	return cfg
}
