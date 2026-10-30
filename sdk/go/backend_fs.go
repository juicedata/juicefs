//go:build !libjfs

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package juicefs

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/juicedata/juicefs/cmd"
	"github.com/juicedata/juicefs/pkg/chunk"
	jfs "github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/metric"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/usage"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
)

var logger = utils.GetLogger("juicefs")

type fsBackend struct {
	fs         *jfs.FileSystem
	registry   *prometheus.Registry
	pusher     *pushGateway
	maxDeletes int

	pid  uint32
	uid  uint32
	gids []uint32
}

func newBackend(name, metaURL string, conf *Config) (backend, error) {
	uid, gids, err := identity()
	if err != nil {
		return nil, err
	}
	setLogLevel(conf.Debug)

	metaConf := conf.metaConfig()
	m := meta.NewClient(metaURL, metaConf)
	fail := func(err error) (backend, error) {
		_ = m.Shutdown()
		return nil, err
	}
	format, err := m.Load(true)
	if err != nil {
		return fail(fmt.Errorf("load setting: %w", err))
	}
	if format.KerbConf != "" {
		return fail(errors.New("kerberos is not supported"))
	}
	if name != format.Name {
		logger.Warnf("name %q differs from the volume name %q", name, format.Name)
	}

	registry, registerer := newRegistry()
	m.InitMetrics(registerer)
	vfs.InitMetrics(registerer)
	metric.UpdateMetrics(registerer)

	blob, err := cmd.NewReloadableStorage(format, m, func(f *meta.Format) {
		if conf.Bucket != "" {
			f.Bucket = conf.Bucket
		}
		if conf.StorageClass != "" {
			f.Tiers[0] = object.Tier{ID: 0, Sc: conf.StorageClass}
		}
	})
	if err != nil {
		return fail(fmt.Errorf("object storage: %w", err))
	}
	chunkConf := conf.chunkConfig(format)
	chunkConf.SelfCheck(format.UUID)
	store := chunk.NewCachedStore(blob, *chunkConf, registerer)
	m.OnMsg(meta.DeleteSlice, func(args ...interface{}) error {
		return store.Remove(args[0].(uint64), int(args[1].(uint32)))
	})
	m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
		return vfs.Compact(*chunkConf, store, args[0].([]meta.Slice), args[1].(uint64), args[2].(uint8))
	})
	if err = m.NewSession(!conf.NoSession); err != nil {
		_ = m.CloseSession()
		return fail(fmt.Errorf("new session: %w", err))
	}
	m.OnReload(func(f *meta.Format) error {
		upload, download := f.UploadLimit, f.DownloadLimit
		if conf.UploadLimit > 0 {
			upload = conf.UploadLimit
		}
		if conf.DownloadLimit > 0 {
			download = conf.DownloadLimit
		}
		store.UpdateLimit(upload, download)
		return nil
	})

	vfsConf := conf.vfsConfig(format, metaConf, chunkConf)
	fsys, err := jfs.NewFileSystem(vfsConf, m, store, registry)
	if err != nil {
		_ = m.CloseSession()
		return fail(fmt.Errorf("initialize: %w", err))
	}
	fsys.InitMetrics(registerer)

	// Start background tasks only after nothing can fail.
	if !conf.ReadOnly && !conf.NoSession && !conf.NoBGJob && vfsConf.BackupMeta > 0 {
		go vfs.Backup(m, blob, vfsConf.BackupMeta, vfsConf.BackupSkipTrash)
	}
	if !conf.NoUsageReport && !conf.NoSession {
		go usage.ReportUsage(m, "go-sdk "+version.Version())
	}
	b := &fsBackend{
		fs:         fsys,
		registry:   registry,
		maxDeletes: conf.MaxDeletes,
		pid:        uint32(os.Getpid()),
		uid:        uid,
		gids:       gids,
	}
	if conf.PushGateway != "" {
		b.pusher = startPushGateway(conf.PushGateway, conf.PushAuth, conf.PushInterval, registry, pushLabels(name, conf.PushLabels))
	}
	return b, nil
}

// identity returns the euid and the groups with the egid first.
func identity() (uint32, []uint32, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return 0, nil, fmt.Errorf("get groups: %w", err)
	}
	gids := []uint32{uint32(os.Getegid())}
	for _, g := range groups {
		if !slices.Contains(gids, uint32(g)) {
			gids = append(gids, uint32(g))
		}
	}
	return uint32(os.Geteuid()), gids, nil
}

func setLogLevel(debug bool) {
	if debug || os.Getenv("JUICEFS_DEBUG") != "" {
		utils.SetLogLevel(logrus.DebugLevel)
		return
	}
	if s := os.Getenv("JUICEFS_LOGLEVEL"); s != "" {
		level, err := logrus.ParseLevel(s)
		if err == nil {
			utils.SetLogLevel(level)
			return
		}
		utils.SetLogLevel(logrus.WarnLevel)
		logger.Errorf("JUICEFS_LOGLEVEL: %s", err)
		return
	}
	utils.SetLogLevel(logrus.WarnLevel)
}

// ctx creates a context per call, as withPid in libjfs.
func (b *fsBackend) ctx() meta.Context {
	return meta.NewContext(b.pid, b.uid, b.gids)
}

func (b *fsBackend) gatherer() prometheus.Gatherer {
	return b.registry
}

func toStat(inode meta.Ino, attr *meta.Attr) *Stat {
	return &Stat{
		Inode: uint64(inode),
		Mode:  attr.SMode(),
		Nlink: attr.Nlink,
		Uid:   attr.Uid,
		Gid:   attr.Gid,
		Size:  attr.Length,
		Rdev:  attr.Rdev,
		Atime: time.Unix(attr.Atime, int64(attr.Atimensec)),
		Mtime: time.Unix(attr.Mtime, int64(attr.Mtimensec)),
		Ctime: time.Unix(attr.Ctime, int64(attr.Ctimensec)),
	}
}

func errno(e syscall.Errno) error {
	if e == 0 {
		return nil
	}
	return e
}

func (b *fsBackend) stat(p string, follow bool) (*Stat, error) {
	var fi *jfs.FileStat
	var e syscall.Errno
	if follow {
		fi, e = b.fs.Stat(b.ctx(), p)
	} else {
		fi, e = b.fs.Lstat(b.ctx(), p)
	}
	if e != 0 {
		return nil, e
	}
	return toStat(fi.Inode(), fi.Sys().(*meta.Attr)), nil
}

func (b *fsBackend) readdir(p string) ([]*fileInfo, error) {
	ctx := b.ctx()
	f, e := b.fs.Open(ctx, p, 0)
	if e != 0 {
		return nil, e
	}
	defer f.Close(ctx)
	entries, e := f.ReaddirPlus(ctx, 0)
	if e != 0 {
		return nil, e
	}
	infos := make([]*fileInfo, len(entries))
	for i, en := range entries {
		infos[i] = newFileInfo(string(en.Name), toStat(en.Inode, en.Attr))
	}
	return infos, nil
}

func (b *fsBackend) mkdir(p string, mode, umask uint16) error {
	return errno(b.fs.Mkdir(b.ctx(), p, mode, umask))
}

func (b *fsBackend) mkdirAll(p string, mode, umask uint16) error {
	return errno(b.fs.MkdirAll0(b.ctx(), p, mode, umask, false))
}

func (b *fsBackend) remove(p string) error {
	return errno(b.fs.Delete(b.ctx(), p))
}

func (b *fsBackend) rmdir(p string) error {
	return errno(b.fs.Rmdir(b.ctx(), p))
}

func (b *fsBackend) removeAll(p string) error {
	return errno(b.fs.Rmr(b.ctx(), p, false, b.maxDeletes))
}

func (b *fsBackend) rename(oldpath, newpath string) error {
	return errno(b.fs.Rename(b.ctx(), oldpath, newpath, 0))
}

func (b *fsBackend) link(oldpath, newpath string) error {
	return errno(b.fs.Link(b.ctx(), oldpath, newpath))
}

func (b *fsBackend) symlink(target, p string) error {
	return errno(b.fs.Symlink(b.ctx(), target, p))
}

func (b *fsBackend) readlink(p string) (string, error) {
	target, e := b.fs.Readlink(b.ctx(), p)
	if e != 0 {
		return "", e
	}
	return string(target), nil
}

func (b *fsBackend) truncate(p string, size uint64) error {
	return errno(b.fs.Truncate(b.ctx(), p, size))
}

// open uses flags 0, which does not register p as opened, as libjfs does.
func (b *fsBackend) open(p string, follow bool, fn func(meta.Context, *jfs.File) syscall.Errno) error {
	ctx := b.ctx()
	var f *jfs.File
	var e syscall.Errno
	if follow {
		f, e = b.fs.Open(ctx, p, 0)
	} else {
		f, e = b.fs.Lopen(ctx, p, 0)
	}
	if e != 0 {
		return e
	}
	defer f.Close(ctx)
	return errno(fn(ctx, f))
}

func (b *fsBackend) chmod(p string, mode uint16) error {
	return b.open(p, true, func(ctx meta.Context, f *jfs.File) syscall.Errno {
		return f.Chmod(ctx, mode)
	})
}

func (b *fsBackend) chown(p string, uid, gid uint32, follow bool) error {
	return b.open(p, follow, func(ctx meta.Context, f *jfs.File) syscall.Errno {
		return f.Chown(ctx, uid, gid)
	})
}

func (b *fsBackend) utime(p string, atime, mtime time.Time) error {
	// Utime2 leaves a time unchanged if both its parts are negative.
	split := func(t time.Time) (int64, int64) {
		if t.IsZero() {
			return -1, -1
		}
		return t.Unix(), int64(t.Nanosecond())
	}
	asec, ansec := split(atime)
	msec, mnsec := split(mtime)
	return b.open(p, true, func(ctx meta.Context, f *jfs.File) syscall.Errno {
		return f.Utime2(ctx, asec, ansec, msec, mnsec)
	})
}

func (b *fsBackend) close() error {
	err := b.fs.Close()
	if b.pusher != nil {
		b.pusher.close()
	}
	return err
}

func (c *Config) metaConfig() *meta.Config {
	conf := meta.DefaultConf()
	conf.Retries = c.IORetries
	conf.MaxDeletes = c.MaxDeletes
	conf.SkipDirNlink = c.SkipDirNlink
	conf.SkipDirMtime = c.SkipDirMtime
	conf.ReadOnly = c.ReadOnly
	conf.NoBGJob = c.NoBGJob || c.NoSession
	conf.OpenCache = c.OpenCache
	conf.Heartbeat = c.Heartbeat
	return conf
}

func (c *Config) chunkConfig(format *meta.Format) *chunk.Config {
	conf := &chunk.Config{
		BlockSize:         format.BlockSize * 1024,
		Compress:          format.Compression,
		CacheDir:          c.CacheDir,
		CacheMode:         0644,
		CacheSize:         c.CacheSize,
		FreeSpace:         float32(c.FreeSpaceRatio),
		AutoCreate:        true,
		CacheFullBlock:    !c.CachePartialOnly,
		CacheChecksum:     c.VerifyCacheChecksum,
		CacheEviction:     c.CacheEviction,
		CacheScanInterval: c.CacheScanInterval,
		CacheExpire:       c.CacheExpire,
		OSCache:           true,
		MaxUpload:         c.MaxUploads,
		// The value SelfCheck sets for libjfs's 0, without its warning.
		MaxDownload:   200,
		MaxRetries:    c.IORetries,
		UploadLimit:   c.UploadLimit * 1e6 / 8,
		DownloadLimit: c.DownloadLimit * 1e6 / 8,
		Prefetch:      c.Prefetch,
		Writeback:     c.Writeback,
		HashPrefix:    format.HashPrefix,
		GetTimeout:    c.GetTimeout,
		PutTimeout:    c.PutTimeout,
		BufferSize:    c.BufferSize,
		Readahead:     int(c.MaxReadahead),
	}
	if conf.UploadLimit == 0 {
		conf.UploadLimit = format.UploadLimit * 1e6 / 8
	}
	if conf.DownloadLimit == 0 {
		conf.DownloadLimit = format.DownloadLimit * 1e6 / 8
	}
	return conf
}

func (c *Config) vfsConfig(format *meta.Format, metaConf *meta.Config, chunkConf *chunk.Config) *vfs.Config {
	return &vfs.Config{
		Meta:            metaConf,
		Format:          *format,
		Chunk:           chunkConf,
		AttrTimeout:     c.AttrCache,
		EntryTimeout:    c.EntryCache,
		DirEntryTimeout: c.DirEntryCache,
		AccessLog:       c.AccessLog,
		FastResolve:     c.FastResolve,
		BackupMeta:      c.BackupMeta,
		BackupSkipTrash: c.BackupSkipTrash,
	}
}
