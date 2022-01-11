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
	"path/filepath"

	"github.com/juicedata/juicefs/pkg/metric"

	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/usage"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"

	mcli "github.com/minio/cli"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/auth"
)

func gatewayFlags() *cli.Command {
	flags := append(clientFlags(),
		&cli.Float64Flag{
			Name:  "attr-cache",
			Value: 1.0,
			Usage: "attributes cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "entry-cache",
			Value: 0,
			Usage: "file entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "dir-entry-cache",
			Value: 1.0,
			Usage: "dir entry cache timeout in seconds",
		},
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "path for JuiceFS access log",
		},
		&cli.StringFlag{
			Name:  "metrics",
			Value: "127.0.0.1:9567",
			Usage: "address to export metrics",
		},
		&cli.StringFlag{
			Name:  "consul",
			Value: "127.0.0.1:8500",
			Usage: "consul address to register",
		},
		&cli.BoolFlag{
			Name:  "no-usage-report",
			Usage: "do not send usage report",
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
		})
	return &cli.Command{
		Name:      "gateway",
		Usage:     "S3-compatible gateway",
		ArgsUsage: "META-URL ADDRESS",
		Flags:     flags,
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
	m := meta.NewClient(addr, &meta.Config{
		Retries:    10,
		Strict:     true,
		ReadOnly:   c.Bool("read-only"),
		OpenCache:  time.Duration(c.Float64("open-cache") * 1e9),
		MountPoint: "s3gateway",
		Subdir:     c.String("subdir"),
		MaxDeletes: c.Int("max-deletes"),
	})
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	wrapRegister("s3gateway", format.Name)

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout:    time.Second * time.Duration(c.Int("get-timeout")),
		PutTimeout:    time.Second * time.Duration(c.Int("put-timeout")),
		MaxUpload:     c.Int("max-uploads"),
		Writeback:     c.Bool("writeback"),
		Prefetch:      c.Int("prefetch"),
		BufferSize:    c.Int("buffer-size") << 20,
		UploadLimit:   c.Int64("upload-limit") * 1e6 / 8,
		DownloadLimit: c.Int64("download-limit") * 1e6 / 8,

		CacheDir:       c.String("cache-dir"),
		CacheSize:      int64(c.Int("cache-size")),
		FreeSpace:      float32(c.Float64("free-space-ratio")),
		CacheMode:      os.FileMode(0600),
		CacheFullBlock: !c.Bool("cache-partial-only"),
		AutoCreate:     true,
	}
	if chunkConf.CacheDir != "memory" {
		ds := utils.SplitDir(chunkConf.CacheDir)
		for i := range ds {
			ds[i] = filepath.Join(ds[i], format.UUID)
		}
		chunkConf.CacheDir = strings.Join(ds, string(os.PathListSeparator))
	}
	if c.IsSet("bucket") {
		format.Bucket = c.String("bucket")
	}
	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	})
	m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	})
	err = m.NewSession()
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}

	conf := &vfs.Config{
		Meta: &meta.Config{
			Retries: 10,
		},
		Format:          format,
		Version:         version.Version(),
		AttrTimeout:     time.Millisecond * time.Duration(c.Float64("attr-cache")*1000),
		EntryTimeout:    time.Millisecond * time.Duration(c.Float64("entry-cache")*1000),
		DirEntryTimeout: time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000),
		AccessLog:       c.String("access-log"),
		Chunk:           &chunkConf,
	}

	metricsAddr := exposeMetrics(m, c)
	if c.IsSet("consul") {
		metric.RegisterToConsul(c.String("consul"), metricsAddr, "s3gateway")
	}
	if d := c.Duration("backup-meta"); d > 0 {
		go vfs.Backup(m, blob, d)
	}
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, "gateway "+version.Version())
	}
	return jfsgateway.NewJFSGateway(conf, m, store, c.Bool("multi-buckets"), c.Bool("keep-etag"))
}
