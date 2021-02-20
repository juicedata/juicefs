// +build !windows,!darwin

package main

import (
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func mount_flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "o",
			Usage: "other FUSE options",
		},
		&cli.Float64Flag{
			Name:  "attr-cache",
			Value: 1.0,
			Usage: "attributes cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "entry-cache",
			Value: 1.0,
			Usage: "file entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "dir-entry-cache",
			Value: 1.0,
			Usage: "dir entry cache timeout in seconds",
		},
		&cli.BoolFlag{
			Name:  "enable-xattr",
			Usage: "enable extended attributes (xattr)",
		},
	}
}

func mount_main(conf *vfs.Config, m meta.Meta, store chunk.ChunkStore, c *cli.Context) {
	err := fuse.Serve(conf, c.String("o"), c.Float64("attr-cache"), c.Float64("entry-cache"), c.Float64("dir-entry-cache"), c.Bool("enable-xattr"))
	if err != nil {
		logger.Fatalf("fuse: %s", err)
	}
}
