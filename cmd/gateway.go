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
	"context"
	"errors"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path"
	"strconv"
	"syscall"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"

	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"
	"github.com/urfave/cli/v2"

	mcli "github.com/minio/cli"
	minio "github.com/minio/minio/cmd"
)

func cmdGateway() *cli.Command {
	selfFlags := []cli.Flag{
		&cli.StringFlag{
			Name:  "log",
			Usage: "path for gateway log",
			Value: path.Join(getDefaultLogDir(), "juicefs-gateway.log"),
		},
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "path for JuiceFS access log",
		},
		&cli.BoolFlag{
			Name:    "background",
			Aliases: []string{"d"},
			Usage:   "run in background",
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
		&cli.BoolFlag{
			Name:  "object-tag",
			Usage: "enable object tagging api",
		},
		&cli.BoolFlag{
			Name:  "object-meta",
			Usage: "enable object metadata api",
		},
		&cli.StringFlag{
			Name:  "domain",
			Usage: "domain for virtual-host-style requests",
		},
		&cli.StringFlag{
			Name:  "refresh-iam-interval",
			Value: "5m",
			Usage: "interval to reload gateway IAM from configuration",
		},
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
		Flags: expandFlags(selfFlags, clientFlags(0), shareInfoFlags()),
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
	if c.IsSet("domain") {
		os.Setenv("MINIO_DOMAIN", c.String("domain"))
	}

	if c.IsSet("refresh-iam-interval") {
		os.Setenv("MINIO_REFRESH_IAM_INTERVAL", c.String("refresh-iam-interval"))
	}

	metaAddr := c.Args().Get(0)
	listenAddr := c.Args().Get(1)
	conf, jfs := initForSvc(c, "s3gateway", metaAddr, listenAddr)

	umask, err := strconv.ParseUint(c.String("umask"), 8, 16)
	if err != nil {
		logger.Fatalf("invalid umask %s: %s", c.String("umask"), err)
	}

	jfsGateway, err = jfsgateway.NewJFSGateway(
		jfs,
		conf,
		&jfsgateway.Config{
			MultiBucket:   c.Bool("multi-buckets"),
			KeepEtag:      c.Bool("keep-etag"),
			Umask:         uint16(umask),
			ObjTag:        c.Bool("object-tag"),
			ObjMeta:       c.Bool("object-meta"),
		},
	)
	if err != nil {
		return err
	}
	if c.IsSet("read-only") {
		os.Setenv("JUICEFS_META_READ_ONLY", "1")
	} else {
		if _, err := jfsGateway.GetBucketInfo(context.Background(), minio.MinioMetaBucket); errors.As(err, &minio.BucketNotFound{}) {
			if err := jfsGateway.MakeBucketWithLocation(context.Background(), minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
				logger.Fatalf("init MinioMetaBucket error %s: %s", minio.MinioMetaBucket, err)
			}
		}
	}

	args := []string{"server", "--address", listenAddr, "--anonymous"}
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

var jfsGateway minio.ObjectLayer

func gateway2(ctx *mcli.Context) error {
	minio.ServerMainForJFS(ctx, jfsGateway)
	return nil
}

func initForSvc(c *cli.Context, mp string, metaUrl, listenAddr string) (*vfs.Config, *fs.FileSystem) {
	removePassword(metaUrl)
	metaConf := getMetaConf(c, mp, c.Bool("read-only"))
	metaCli := meta.NewClient(metaUrl, metaConf)
	if c.Bool("background") {
		if err := makeDaemonForSvc(c, metaCli, metaUrl, listenAddr); err != nil {
			logger.Fatalf("make daemon: %s", err)
		}
	}

	format, err := metaCli.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	if st := metaCli.Chroot(meta.Background(), metaConf.Subdir); st != 0 {
		logger.Fatalf("Chroot to %s: %s", metaConf.Subdir, st)
	}
	registerer, registry := wrapRegister(c, mp, format.Name)

	blob, err := NewReloadableStorage(format, metaCli, updateFormat(c))
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	chunkConf := getChunkConf(c, format)
	store := chunk.NewCachedStore(blob, *chunkConf, registerer)
	registerMetaMsg(metaCli, store, chunkConf)

	err = metaCli.NewSession(true)
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}
	metaCli.OnReload(func(fmt *meta.Format) {
		updateFormat(c)(fmt)
		store.UpdateLimit(fmt.UploadLimit, fmt.DownloadLimit)
	})

	// Go will catch all the signals
	signal.Ignore(syscall.SIGPIPE)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		sig := <-signalChan
		logger.Infof("Received signal %s, exiting...", sig.String())
		if err := metaCli.CloseSession(); err != nil {
			logger.Fatalf("close session failed: %s", err)
		}
		object.Shutdown(blob)
		os.Exit(0)
	}()
	vfsConf := getVfsConf(c, metaConf, format, chunkConf)
	vfsConf.AccessLog = c.String("access-log")
	vfsConf.AttrTimeout = utils.Duration(c.String("attr-cache"))
	vfsConf.EntryTimeout = utils.Duration(c.String("entry-cache"))
	vfsConf.DirEntryTimeout = utils.Duration(c.String("dir-entry-cache"))

	initBackgroundTasks(c, vfsConf, metaConf, metaCli, blob, registerer, registry)
	jfs, err := fs.NewFileSystem(vfsConf, metaCli, store)
	if err != nil {
		logger.Fatalf("Initialize failed: %s", err)
	}
	jfs.InitMetrics(registerer)

	return vfsConf, jfs
}
