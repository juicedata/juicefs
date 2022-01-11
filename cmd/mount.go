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
	"net"
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

func exposeMetrics(m meta.Meta, c *cli.Context) string {
	var ip, port string
	//default set
	ip, port, err := net.SplitHostPort(c.String("metrics"))
	if err != nil {
		logger.Fatalf("metrics format error: %v", err)
	}

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

	// If not set metrics addr,the port will be auto set
	if !c.IsSet("metrics") {
		// If only set consul, ip will auto set
		if c.IsSet("consul") {
			ip, err = utils.GetLocalIp(c.String("consul"))
			if err != nil {
				logger.Errorf("Get local ip failed: %v", err)
				return ""
			}
		}
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(ip, port))
	if err != nil {
		// Don't try other ports on metrics set but listen failed
		if c.IsSet("metrics") {
			logger.Errorf("listen on %s:%s failed: %v", ip, port, err)
			return ""
		}
		// Listen port on 0 will auto listen on a free port
		ln, err = net.Listen("tcp", net.JoinHostPort(ip, "0"))
		if err != nil {
			logger.Errorf("Listen failed: %v", err)
			return ""
		}
	}

	go func() {
		if err := http.Serve(ln, nil); err != nil {
			logger.Errorf("Serve for metrics: %s", err)
		}
	}()

	metricsAddr := ln.Addr().String()
	logger.Infof("Prometheus metrics listening on %s", metricsAddr)
	return metricsAddr
}

func wrapRegister(mp, name string) {
	registry := prometheus.NewRegistry() // replace default so only JuiceFS metrics are exposed
	prometheus.DefaultGatherer = registry
	metricLabels := prometheus.Labels{"mp": mp, "vol_name": name}
	prometheus.DefaultRegisterer = prometheus.WrapRegistererWithPrefix("juicefs_",
		prometheus.WrapRegistererWith(metricLabels, registry))
	prometheus.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	prometheus.MustRegister(prometheus.NewGoCollector())
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
		MaxDeletes:  c.Int("max-deletes"),
	}
	m := meta.NewClient(addr, metaConf)
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	// Wrap the default registry, all prometheus.MustRegister() calls should be afterwards
	wrapRegister(mp, format.Name)

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
	conf := &vfs.Config{
		Meta:       metaConf,
		Format:     format,
		Version:    version.Version(),
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}

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
	v := vfs.NewVFS(conf, m, store)
	metricsAddr := exposeMetrics(m, c)
	if c.IsSet("consul") {
		metric.RegisterToConsul(c.String("consul"), metricsAddr, mp)
	}
	if d := c.Duration("backup-meta"); d > 0 {
		go vfs.Backup(m, blob, d)
	}
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, version.Version())
	}
	mount_main(v, c)
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
			Name:  "max-deletes",
			Value: 2,
			Usage: "number of threads to delete objects",
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
			Value: 100 << 10,
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
		&cli.DurationFlag{
			Name:  "backup-meta",
			Value: time.Hour,
			Usage: "interval to automatically backup metadata in the object storage (0 means disable backup)",
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
			&cli.StringFlag{
				Name:  "consul",
				Value: "127.0.0.1:8500",
				Usage: "consul address to register",
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
