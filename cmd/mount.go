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
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
		Flags: expandFlags(mountFlags(), clientFlags(1.0), shareInfoFlags()),
	}
}

func exposeMetrics(c *cli.Context, registerer prometheus.Registerer, registry *prometheus.Registry) string {
	var ip, port string
	// default set
	ip, port, err := net.SplitHostPort(c.String("metrics"))
	if err != nil {
		logger.Fatalf("metrics format error: %v", err)
	}
	go metric.UpdateMetrics(registerer)
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

func wrapRegister(c *cli.Context, mp, name string) (prometheus.Registerer, *prometheus.Registry) {
	commonLabels := prometheus.Labels{"mp": mp, "vol_name": name, "juicefs_version": version.Version()}
	if h, err := os.Hostname(); err == nil {
		commonLabels["instance"] = h
	} else {
		logger.Warnf("cannot get hostname: %s", err)
	}
	if c.IsSet("custom-labels") {
		for _, kv := range strings.Split(c.String("custom-labels"), ";") {
			splited := strings.Split(kv, ":")
			if len(splited) != 2 {
				logger.Fatalf("invalid label format: %s", kv)
			}
			if utils.StringContains([]string{"mp", "vol_name", "instance"}, splited[0]) {
				logger.Warnf("overriding reserved label: %s", splited[0])
			}
			commonLabels[splited[0]] = splited[1]
		}
	}
	registry := prometheus.NewRegistry() // replace default so only JuiceFS metrics are exposed
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_",
		prometheus.WrapRegistererWith(commonLabels, registry))

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
			format.UploadLimit = utils.ParseMbps(c, "upload-limit")
		}
		if c.IsSet("download-limit") {
			format.DownloadLimit = utils.ParseMbps(c, "download-limit")
		}
	}
}

func relPathToAbs(ss []string) []string {
	for i, d := range ss {
		if strings.HasPrefix(d, "/") {
			continue
		} else if strings.HasPrefix(d, "~/") {
			if h, err := os.UserHomeDir(); err == nil {
				ss[i] = filepath.Join(h, d[1:])
			} else {
				logger.Fatalf("Expand user home dir of %s: %s", d, err)
			}
		} else {
			if ad, err := filepath.Abs(d); err == nil {
				ss[i] = ad
			} else {
				logger.Fatalf("Find absolute path of %s: %s", d, err)
			}
		}
	}
	return ss
}

func cacheDirPathToAbs(c *cli.Context) {
	if runtime.GOOS != "windows" {
		if cd := c.String("cache-dir"); cd != "memory" {
			ds := utils.SplitDir(cd)
			ds = relPathToAbs(ds)
			for i, a := range os.Args {
				if a == cd || a == "--cache-dir="+cd {
					os.Args[i] = a[:len(a)-len(cd)] + strings.Join(ds, string(os.PathListSeparator))
				}
			}
		}
	}

	if rpAcLog := c.String("access-log"); rpAcLog != "" {
		ap, err := filepath.Abs(rpAcLog)
		if err == nil && ap != rpAcLog {
			for i, a := range os.Args {
				if a == rpAcLog || a == "--access-log="+rpAcLog {
					os.Args[i] = a[:len(a)-len(rpAcLog)] + ap
					break
				}
			}
		}
	}
}

func daemonRun(c *cli.Context, addr string, vfsConf *vfs.Config) {
	cacheDirPathToAbs(c)
	_ = expandPathForEmbedded(addr)
	// The default log to syslog is only in daemon mode.
	utils.InitLoggers(!c.Bool("no-syslog"))
	err := makeDaemon(c, vfsConf)
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
		Meta:   metaConf,
		Format: *format,
		Security: &vfs.SecurityConfig{
			EnableCap:     c.Bool("enable-cap"),
			EnableSELinux: c.Bool("enable-selinux"),
		},
		Version:         version.Version(),
		Chunk:           chunkConf,
		BackupMeta:      utils.Duration(c.String("backup-meta")),
		BackupSkipTrash: c.Bool("backup-skip-trash"),
		Port:            &vfs.Port{DebugAgent: debugAgent, PyroscopeAddr: c.String("pyroscope")},
		PrefixInternal:  c.Bool("prefix-internal"),
		Pid:             os.Getpid(),
		PPid:            os.Getppid(),
		UMask:           0xFFFF,
		HideInternal:    c.Bool("hide-internal"),
	}

	if c.IsSet("umask") {
		umask, err := strconv.ParseUint(c.String("umask"), 8, 16)
		if err != nil {
			logger.Fatalf("invalid umask %s: %s", c.String("umask"), err)
		}
		cfg.UMask = uint16(umask)
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

func readConfig(mp string) ([]byte, error) {
	contents, err := os.ReadFile(filepath.Join(mp, ".jfs.config"))
	if os.IsNotExist(err) {
		contents, err = os.ReadFile(filepath.Join(mp, ".config"))
	}
	return contents, err
}

func getMetaConf(c *cli.Context, mp string, readOnly bool) *meta.Config {
	conf := meta.DefaultConf()
	conf.Retries = c.Int("io-retries")
	conf.MaxDeletes = c.Int("max-deletes")
	conf.SkipDirNlink = c.Int("skip-dir-nlink")
	conf.ReadOnly = readOnly
	conf.NoBGJob = c.Bool("no-bgjob")
	conf.OpenCache = utils.Duration(c.String("open-cache"))
	conf.OpenCacheLimit = c.Uint64("open-cache-limit")
	conf.Heartbeat = utils.Duration(c.String("heartbeat"))
	conf.MountPoint = mp
	conf.Subdir = c.String("subdir")
	conf.SkipDirMtime = utils.Duration(c.String("skip-dir-mtime"))
	conf.Sid, _ = strconv.ParseUint(os.Getenv("_JFS_META_SID"), 10, 64)
	conf.SortDir = c.Bool("sort-dir")
	conf.FastStatfs = c.Bool("fast-statfs")

	atimeMode := c.String("atime-mode")
	if atimeMode != meta.RelAtime && atimeMode != meta.StrictAtime && atimeMode != meta.NoAtime {
		logger.Warnf("unknown atime-mode \"%s\", changed to %s", atimeMode, meta.NoAtime)
		atimeMode = meta.NoAtime
	}
	conf.AtimeMode = atimeMode

	// Parse network interfaces
	if ifaces := c.String("network-interfaces"); ifaces != "" {
		conf.NetworkInterfaces = strings.Split(ifaces, ",")
		// Trim whitespace from each interface name
		for i := range conf.NetworkInterfaces {
			conf.NetworkInterfaces[i] = strings.TrimSpace(conf.NetworkInterfaces[i])
		}
	}

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

		GetTimeout:    utils.Duration(c.String("get-timeout")),
		PutTimeout:    utils.Duration(c.String("put-timeout")),
		MaxUpload:     c.Int("max-uploads"),
		MaxStageWrite: c.Int("max-stage-write"),
		MaxRetries:    c.Int("io-retries"),
		Writeback:     c.Bool("writeback"),
		Prefetch:      c.Int("prefetch"),
		BufferSize:    utils.ParseBytes(c, "buffer-size", 'M'),
		UploadLimit:   utils.ParseMbps(c, "upload-limit") * 1e6 / 8,
		DownloadLimit: utils.ParseMbps(c, "download-limit") * 1e6 / 8,
		UploadDelay:   utils.Duration(c.String("upload-delay")),
		UploadHours:   c.String("upload-hours"),

		CacheDir:          c.String("cache-dir"),
		CacheSize:         utils.ParseBytes(c, "cache-size", 'M'),
		CacheItems:        c.Int64("cache-items"),
		FreeSpace:         float32(c.Float64("free-space-ratio")),
		CacheMode:         os.FileMode(cm),
		CacheFullBlock:    !c.Bool("cache-partial-only"),
		CacheLargeWrite:   c.Bool("cache-large-write"),
		CacheChecksum:     c.String("verify-cache-checksum"),
		CacheEviction:     c.String("cache-eviction"),
		CacheScanInterval: utils.Duration(c.String("cache-scan-interval")),
		CacheExpire:       utils.Duration(c.String("cache-expire")),
		OSCache:           os.Getenv("JFS_DROP_OSCACHE") == "",
		AutoCreate:        true,
	}
	if c.IsSet("max-readahead") {
		chunkConf.Readahead = int(utils.ParseBytes(c, "max-readahead", 'M'))
	} else {
		chunkConf.Readahead = 8 * chunkConf.BlockSize
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
	metricsAddr := exposeMetrics(c, registerer, registry)
	m.InitMetrics(registerer)
	vfs.InitMetrics(registerer)
	vfsConf.Port.PrometheusAgent = metricsAddr
	if c.IsSet("consul") {
		metadata := make(map[string]string)
		metadata["mountPoint"] = vfsConf.Meta.MountPoint
		metric.RegisterToConsul(c.String("consul"), metricsAddr, metadata)
		vfsConf.Port.ConsulAddr = c.String("consul")
	}
	if !metaConf.ReadOnly && !metaConf.NoBGJob && vfsConf.BackupMeta > 0 {
		registerer.MustRegister(vfs.LastBackupTimeG)
		registerer.MustRegister(vfs.LastBackupDurationG)
		go vfs.Backup(m, blob, vfsConf.BackupMeta, vfsConf.BackupSkipTrash)
	} else {
		logger.Warnf("Metadata backup is disabled")
	}
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, version.Version())
	}
}

type storageHolder struct {
	object.ObjectStorage
	fmt meta.Format
}

func (h *storageHolder) Shutdown() {
	object.Shutdown(h.ObjectStorage)
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

func insideContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	mountinfo, err := os.Open("/proc/1/mountinfo")
	if err != nil {
		if os.IsNotExist(err) {
			return false
		} else {
			logger.Warnf("Open /proc/1/mountinfo: %s", err)
			return false
		}
	}
	defer mountinfo.Close()
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

func getDefaultLogDir() string {
	var defaultLogDir = "/var/log"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Warn(err)
			homeDir = defaultLogDir
		}
		defaultLogDir = path.Join(homeDir, ".juicefs")
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
		}
		defaultLogDir = path.Join(homeDir, ".juicefs")
	}
	return defaultLogDir
}

func mount(c *cli.Context) error {
	setup(c, 2)
	addr := c.Args().Get(0)
	removePassword(addr)
	mp := c.Args().Get(1)

	stage := getDaemonStage()
	if stage < 0 || stage > 2 {
		logger.Fatalf("Invalid daemon stage: %d", stage)
	}
	supervisor := os.Getenv("JFS_SUPERVISOR")
	if supervisor != "" || runtime.GOOS == "windows" {
		stage = 3
	}

	var err error
	if stage == 0 || supervisor == "test" {
		err = utils.WithTimeout(context.TODO(), func(context.Context) error {
			mp, err = filepath.Abs(mp)
			return err
		}, time.Second*3)
		if err != nil {
			logger.Fatalf("abs %s: %s", mp, err)
		}
		if mp == "/" {
			logger.Fatalf("should not mount on the root directory")
		}
		prepareMp(mp)
		if runtime.GOOS == "linux" && c.Bool("update-fstab") && !calledViaMount(os.Args) && !insideContainer() {
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
	}

	var format = &meta.Format{}
	var metaCli meta.Meta
	var blob object.ObjectStorage
	metaConf := getMetaConf(c, mp, c.Bool("read-only") || utils.StringContains(strings.Split(c.String("o"), ","), "ro"))
	if runtime.GOOS == "windows" {
		metaConf.CaseInsensi = !c.Bool("case-sensitive")
	}
	// stage 0: check the connection to fail fast
	// stage 2: need the volume name to check if it's already mounted
	// stage 3: the real service process
	if stage != 1 {
		metaCli = meta.NewClient(addr, metaConf)
		format, err = metaCli.Load(true)
		if err != nil {
			return err
		}
	}

	chunkConf := getChunkConf(c, format)
	vfsConf := getVfsConf(c, metaConf, format, chunkConf)
	setFuseOption(c, format, vfsConf)
	if stage == 0 || stage == 3 {
		blob, err = NewReloadableStorage(format, metaCli, updateFormat(c))
		if err != nil {
			return fmt.Errorf("object storage: %s", err)
		}
		logger.Infof("Data use %s", blob)

	}

	if stage < 3 {
		// supervisor serves no user request
		if metaCli != nil {
			if err = metaCli.Shutdown(); err != nil {
				logger.Errorf("[pid=%d] meta shutdown: %s", os.Getpid(), err)
			}
		}
		if blob != nil {
			// test storage at startup to fail fast instead of throwing EIO in the middle of user's workload
			if c.Bool("check-storage") {
				start := time.Now()
				if err = test(blob); err != nil {
					logger.Errorf("Object storage test failed: %s", err)
					return err
				} else {
					logger.Infof("Object storage test passed in %s", time.Since(start))
				}
			}
			object.Shutdown(blob)
		}
		var foreground bool
		if runtime.GOOS == "windows" || !c.Bool("background") || os.Getenv("JFS_FOREGROUND") != "" {
			foreground = true
		} else if c.Bool("background") || os.Getenv("__DAEMON_STAGE") != "" {
			foreground = false
		} else {
			foreground = os.Getppid() == 1 && !insideContainer()
		}
		if foreground {
			go checkMountpoint(format.Name, mp, c.String("log"), false)
		} else {
			daemonRun(c, addr, vfsConf) // only stage 0 needs the vfsConf
		}
		os.Setenv("JFS_SUPERVISOR", strconv.Itoa(os.Getppid()))
		return launchMount(c, mp, vfsConf)
	} else if runtime.GOOS == "windows" && c.Bool("background") {
		daemonRun(c, addr, vfsConf)
		return nil
	}
	logger.Infof("JuiceFS version %s", version.Version())

	if commPath := os.Getenv("_FUSE_FD_COMM"); commPath != "" {
		vfsConf.CommPath = commPath
		vfsConf.StatePath = fmt.Sprintf("/tmp/state%d.json", os.Getppid())
	}

	if st := metaCli.Chroot(meta.Background(), metaConf.Subdir); st != 0 {
		return st
	}
	// Wrap the default registry, all prometheus.MustRegister() calls should be afterwards
	registerer, registry := wrapRegister(c, mp, format.Name)

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
	v := vfs.NewVFS(vfsConf, metaCli, store, registerer, registry)
	installHandler(metaCli, mp, v, blob)
	v.UpdateFormat = updateFormat(c)
	initBackgroundTasks(c, vfsConf, metaConf, metaCli, blob, registerer, registry)
	mountMain(v, c)
	if err := v.FlushAll(""); err != nil {
		logger.Errorf("flush all delayed data: %s", err)
	}
	err = metaCli.CloseSession()
	object.Shutdown(blob)
	logger.Infof("The juicefs mount process exit successfully, mountpoint: %s", metaConf.MountPoint)
	return err
}
