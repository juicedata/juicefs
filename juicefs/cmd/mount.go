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
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/gops/agent"
	"github.com/juicedata/godaemon"
	"github.com/urfave/cli/v2"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

func MakeDaemon(onExit func(int) error) error {
	_, _, err := godaemon.MakeDaemon(&godaemon.DaemonAttr{OnExit: onExit})
	return err
}

func installHandler(mp string) {
	// Go will catch all the signals
	signal.Ignore(syscall.SIGPIPE)
	signalChan := make(chan os.Signal, 10)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for {
			<-signalChan
			go func() {
				if runtime.GOOS == "linux" {
					_ = exec.Command("umount", mp, "-l").Run()
				} else if runtime.GOOS == "darwin" {
					_ = exec.Command("diskutil", "umount", "force", mp).Run()
				}
			}()
			go func() {
				time.Sleep(time.Second * 3)
				os.Exit(1)
			}()
		}
	}()
}

func mount(c *cli.Context) error {
	go func() {
		for port := 6060; port < 6100; port++ {
			_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), nil)
		}
	}()
	go func() {
		for port := 6070; port < 6100; port++ {
			_ = agent.Listen(agent.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
		}
	}()
	setLoggerLevel(c)
	if !c.Bool("no-syslog") {
		// The default log to syslog is only in daemon mode.
		utils.InitLoggers(c.Bool("background"))
	}
	if c.Args().Len() < 1 {
		logger.Fatalf("Redis URL and mountpoint are required")
	}
	addr := c.Args().Get(0)
	if !strings.Contains(addr, "://") {
		addr = "redis://" + addr
	}
	if c.Args().Len() < 2 {
		logger.Fatalf("MOUNTPOINT is required")
	}
	mp := c.Args().Get(1)
	if !utils.Exists(mp) {
		if err := os.MkdirAll(mp, 0777); err != nil {
			logger.Fatalf("create %s: %s", mp, err)
		}
	}

	logger.Infof("Meta address: %s", addr)
	var rc = meta.RedisConfig{Retries: 10, Strict: true}
	m, err := meta.NewRedisMeta(addr, &rc)
	if err != nil {
		logger.Fatalf("Meta: %s", err)
	}
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout:  time.Second * time.Duration(c.Int("get-timeout")),
		PutTimeout:  time.Second * time.Duration(c.Int("put-timeout")),
		MaxUpload:   c.Int("max-uploads"),
		AsyncUpload: c.Bool("writeback"),
		Prefetch:    c.Int("prefetch"),
		BufferSize:  c.Int("buffer-size") << 20,

		CacheDir:       c.String("cache-dir"),
		CacheSize:      int64(c.Int("cache-size")),
		FreeSpace:      float32(c.Float64("free-space-ratio")),
		CacheMode:      os.FileMode(0600),
		CacheFullBlock: !c.Bool("cache-partial-only"),
		AutoCreate:     true,
	}
	if chunkConf.CacheDir != "memory" {
		chunkConf.CacheDir = filepath.Join(chunkConf.CacheDir, format.UUID)
	}
	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	logger.Infof("Mounting volume %s at %s ...", format.Name, mp)

	if c.Bool("background") {
		err := MakeDaemon(func(stage int) error {
			if stage != 0 {
				return nil
			}
			for {
				time.Sleep(time.Millisecond * 50)
				st, err := os.Stat(mp)
				if err == nil {
					if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == 1 {
						logger.Infof("\033[92mOK\033[0m, %s is ready at %s", format.Name, mp)
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			logger.Fatalf("Failed to make daemon: %s", err)
		}
	}

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
		Meta: &meta.Config{
			IORetries: 10,
		},
		Format:     format,
		Version:    Version(),
		Mountpoint: mp,
		Primary: &vfs.StorageConfig{
			Name:      format.Storage,
			Endpoint:  format.Bucket,
			AccessKey: format.AccessKey,
			SecretKey: format.SecretKey,
		},
		Chunk: &chunkConf,
	}
	vfs.Init(conf, m, store)

	installHandler(mp)
	if !c.Bool("no-usage-report") {
		go reportUsage(m)
	}
	err = fuse.Main(conf, c.String("o"), c.Float64("attr-cache"), c.Float64("entry-cache"), c.Float64("dir-entry-cache"), c.Bool("enable-xattr"))
	if err != nil {
		logger.Fatalf("fuse: %s", err)
	}
	return nil
}

func mountFlags() *cli.Command {
	var defaultCacheDir = "/var/jfsCache"
	if runtime.GOOS == "darwin" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	}
	return &cli.Command{
		Name:      "mount",
		Usage:     "mount a volume",
		ArgsUsage: "REDIS-URL MOUNTPOINT",
		Action:    mount,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "d",
				Aliases: []string{"background"},
				Usage:   "run in background",
			},
			&cli.BoolFlag{
				Name:  "no-syslog",
				Usage: "disable syslog",
			},

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
				Usage: "total read/write buffering in MiB",
			},
			&cli.IntFlag{
				Name:  "prefetch",
				Value: 3,
				Usage: "prefetch N blocks in parallel",
			},

			&cli.BoolFlag{
				Name:  "writeback",
				Usage: "Upload objects in background",
			},
			&cli.StringFlag{
				Name:  "cache-dir",
				Value: defaultCacheDir,
				Usage: "directory to cache object",
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
				Name:  "no-usage-report",
				Usage: "do not send usage report",
			},
		},
	}
}
