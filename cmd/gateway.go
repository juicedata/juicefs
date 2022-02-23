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

package main

import (
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"

	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"
	"github.com/urfave/cli/v2"

	mcli "github.com/minio/cli"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/auth"
)

func gatewayFlags() *cli.Command {
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
	}

	compoundFlags := [][]cli.Flag{
		clientFlags(),
		cacheFlags(0),
		selfFlags,
		shareInfoFlags(),
	}

	return &cli.Command{
		Name:      "gateway",
		Usage:     "S3-compatible gateway",
		ArgsUsage: "META-URL ADDRESS",
		Flags:     expandFlags(compoundFlags),
		Action:    gateway,
	}
}

func gateway(c *cli.Context) error {
	setLoggerLevel(c)

	if c.Args().Len() < 2 {
		logger.Fatalf("Meta URL and listen address are required")
	}

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
	removePassword(addr)
	m, store, conf := initForSvc(c, "s3gateway", addr)
	return jfsgateway.NewJFSGateway(conf, m, store, c.Bool("multi-buckets"), c.Bool("keep-etag"))
}

func initForSvc(c *cli.Context, mp string, metaUrl string) (meta.Meta, chunk.ChunkStore, *vfs.Config) {
	readOnly := c.Bool("read-only")
	metaConf := getMetaConf(c, mp, readOnly)
	metaCli := meta.NewClient(metaUrl, metaConf)
	format, err := metaCli.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	if !c.Bool("writeback") && c.IsSet("upload-delay") {
		logger.Warnf("delayed upload only work in writeback mode")
	}

	chunkConf := getChunkConf(c, format)
	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	store := chunk.NewCachedStore(blob, *chunkConf)
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

	initBackgroundTasks(c, metaCli, vfsConf, blob, readOnly)

	return metaCli, store, vfsConf
}
