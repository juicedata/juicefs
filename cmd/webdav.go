/*
 *
 *  * JuiceFS, Copyright 2022 Juicedata, Inc.
 *  *
 *  * Licensed under the Apache License, Version 2.0 (the "License");
 *  * you may not use this file except in compliance with the License.
 *  * You may obtain a copy of the License at
 *  *
 *  *     http://www.apache.org/licenses/LICENSE-2.0
 *  *
 *  * Unless required by applicable law or agreed to in writing, software
 *  * distributed under the License is distributed on an "AS IS" BASIS,
 *  * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  * See the License for the specific language governing permissions and
 *  * limitations under the License.
 *
 */

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/metric"
	"github.com/juicedata/juicefs/pkg/usage"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func webDavFlags() *cli.Command {
	flags := append(clientFlags(),
		&cli.BoolFlag{
			Name:  "gzip",
			Usage: "compress served files via gzip",
		},
		&cli.BoolFlag{
			Name:  "disallowList",
			Usage: "disallow list a directory",
		},
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
	)
	return &cli.Command{
		Name:      "webdav",
		Usage:     "start a webdav server",
		ArgsUsage: "META-URL ADDRESS",
		Flags:     flags,
		Action:    webdavSvc,
	}
}

func webdavSvc(c *cli.Context) error {
	setLoggerLevel(c)
	if c.Args().Len() < 1 {
		logger.Fatalf("meta url are required")
	}
	metaUrl := c.Args().Get(0)
	if c.Args().Len() < 2 {
		logger.Fatalf("listen address is required")
	}
	listenAddr := c.Args().Get(1)

	mp := "webdav"
	metaConf := &meta.Config{
		Retries:    10,
		Strict:     true,
		ReadOnly:   c.Bool("read-only"),
		OpenCache:  time.Duration(c.Float64("open-cache") * 1e9),
		MountPoint: mp,
		Subdir:     c.String("subdir"),
		MaxDeletes: c.Int("max-deletes"),
	}
	m := meta.NewClient(metaUrl, metaConf)
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
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
		AccessLog:       c.String("access-log"),
		Meta:            metaConf,
		Format:          format,
		Version:         version.Version(),
		Chunk:           &chunkConf,
		AttrTimeout:     time.Millisecond * time.Duration(c.Float64("attr-cache")*1000),
		EntryTimeout:    time.Millisecond * time.Duration(c.Float64("entry-cache")*1000),
		DirEntryTimeout: time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000),
	}

	metricsAddr := exposeMetrics(m, c)
	if c.IsSet("consul") {
		metric.RegisterToConsul(c.String("consul"), metricsAddr, mp)
	}
	if d := c.Duration("backup-meta"); d > 0 && !c.Bool("read-only") {
		go vfs.Backup(m, blob, d)
	}
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, fmt.Sprintf("%s %s", mp, version.Version()))
	}
	jfs, err := fs.NewFileSystem(conf, m, store)
	if err != nil {
		return fmt.Errorf("initialize failed: %s", err)
	}
	fs.StartHTTPServer(jfs, listenAddr, c.Bool("gzip"), c.Bool("disallowList"))
	return m.CloseSession()
}
