//go:build !nogateway
// +build !nogateway

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
	_ "net/http/pprof"
	"os"
	"strconv"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"

	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"
	"github.com/urfave/cli/v2"

	mcli "github.com/minio/cli"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/auth"
)

func cmdGateway() *cli.Command {
	selfFlags := []cli.Flag{
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "path for JuiceFS access log",
		},
		&cli.BoolFlag{
			Name:  "no-banner",
			Usage: "disable MinIO startup information",
		},
		&cli.BoolFlag{
			Name:  "multi-buckets",
			Usage: "use top level of directories as buckets",
		},
		&cli.BoolFlag{
			Name:  "keep-etag",
			Usage: "keep the ETag for uploaded objects",
		},
		&cli.StringFlag{
			Name:  "umask",
			Value: "022",
			Usage: "umask for new files and directories in octal",
		},
	}

	compoundFlags := [][]cli.Flag{
		clientFlags(),
		cacheFlags(0),
		selfFlags,
		shareInfoFlags(),
	}

	return &cli.Command{
		Name:      "gateway",
		Action:    gateway,
		Category:  "SERVICE",
		Usage:     "Start an S3-compatible gateway",
		ArgsUsage: "META-URL ADDRESS",
		Description: `
It is implemented based on the MinIO S3 Gateway. Before starting the gateway, you need to set
MINIO_ROOT_USER and MINIO_ROOT_PASSWORD environment variables, which are the access key and secret
key used for accessing S3 APIs.

Examples:
$ export MINIO_ROOT_USER=admin
$ export MINIO_ROOT_PASSWORD=12345678
$ juicefs gateway redis://localhost localhost:9000

Details: https://juicefs.com/docs/community/s3_gateway`,
		Flags: expandFlags(compoundFlags),
	}
}

func gateway(c *cli.Context) error {
	setup(c, 2)
	ak := os.Getenv("MINIO_ROOT_USER")
	if ak == "" {
		ak = os.Getenv("MINIO_ACCESS_KEY")
	}
	if len(ak) < 3 {
		logger.Fatalf("MINIO_ROOT_USER should be specified as an environment variable with at least 3 characters")
	}
	sk := os.Getenv("MINIO_ROOT_PASSWORD")
	if sk == "" {
		sk = os.Getenv("MINIO_SECRET_KEY")
	}
	if len(sk) < 8 {
		logger.Fatalf("MINIO_ROOT_PASSWORD should be specified as an environment variable with at least 8 characters")
	}

	address := c.Args().Get(1)
	gw = &GateWay{c}

	args := []string{"gateway", "--address", address, "--anonymous"}
	if c.Bool("no-banner") {
		args = append(args, "--quiet")
	}
	app := &mcli.App{
		Action: gateway2,
		Flags: []mcli.Flag{
			mcli.StringFlag{
				Name:  "address",
				Value: ":9000",
				Usage: "bind to a specific ADDRESS:PORT, ADDRESS can be an IP or hostname",
			},
			mcli.BoolFlag{
				Name:  "anonymous",
				Usage: "hide sensitive information from logging",
			},
			mcli.BoolFlag{
				Name:  "json",
				Usage: "output server logs and startup information in json format",
			},
			mcli.BoolFlag{
				Name:  "quiet",
				Usage: "disable MinIO startup information",
			},
		},
	}
	return app.Run(args)
}

var gw *GateWay

func gateway2(ctx *mcli.Context) error {
	minio.StartGateway(ctx, gw)
	return nil
}

type GateWay struct {
	ctx *cli.Context
}

func (g *GateWay) Name() string {
	return "JuiceFS"
}

func (g *GateWay) Production() bool {
	return true
}

func (g *GateWay) NewGatewayLayer(creds auth.Credentials) (minio.ObjectLayer, error) {
	c := g.ctx
	addr := c.Args().Get(0)
	conf, jfs := initForSvc(c, "s3gateway", addr)

	umask, err := strconv.ParseUint(c.String("umask"), 8, 16)
	if err != nil {
		logger.Fatalf("invalid umask %s: %s", c.String("umask"), err)
	}

	return jfsgateway.NewJFSGateway(
		jfs,
		conf,
		&jfsgateway.Config{
			MultiBucket: c.Bool("multi-buckets"),
			KeepEtag:    c.Bool("keep-etag"),
			Mode:        uint16(0666 &^ umask),
			DirMode:     uint16(0777 &^ umask),
		},
	)
}

func initForSvc(c *cli.Context, mp string, metaUrl string) (*vfs.Config, *fs.FileSystem) {
	removePassword(metaUrl)
	metaConf := getMetaConf(c, mp, c.Bool("read-only"))
	metaCli := meta.NewClient(metaUrl, metaConf)
	format, err := metaCli.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	registerer, registry := wrapRegister(mp, format.Name)

	blob, err := NewReloadableStorage(format, func() (*meta.Format, error) {
		return getFormat(c, metaCli)
	})
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	chunkConf := getChunkConf(c, format)
	store := chunk.NewCachedStore(blob, *chunkConf, registerer)
	registerMetaMsg(metaCli, store, chunkConf)

	err = metaCli.NewSession()
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}

	vfsConf := getVfsConf(c, metaConf, format, chunkConf)
	vfsConf.AccessLog = c.String("access-log")
	vfsConf.AttrTimeout = time.Millisecond * time.Duration(c.Float64("attr-cache")*1000)
	vfsConf.EntryTimeout = time.Millisecond * time.Duration(c.Float64("entry-cache")*1000)
	vfsConf.DirEntryTimeout = time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000)

	initBackgroundTasks(c, vfsConf, metaConf, metaCli, blob, registerer, registry)
	jfs, err := fs.NewFileSystem(vfsConf, metaCli, store)
	if err != nil {
		logger.Fatalf("Initialize failed: %s", err)
	}
	jfs.InitMetrics(registerer)

	return vfsConf, jfs
}
