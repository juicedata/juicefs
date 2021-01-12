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
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/VividCortex/godaemon"
	"github.com/google/gops/agent"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/redis"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

func MakeDaemon() {
	godaemon.MakeDaemon(&godaemon.DaemonAttr{})
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
					exec.Command("umount", mp, "-l").Run()
				} else if runtime.GOOS == "darwin" {
					exec.Command("diskutil", "umount", "force", mp).Run()
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
			http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), nil)
		}
	}()
	go func() {
		for port := 6070; port < 6100; port++ {
			agent.Listen(agent.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
		}
	}()
	if c.Bool("trace") {
		utils.SetLogLevel(logrus.TraceLevel)
	} else if c.Bool("debug") {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if c.Bool("quiet") {
		utils.SetLogLevel(logrus.ErrorLevel)
		utils.InitLoggers(!c.Bool("nosyslog"))
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
	var rc = redis.RedisConfig{Retries: 10, Strict: true}
	m, err := redis.NewRedisMeta(addr, &rc)
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

		GetTimeout:  time.Second * time.Duration(c.Int("getTimeout")),
		PutTimeout:  time.Second * time.Duration(c.Int("putTimeout")),
		MaxUpload:   c.Int("maxUpload"),
		AsyncUpload: c.Bool("writeback"),
		Prefetch:    c.Int("prefetch"),
		BufferSize:  c.Int("bufferSize") << 20,

		CacheDir:       c.String("cacheDir"),
		CacheSize:      int64(c.Int("cacheSize")),
		FreeSpace:      float32(c.Float64("freeRatio")),
		CacheMode:      os.FileMode(0600),
		CacheFullBlock: !c.Bool("partialOnly"),
		AutoCreate:     true,
	}
	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	logger.Infof("mount volume %s at %s", format.Name, mp)

	if c.Bool("d") {
		MakeDaemon()
	}

	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	}))
	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		slices := args[1].([]meta.Slice)
		return compact(chunkConf, store, slices, chunkid)
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
			SecretKey: format.AccessKey,
		},
		Chunk: &chunkConf,
	}
	vfs.Init(conf, m, store)

	installHandler(mp)
	if !c.Bool("no-usage-report") {
		go reportUsage(m)
	}
	err = fuse.Main(conf, c.String("o"), c.Float64("attrcacheto"), c.Float64("entrycacheto"), c.Float64("direntrycacheto"))
	if err != nil {
		logger.Errorf("%s", err)
		os.Exit(1)
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
				Name:  "d",
				Usage: "run in background",
			},
			&cli.StringFlag{
				Name:  "o",
				Usage: "other fuse options",
			},
			&cli.Float64Flag{
				Name:  "attrcacheto",
				Value: 1.0,
				Usage: "attributes cache timeout in seconds",
			},
			&cli.Float64Flag{
				Name:  "entrycacheto",
				Value: 1.0,
				Usage: "file entry cache timeout in seconds",
			},
			&cli.Float64Flag{
				Name:  "direntrycacheto",
				Value: 1.0,
				Usage: "dir entry cache timeout in seconds",
			},

			&cli.IntFlag{
				Name:  "getTimeout",
				Value: 60,
				Usage: "the max number of seconds to download an object",
			},
			&cli.IntFlag{
				Name:  "putTimeout",
				Value: 60,
				Usage: "the max number of seconds to upload an object",
			},
			&cli.IntFlag{
				Name:  "ioretries",
				Value: 30,
				Usage: "number of retries after network failure",
			},
			&cli.IntFlag{
				Name:  "maxUpload",
				Value: 20,
				Usage: "number of connections to upload",
			},
			&cli.IntFlag{
				Name:  "bufferSize",
				Value: 300,
				Usage: "total read/write buffering in MB",
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
				Name:  "cacheDir",
				Value: defaultCacheDir,
				Usage: "directory to cache object",
			},
			&cli.IntFlag{
				Name:  "cacheSize",
				Value: 1 << 10,
				Usage: "size of cached objects in MiB",
			},
			&cli.Float64Flag{
				Name:  "freeSpace",
				Value: 0.1,
				Usage: "min free space (ratio)",
			},
			&cli.BoolFlag{
				Name:  "partialOnly",
				Usage: "cache only random/small read",
			},

			&cli.BoolFlag{
				Name:  "no-usage-report",
				Usage: "do not send usage report to juicefs.io",
			},
		},
	}
}

func readSlice(store chunk.ChunkStore, s *meta.Slice, page *chunk.Page, off int) error {
	buf := page.Data
	read := 0
	reader := store.NewReader(s.Chunkid, int(s.Size))
	for read < len(buf) {
		p := page.Slice(read, len(buf)-read)
		n, err := reader.ReadAt(context.Background(), p, off+int(s.Off))
		p.Release()
		if n == 0 && err != nil {
			return err
		}
		read += n
		off += n
	}
	return nil
}

func compact(conf chunk.Config, store chunk.ChunkStore, slices []meta.Slice, chunkid uint64) error {
	writer := store.NewWriter(chunkid)
	defer writer.Abort()

	var pos int
	for i, s := range slices {
		if s.Chunkid == 0 {
			_, err := writer.WriteAt(make([]byte, int(s.Len)), int64(pos))
			if err != nil {
				return err
			}
			pos += int(s.Len)
			continue
		}
		var read int
		for read < int(s.Len) {
			l := utils.Min(conf.BlockSize, int(s.Len)-read)
			p := chunk.NewOffPage(l)
			if err := readSlice(store, &s, p, read); err != nil {
				logger.Infof("can't compact chunk %d, retry later, read %d: %s", chunkid, i, err)
				p.Release()
				return err
			}
			_, err := writer.WriteAt(p.Data, int64(pos+read))
			p.Release()
			if err != nil {
				logger.Errorf("can't compact chunk %d, retry later, write: %s", chunkid, err)
				return err
			}
			read += l
			if pos+read >= conf.BlockSize {
				if err = writer.FlushTo(pos + read); err != nil {
					panic(err)
				}
			}
		}
		pos += int(s.Len)
	}
	return writer.Finish(pos)
}
