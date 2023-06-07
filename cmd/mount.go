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
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/object"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
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

func cmdMount() *cli.Command {
	return &cli.Command{
		Name:      "mount",
		Action:    mount,
		Category:  "SERVICE",
		Usage:     "Mount a volume",
		ArgsUsage: "META-URL MOUNTPOINT",
		Description: `
Mount the target volume at the mount point.

Examples:
# Mount in foreground
$ juicefs mount redis://localhost /mnt/jfs

# Mount in background with password protected Redis
$ juicefs mount redis://:mypassword@localhost /mnt/jfs -d
# A safer alternative
$ META_PASSWORD=mypassword juicefs mount redis://localhost /mnt/jfs -d

# Mount with a sub-directory as root
$ juicefs mount redis://localhost /mnt/jfs --subdir /dir/in/jfs

# Enable "writeback" mode, which improves performance at the risk of losing objects
$ juicefs mount redis://localhost /mnt/jfs -d --writeback

# Enable "read-only" mode
$ juicefs mount redis://localhost /mnt/jfs -d --read-only

# Disable metadata backup
$ juicefs mount redis://localhost /mnt/jfs --backup-meta 0`,
		Flags: expandFlags(mount_flags(), clientFlags(1.0), shareInfoFlags()),
	}
}

func installHandler(mp string) {
	// Go will catch all the signals
	signal.Ignore(syscall.SIGPIPE)
	signalChan := make(chan os.Signal, 10)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for {
			sig := <-signalChan
			logger.Infof("Received signal %s, exiting...", sig.String())
			go func() { _ = doUmount(mp, true) }()
			go func() {
				time.Sleep(time.Second * 3)
				logger.Warnf("Umount not finished after 3 seconds, force exit")
				os.Exit(1)
			}()
		}
	}()
}

func exposeMetrics(c *cli.Context, m meta.Meta, registerer prometheus.Registerer, registry *prometheus.Registry) string {
	var ip, port string
	//default set
	ip, port, err := net.SplitHostPort(c.String("metrics"))
	if err != nil {
		logger.Fatalf("metrics format error: %v", err)
	}

	m.InitMetrics(registerer)
	vfs.InitMetrics(registerer)
	go metric.UpdateMetrics(m, registerer)
	http.Handle("/metrics", promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			// Opt into OpenMetrics to support exemplars.
			EnableOpenMetrics: true,
		},
	))
	registerer.MustRegister(collectors.NewBuildInfoCollector())

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

func wrapRegister(mp, name string) (prometheus.Registerer, *prometheus.Registry) {
	registry := prometheus.NewRegistry() // replace default so only JuiceFS metrics are exposed
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_",
		prometheus.WrapRegistererWith(prometheus.Labels{"mp": mp, "vol_name": name}, registry))
	registerer.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registerer.MustRegister(collectors.NewGoCollector())
	return registerer, registry
}

func updateFormat(c *cli.Context) func(*meta.Format) {
	return func(format *meta.Format) {
		if c.IsSet("bucket") {
			format.Bucket = c.String("bucket")
		}
		if c.IsSet("storage") {
			format.Storage = c.String("storage")
		}
		if c.IsSet("storage-class") {
			format.StorageClass = c.String("storage-class")
		}
		if c.IsSet("upload-limit") {
			format.UploadLimit = c.Int64("upload-limit")
		}
		if c.IsSet("download-limit") {
			format.DownloadLimit = c.Int64("download-limit")
		}
	}
}

func daemonRun(c *cli.Context, addr string, vfsConf *vfs.Config, m meta.Meta) {
	if runtime.GOOS != "windows" {
		if cd := c.String("cache-dir"); cd != "memory" {
			ds := utils.SplitDir(cd)
			for i, d := range ds {
				if strings.HasPrefix(d, "/") {
					continue
				} else if strings.HasPrefix(d, "~/") {
					if h, err := os.UserHomeDir(); err == nil {
						ds[i] = filepath.Join(h, d[1:])
					} else {
						logger.Fatalf("Expand user home dir of %s: %s", d, err)
					}
				} else {
					if ad, err := filepath.Abs(d); err == nil {
						ds[i] = ad
					} else {
						logger.Fatalf("Find absolute path of %s: %s", d, err)
					}
				}
			}
			for i, a := range os.Args {
				if a == cd || a == "--cache-dir="+cd {
					os.Args[i] = a[:len(a)-len(cd)] + strings.Join(ds, string(os.PathListSeparator))
				}
			}
		}
	}
	_ = expandPathForEmbedded(addr)
	// The default log to syslog is only in daemon mode.
	utils.InitLoggers(!c.Bool("no-syslog"))
	err := makeDaemon(c, vfsConf.Format.Name, vfsConf.Meta.MountPoint, m)
	if err != nil {
		logger.Fatalf("Failed to make daemon: %s", err)
	}
	if runtime.GOOS == "linux" {
		log.SetOutput(os.Stderr)
	}
}

func expandPathForEmbedded(addr string) string {
	embeddedSchemes := []string{"sqlite3://", "badger://"}
	for _, es := range embeddedSchemes {
		if strings.HasPrefix(addr, es) {
			path := addr[len(es):]
			absPath, err := filepath.Abs(path)
			if err == nil && absPath != path {
				for i, a := range os.Args {
					if a == addr {
						expanded := es + absPath
						os.Args[i] = expanded
						return expanded
					}
				}
			}
		}
	}
	return addr
}

func getVfsConf(c *cli.Context, metaConf *meta.Config, format *meta.Format, chunkConf *chunk.Config) *vfs.Config {
	cfg := &vfs.Config{
		Meta:           metaConf,
		Format:         *format,
		Version:        version.Version(),
		Chunk:          chunkConf,
		BackupMeta:     duration(c.String("backup-meta")),
		Port:           &vfs.Port{DebugAgent: debugAgent, PyroscopeAddr: c.String("pyroscope")},
		PrefixInternal: c.Bool("prefix-internal"),
	}
	skip_check := os.Getenv("SKIP_BACKUP_META_CHECK") == "true"
	if !skip_check && cfg.BackupMeta > 0 && cfg.BackupMeta < time.Minute*5 {
		logger.Fatalf("backup-meta should not be less than 5 minutes: %s", cfg.BackupMeta)
	}
	return cfg
}

func registerMetaMsg(m meta.Meta, store chunk.ChunkStore, chunkConf *chunk.Config) {
	m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
		return store.Remove(args[0].(uint64), int(args[1].(uint32)))
	})
	m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
		return vfs.Compact(*chunkConf, store, args[0].([]meta.Slice), args[1].(uint64))
	})
}

func configEqual(a, b *vfs.Config) bool {
	if a == nil || b == nil {
		return a == b
	}

	ac, bc := *a, *b
	ac.Meta, ac.Chunk, ac.Port, ac.Format.SecretKey, ac.AttrTimeout, ac.DirEntryTimeout, ac.EntryTimeout = nil, nil, nil, "", 0, 0, 0
	bc.Meta, bc.Chunk, bc.Port, bc.Format.SecretKey, bc.AttrTimeout, bc.DirEntryTimeout, bc.EntryTimeout = nil, nil, nil, "", 0, 0, 0
	eq := ac == bc

	if a.Meta == nil || b.Meta == nil {
		eq = eq && a.Meta == b.Meta
	} else {
		eq = eq && *a.Meta == *b.Meta
	}

	if a.Chunk == nil || b.Chunk == nil {
		eq = eq && a.Chunk == b.Chunk
	} else {
		eq = eq && *a.Chunk == *b.Chunk
	}

	return eq
}

func readConfig(mp string) ([]byte, error) {
	contents, err := os.ReadFile(filepath.Join(mp, ".jfs.config"))
	if os.IsNotExist(err) {
		contents, err = os.ReadFile(filepath.Join(mp, ".config"))
	}
	return contents, err
}

func prepareMp(newCfg *vfs.Config, mp string) (ignore bool) {
	fi, err := os.Stat(mp)
	if err != nil {
		if strings.Contains(mp, ":") {
			// Windows path, users should inspect mount point by themselves
			return
		}
		if err := os.MkdirAll(mp, 0777); err != nil {
			if os.IsExist(err) {
				// a broken mount point, umount it and continue to mount
				_ = doUmount(mp, true)
				return
			}
			logger.Fatalf("create %s: %s", mp, err)
		}
		return
	}
	if fi.Size() == 0 {
		// a broken mount point, umount it and continue to mount
		_ = doUmount(mp, true)
		return
	}

	ino, _ := utils.GetFileInode(mp)
	if ino != uint64(meta.RootInode) {
		// not a mount point, just mount it
		return
	}

	contents, err := readConfig(mp)
	if err != nil {
		// failed to read juicefs config, continue to mount
		return
	}

	originConfig := vfs.Config{}
	if err = json.Unmarshal(contents, &originConfig); err != nil {
		// not a valid juicefs config, continue to mount
		return
	}

	if !configEqual(newCfg, &originConfig) {
		// not the same juicefs, continue to mount
		return
	}

	logger.Warnf("%s is already mounted by the same juicefs, ignored", mp)
	return true
}

func getMetaConf(c *cli.Context, mp string, readOnly bool) *meta.Config {
	conf := meta.DefaultConf()
	conf.Retries = c.Int("io-retries")
	conf.MaxDeletes = c.Int("max-deletes")
	conf.SkipDirNlink = c.Int("skip-dir-nlink")
	conf.ReadOnly = readOnly
	conf.NoBGJob = c.Bool("no-bgjob")
	conf.OpenCache = time.Duration(c.Float64("open-cache") * 1e9)
	conf.OpenCacheLimit = c.Uint64("open-cache-limit")
	conf.Heartbeat = duration(c.String("heartbeat"))
	conf.MountPoint = mp
	conf.Subdir = c.String("subdir")

	atimeMode := c.String("atime-mode")
	if atimeMode != meta.RelAtime && atimeMode != meta.StrictAtime && atimeMode != meta.NoAtime {
		logger.Warnf("unknown atime-mode \"%s\", changed to %s", atimeMode, meta.NoAtime)
		atimeMode = meta.NoAtime
	}
	conf.AtimeMode = atimeMode
	return conf
}

func getChunkConf(c *cli.Context, format *meta.Format) *chunk.Config {
	cm, err := strconv.ParseUint(c.String("cache-mode"), 8, 32)
	if err != nil {
		logger.Warnf("Invalid cache-mode %s, using default value 0600", c.String("cache-mode"))
		cm = 0600
	}
	chunkConf := &chunk.Config{
		BlockSize:  format.BlockSize * 1024,
		Compress:   format.Compression,
		HashPrefix: format.HashPrefix,

		GetTimeout:    time.Second * time.Duration(c.Int("get-timeout")),
		PutTimeout:    time.Second * time.Duration(c.Int("put-timeout")),
		MaxUpload:     c.Int("max-uploads"),
		MaxRetries:    c.Int("io-retries"),
		Writeback:     c.Bool("writeback"),
		Prefetch:      c.Int("prefetch"),
		BufferSize:    c.Int("buffer-size") << 20,
		UploadLimit:   c.Int64("upload-limit") * 1e6 / 8,
		DownloadLimit: c.Int64("download-limit") * 1e6 / 8,
		UploadDelay:   duration(c.String("upload-delay")),

		CacheDir:          c.String("cache-dir"),
		CacheSize:         int64(c.Int("cache-size")),
		FreeSpace:         float32(c.Float64("free-space-ratio")),
		CacheMode:         os.FileMode(cm),
		CacheFullBlock:    !c.Bool("cache-partial-only"),
		CacheChecksum:     c.String("verify-cache-checksum"),
		CacheEviction:     c.String("cache-eviction"),
		CacheScanInterval: duration(c.String("cache-scan-interval")),
		AutoCreate:        true,
	}
	if chunkConf.UploadLimit == 0 {
		chunkConf.UploadLimit = format.UploadLimit * 1e6 / 8
	}
	if chunkConf.DownloadLimit == 0 {
		chunkConf.DownloadLimit = format.DownloadLimit * 1e6 / 8
	}
	chunkConf.SelfCheck(format.UUID)
	return chunkConf
}

func initBackgroundTasks(c *cli.Context, vfsConf *vfs.Config, metaConf *meta.Config, m meta.Meta, blob object.ObjectStorage, registerer prometheus.Registerer, registry *prometheus.Registry) {
	metricsAddr := exposeMetrics(c, m, registerer, registry)
	vfsConf.Port.PrometheusAgent = metricsAddr
	if c.IsSet("consul") {
		metric.RegisterToConsul(c.String("consul"), metricsAddr, vfsConf.Meta.MountPoint)
		vfsConf.Port.ConsulAddr = c.String("consul")
	}
	if !metaConf.ReadOnly && !metaConf.NoBGJob && vfsConf.BackupMeta > 0 {
		go vfs.Backup(m, blob, vfsConf.BackupMeta)
	}
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, version.Version())
	}
}

type storageHolder struct {
	object.ObjectStorage
	fmt meta.Format
}

func NewReloadableStorage(format *meta.Format, cli meta.Meta, patch func(*meta.Format)) (object.ObjectStorage, error) {
	if patch != nil {
		patch(format)
	}
	blob, err := createStorage(*format)
	if err != nil {
		return nil, err
	}
	holder := &storageHolder{
		ObjectStorage: blob,
		fmt:           *format, // keep a copy to find the change
	}
	cli.OnReload(func(new *meta.Format) {
		if patch != nil {
			patch(new)
		}
		old := &holder.fmt
		if new.Storage != old.Storage || new.Bucket != old.Bucket || new.AccessKey != old.AccessKey || new.SecretKey != old.SecretKey || new.SessionToken != old.SessionToken || new.StorageClass != old.StorageClass {
			logger.Infof("found new configuration: storage=%s bucket=%s ak=%s storageClass=%s", new.Storage, new.Bucket, new.AccessKey, new.StorageClass)

			newBlob, err := createStorage(*new)
			if err != nil {
				logger.Warnf("object storage: %s", err)
				return
			}
			holder.ObjectStorage = newBlob
			holder.fmt = *new
		}
	})
	return holder, nil
}

func tellFstabOptions(c *cli.Context) string {
	opts := []string{"_netdev"}
	for _, s := range os.Args[2:] {
		if !strings.HasPrefix(s, "-") {
			continue
		}
		s = strings.TrimLeft(s, "-")
		s = strings.Split(s, "=")[0]
		if !c.IsSet(s) || s == "update-fstab" || s == "background" || s == "d" {
			continue
		}
		if s == "o" {
			opts = append(opts, c.String(s))
		} else if v := c.Bool(s); v {
			opts = append(opts, s)
		} else {
			opts = append(opts, fmt.Sprintf("%s=%s", s, c.Generic(s)))
		}
	}
	sort.Strings(opts)
	return strings.Join(opts, ",")
}

func tryToInstallMountExec() error {
	if _, err := os.Stat("/sbin/mount.juicefs"); err == nil {
		return nil
	}
	src, err := os.Executable()
	if err != nil {
		return err
	}
	return os.Symlink(src, "/sbin/mount.juicefs")
}

func insideContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	mountinfo, err := os.Open("/proc/1/mountinfo")
	if os.IsNotExist(err) {
		return false
	}
	scanner := bufio.NewScanner(mountinfo)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) > 8 && fields[4] == "/" {
			fstype := fields[8]
			return strings.Contains(fstype, "overlay") || strings.Contains(fstype, "aufs")
		}
	}
	if err = scanner.Err(); err != nil {
		logger.Warnf("scan /proc/1/mountinfo: %s", err)
	}
	return false
}

func updateFstab(c *cli.Context) error {
	addr := expandPathForEmbedded(c.Args().Get(0))
	mp := c.Args().Get(1)
	var fstab = "/etc/fstab"

	f, err := os.Open(fstab)
	if err != nil {
		return err
	}
	defer f.Close()
	entryIndex := -1
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 6 && fields[2] == "juicefs" && fields[0] == addr && fields[1] == mp {
			entryIndex = len(lines)
		}
		lines = append(lines, line)
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	opts := tellFstabOptions(c)
	entry := fmt.Sprintf("%s  %s  juicefs  %s  0 0", addr, mp, opts)
	if entryIndex >= 0 {
		if entry == lines[entryIndex] {
			return nil
		}
		lines[entryIndex] = entry
	} else {
		lines = append(lines, entry)
	}
	tempFstab := fstab + ".tmp"
	tmpf, err := os.OpenFile(tempFstab, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer tmpf.Close()
	if _, err := tmpf.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		_ = os.Remove(tempFstab)
		return err
	}
	return os.Rename(tempFstab, fstab)
}

func mount(c *cli.Context) error {
	setup(c, 2)
	addr := c.Args().Get(0)
	mp := c.Args().Get(1)

	metaConf := getMetaConf(c, mp, c.Bool("read-only") || utils.StringContains(strings.Split(c.String("o"), ","), "ro"))
	metaConf.CaseInsensi = strings.HasSuffix(mp, ":") && runtime.GOOS == "windows"
	metaCli := meta.NewClient(addr, metaConf)
	format, err := metaCli.Load(true)
	if err != nil {
		return err
	}
	if st := metaCli.Chroot(meta.Background, metaConf.Subdir); st != 0 {
		return st
	}

	// Wrap the default registry, all prometheus.MustRegister() calls should be afterwards
	registerer, registry := wrapRegister(mp, format.Name)

	blob, err := NewReloadableStorage(format, metaCli, updateFormat(c))
	if err != nil {
		return fmt.Errorf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	if c.Bool("update-fstab") && !calledViaMount(os.Args) && !insideContainer() {
		if os.Getuid() != 0 {
			logger.Warnf("--update-fstab should be used with root")
		} else {
			var e1, e2 error
			if e1 = tryToInstallMountExec(); e1 != nil {
				logger.Warnf("failed to create /sbin/mount.juicefs: %s", e1)
			}
			if e2 = updateFstab(c); e2 != nil {
				logger.Warnf("failed to update fstab: %s", e2)
			}
			if e1 == nil && e2 == nil {
				logger.Infof("Successfully updated fstab, now you can mount with `mount %s`", mp)
			}
		}
	}

	chunkConf := getChunkConf(c, format)
	store := chunk.NewCachedStore(blob, *chunkConf, registerer)
	registerMetaMsg(metaCli, store, chunkConf)

	vfsConf := getVfsConf(c, metaConf, format, chunkConf)
	ignore := prepareMp(vfsConf, mp)
	if !c.Bool("force") && ignore {
		return nil
	}

	if c.Bool("background") && os.Getenv("JFS_FOREGROUND") == "" {
		daemonRun(c, addr, vfsConf, metaCli)
	} else {
		if c.IsSet("log") {
			logger.Warnf("--log flag is ignored in foreground mode, the log output will be Stderr")
		}
		go checkMountpoint(vfsConf.Format.Name, mp, c.String("log"), false)
	}

	removePassword(addr)
	err = metaCli.NewSession(true)
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}

	metaCli.OnReload(func(fmt *meta.Format) {
		updateFormat(c)(fmt)
		store.UpdateLimit(fmt.UploadLimit, fmt.DownloadLimit)
	})
	installHandler(mp)
	v := vfs.NewVFS(vfsConf, metaCli, store, registerer, registry)
	v.UpdateFormat = updateFormat(c)
	initBackgroundTasks(c, vfsConf, metaConf, metaCli, blob, registerer, registry)
	mount_main(v, c)
	err = metaCli.CloseSession()
	logger.Infof("The juicefs mount process exit successfully, mountpoint: %s", metaConf.MountPoint)
	return err
}
