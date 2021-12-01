/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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

package main

import (
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/cli/v2"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/metric"
	"github.com/juicedata/juicefs/pkg/usage"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
)

func installHandler(mp string) {
	// Go will catch all the signals
	signal.Ignore(syscall.SIGPIPE)
	signalChan := make(chan os.Signal, 10)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for {
			<-signalChan
			go func() { _ = doUmount(mp, true) }()
			go func() {
				time.Sleep(time.Second * 3)
				os.Exit(1)
			}()
		}
	}()
}

func exposeMetrics(m meta.Meta, addr string) {
	meta.InitMetrics()
	vfs.InitMetrics()
	go metric.UpdateMetrics(m)
	http.Handle("/metrics", promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{
			// Opt into OpenMetrics to support exemplars.
			EnableOpenMetrics: true,
		},
	))
	prometheus.MustRegister(prometheus.NewBuildInfoCollector())
	go func() {
		err := http.ListenAndServe(addr, nil)
		if err != nil {
			logger.Errorf("listen and serve for metrics: %s", err)
		}
	}()
}

func mount(c *cli.Context) error {
	setLoggerLevel(c)
	if c.Args().Len() < 1 {
		logger.Fatalf("Meta URL and mountpoint are required")
	}
	addr := c.Args().Get(0)
	if c.Args().Len() < 2 {
		logger.Fatalf("MOUNTPOINT is required")
	}
	mp := c.Args().Get(1)
	fi, err := os.Stat(mp)
	if !strings.Contains(mp, ":") && err != nil {
		if err := os.MkdirAll(mp, 0777); err != nil {
			if os.IsExist(err) {
				// a broken mount point, umount it
				if err = doUmount(mp, true); err != nil {
					logger.Fatalf("umount %s: %s", mp, err)
				}
			} else {
				logger.Fatalf("create %s: %s", mp, err)
			}
		}
	} else if err == nil && fi.Size() == 0 {
		// a broken mount point, umount it
		if err = doUmount(mp, true); err != nil {
			logger.Fatalf("umount %s: %s", mp, err)
		}
	}
	var readOnly = c.Bool("read-only")
	for _, o := range strings.Split(c.String("o"), ",") {
		if o == "ro" {
			readOnly = true
		}
	}
	metaConf := &meta.Config{
		Retries:     10,
		Strict:      true,
		CaseInsensi: strings.HasSuffix(mp, ":") && runtime.GOOS == "windows",
		ReadOnly:    readOnly,
		OpenCache:   time.Duration(c.Float64("open-cache") * 1e9),
		MountPoint:  mp,
		Subdir:      c.String("subdir"),
	}
	m := meta.NewClient(addr, metaConf)
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	metricLabels := prometheus.Labels{
		"vol_name": format.Name,
		"mp":       mp,
	}
	// Wrap the default registry, all prometheus.MustRegister() calls should be afterwards
	prometheus.DefaultRegisterer = prometheus.WrapRegistererWith(metricLabels,
		prometheus.WrapRegistererWithPrefix("juicefs_", prometheus.DefaultRegisterer))

	if !c.Bool("writeback") && c.IsSet("upload-delay") {
		logger.Warnf("delayed upload only work in writeback mode")
	}

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout:    time.Second * time.Duration(c.Int("get-timeout")),
		PutTimeout:    time.Second * time.Duration(c.Int("put-timeout")),
		MaxUpload:     c.Int("max-uploads"),
		Writeback:     c.Bool("writeback"),
		UploadDelay:   c.Duration("upload-delay"),
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
	m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	}))
	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	}))
	conf := &vfs.Config{
		Meta:       metaConf,
		Format:     format,
		Version:    version.Version(),
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}
	vfs.Init(conf, m, store)

	if c.Bool("background") && os.Getenv("JFS_FOREGROUND") == "" {
		if runtime.GOOS != "windows" {
			d := c.String("cache-dir")
			if d != "memory" && !strings.HasPrefix(d, "/") {
				ad, err := filepath.Abs(d)
				if err != nil {
					logger.Fatalf("cache-dir should be absolute path in daemon mode")
				} else {
					for i, a := range os.Args {
						if a == d || a == "--cache-dir="+d {
							os.Args[i] = a[:len(a)-len(d)] + ad
						}
					}
				}
			}
		}
		sqliteScheme := "sqlite3://"
		if strings.HasPrefix(addr, sqliteScheme) {
			path := addr[len(sqliteScheme):]
			path2, err := filepath.Abs(path)
			if err == nil && path2 != path {
				for i, a := range os.Args {
					if a == addr {
						os.Args[i] = sqliteScheme + path2
					}
				}
			}
		}
		// The default log to syslog is only in daemon mode.
		utils.InitLoggers(!c.Bool("no-syslog"))
		err := makeDaemon(c, conf.Format.Name, conf.Mountpoint)
		if err != nil {
			logger.Fatalf("Failed to make daemon: %s", err)
		}
	} else {
		go checkMountpoint(conf.Format.Name, mp)
	}

	err = m.NewSession()
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}
	installHandler(mp)
	exposeMetrics(m, c.String("metrics"))
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, version.Version())
	}
	mount_main(conf, m, store, c)
	return m.CloseSession()
}

func clientFlags() []cli.Flag {
	var defaultCacheDir = "/var/jfsCache"
	switch runtime.GOOS {
	case "darwin":
		fallthrough
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	}
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "bucket",
			Usage: "customized endpoint to access object store",
		},
		&cli.IntFlag{
			Name:  "get-timeout",
			Value: 60,
			Usage: "the max number of seconds to download an object",
		},
		&cli.IntFlag{
			Name:  "put-timeout",
			Value: 60,
			Usage: "the max number of seconds to upload an object",
		},
		&cli.IntFlag{
			Name:  "io-retries",
			Value: 30,
			Usage: "number of retries after network failure",
		},
		&cli.IntFlag{
			Name:  "max-uploads",
			Value: 20,
			Usage: "number of connections to upload",
		},
		&cli.IntFlag{
			Name:  "buffer-size",
			Value: 300,
			Usage: "total read/write buffering in MB",
		},
		&cli.Int64Flag{
			Name:  "upload-limit",
			Value: 0,
			Usage: "bandwidth limit for upload in Mbps",
		},
		&cli.Int64Flag{
			Name:  "download-limit",
			Value: 0,
			Usage: "bandwidth limit for download in Mbps",
		},

		&cli.IntFlag{
			Name:  "prefetch",
			Value: 1,
			Usage: "prefetch N blocks in parallel",
		},
		&cli.BoolFlag{
			Name:  "writeback",
			Usage: "upload objects in background",
		},
		&cli.DurationFlag{
			Name:  "upload-delay",
			Usage: "delayed duration for uploading objects (\"s\", \"m\", \"h\")",
		},
		&cli.StringFlag{
			Name:  "cache-dir",
			Value: defaultCacheDir,
			Usage: "directory paths of local cache, use colon to separate multiple paths",
		},
		&cli.IntFlag{
			Name:  "cache-size",
			Value: 1 << 10,
			Usage: "size of cached objects in MiB",
		},
		&cli.Float64Flag{
			Name:  "free-space-ratio",
			Value: 0.1,
			Usage: "min free space (ratio)",
		},
		&cli.BoolFlag{
			Name:  "cache-partial-only",
			Usage: "cache only random/small read",
		},

		&cli.BoolFlag{
			Name:  "read-only",
			Usage: "allow lookup/read operations only",
		},
		&cli.Float64Flag{
			Name:  "open-cache",
			Value: 0.0,
			Usage: "open files cache timeout in seconds (0 means disable this feature)",
		},
		&cli.StringFlag{
			Name:  "subdir",
			Usage: "mount a sub-directory as root",
		},
	}
}

func mountFlags() *cli.Command {
	cmd := &cli.Command{
		Name:      "mount",
		Usage:     "mount a volume",
		ArgsUsage: "META-URL MOUNTPOINT",
		Action:    mount,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "metrics",
				Value: "127.0.0.1:9567",
				Usage: "address to export metrics",
			},
			&cli.BoolFlag{
				Name:  "no-usage-report",
				Usage: "do not send usage report",
			},
		},
	}
	cmd.Flags = append(cmd.Flags, mount_flags()...)
	cmd.Flags = append(cmd.Flags, clientFlags()...)
	return cmd
}
