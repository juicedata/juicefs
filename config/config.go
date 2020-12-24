// Copyright (C) 2020-present Juicedata Inc.

package config

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
	Verbose     bool
	Quiet       bool
}

func NewConfigFromCli(c *cli.Context) *Config {
	return &Config{
		Start:       c.String("start"),
		End:         c.String("end"),
		Threads:     c.Int("threads"),
		HTTPPort:    c.Int("http-port"),
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
		Verbose:     c.Bool("verbose"),
		Quiet:       c.Bool("quiet"),
	}
}
