package config

import (
	"github.com/urfave/cli/v2"
)

type Config struct {
	Start     string
	End       string
	Threads   int
	HTTPPort  int
	Update    bool
	Dry       bool
	DeleteSrc bool
	DeleteDst bool
	Verbose   bool
	Quiet     bool
}

func NewConfigFromCli(c *cli.Context) *Config {
	return &Config{
		Start:     c.String("start"),
		End:       c.String("end"),
		Threads:   c.Int("threads"),
		HTTPPort:  c.Int("http-port"),
		Update:    c.Bool("update"),
		Dry:       c.Bool("dry"),
		DeleteSrc: c.Bool("delete-src"),
		DeleteDst: c.Bool("delete-dst"),
		Verbose:   c.Bool("verbose"),
		Quiet:     c.Bool("quiet"),
	}
}
