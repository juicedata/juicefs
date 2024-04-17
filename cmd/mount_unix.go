//go:build !windows
// +build !windows

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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/godaemon"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func showThreadStack(agentAddr string) {
	if agentAddr == "" {
		return
	}
	resp, err := http.Get(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=2", agentAddr))
	if err != nil {
		logger.Warnf("list goroutine from %s: %s", agentAddr, err)
	} else {
		grs, _ := io.ReadAll(resp.Body)
		logger.Infof("list goroutines from %s:\n%s", agentAddr, string(grs))
	}
}

// devMinor returns the minor component of a Linux device number.
func devMinor(dev uint64) uint32 {
	minor := dev & 0xff
	minor |= (dev >> 12) & 0xffffff00
	return uint32(minor)
}

func killMountProcess(pid int, dev uint64, lastActive *int64) {
	if pid > 0 {
		logger.Infof("watchdog: kill %d", pid)
		err := syscall.Kill(pid, syscall.SIGABRT)
		if err != nil {
			logger.Warnf("kill %d: %s", pid, err)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		// double check
		time.Sleep(time.Second * 10)
		if atomic.LoadInt64(lastActive)+30 > time.Now().Unix() {
			return
		}
	}
	if runtime.GOOS == "linux" && dev > 0 {
		tids, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		for _, tid := range tids {
			stack, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/stack", pid, tid))
			if err == nil && bytes.Contains(stack, []byte("fuse_simple_request")) {
				logger.Errorf("find deadlock in mount process, abort it: %s", string(stack))
				if fuseFd > 0 {
					_ = syscall.Close(fuseFd)
					fuseFd = 0
				}
				f, err := os.OpenFile(fmt.Sprintf("/sys/fs/fuse/connections/%d/abort", devMinor(dev)), os.O_WRONLY, 0777)
				if err != nil {
					logger.Warn(err)
				} else {
					_, _ = f.WriteString("1")
					_ = f.Close()
				}
				break
			}
		}
	}
}

func loadConfig(path string) (string, *vfs.Config, error) {
	for d := path; d != "/"; d = filepath.Dir(d) {
		data, err := readConfig(d)
		if err == nil {
			var conf vfs.Config
			err = json.Unmarshal(data, &conf)
			return d, &conf, err
		}
		if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("read %s: %w", d, err)
		}
	}
	return "", nil, fmt.Errorf("%s is not inside JuiceFS", path)
}

func watchdog(ctx context.Context, mp string) {
	var lastActive int64
	var pid int
	var agentAddr string
	var dev uint64
	go func() {
		time.Sleep(time.Millisecond * 100) // wait for child process
		atomic.StoreInt64(&lastActive, time.Now().Unix())
		for ctx.Err() == nil {
			var confName = ".config"
			if !vfs.IsSpecialName(confName) {
				confName = ".jfs" + confName
			}
			var confStat syscall.Stat_t
			err := syscall.Stat(filepath.Join(mp, confName), &confStat)
			ino, _ := vfs.GetInternalNodeByName(confName)
			if err == nil && confStat.Ino == uint64(ino) {
				if dev == 0 && runtime.GOOS == "linux" {
					var st syscall.Stat_t
					if err := syscall.Stat(mp, &st); err == nil && st.Ino == 1 {
						dev = uint64(st.Dev)
					}
				}
				if pid == 0 {
					_, conf, err := loadConfig(mp)
					if err == nil {
						logger.Infof("watching %s, pid %d", mp, conf.Pid)
						pid = conf.Pid
						agentAddr = conf.DebugAgent
					} else {
						logger.Warnf("load config: %s", err)
						continue
					}
				}
			}
			atomic.StoreInt64(&lastActive, time.Now().Unix())
			time.Sleep(time.Second * 5)
		}
	}()
	for ctx.Err() == nil {
		now := time.Now().Unix()
		if atomic.LoadInt64(&lastActive)+30 < now {
			showThreadStack(agentAddr)
			time.Sleep(time.Second * 30)
			// double check
			if atomic.LoadInt64(&lastActive)+60 < time.Now().Unix() && ctx.Err() == nil {
				logger.Infof("mount point %s is not active for %s", mp, time.Since(time.Unix(atomic.LoadInt64(&lastActive), 0)))
				showThreadStack(agentAddr)
				killMountProcess(pid, dev, &lastActive)
				atomic.StoreInt64(&lastActive, time.Now().Unix())
				pid = 0
				dev = 0
			}
		}
		time.Sleep(time.Second * 10)
	}
}

func checkMountpoint(name, mp, logPath string, background bool) {
	mountTimeOut := 10 // default 10 seconds
	interval := 500    // check every 500 Millisecond
	if tStr, ok := os.LookupEnv("JFS_MOUNT_TIMEOUT"); ok {
		if t, err := strconv.ParseInt(tStr, 10, 64); err == nil {
			mountTimeOut = int(t)
		} else {
			logger.Errorf("invalid env JFS_MOUNT_TIMEOUT: %s %s", tStr, err)
		}
	}
	for i := 0; i < mountTimeOut*1000/interval; i++ {
		time.Sleep(time.Duration(interval) * time.Millisecond)
		st, err := os.Stat(mp)
		if err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == uint64(meta.RootInode) {
				logger.Infof("\033[92mOK\033[0m, %s is ready at %s", name, mp)
				return
			}
		}
		_, _ = os.Stdout.WriteString(".")
		_ = os.Stdout.Sync()
	}
	_, _ = os.Stdout.WriteString("\n")
	if background {
		logger.Fatalf("The mount point is not ready in %d seconds, please check the log (%s) or re-mount in foreground", mountTimeOut, logPath)
	} else {
		logger.Fatalf("The mount point is not ready in %d seconds, exit it", mountTimeOut)
	}
}

func makeDaemonForSvc(c *cli.Context, m meta.Meta, metaUrl string) error {
	cacheDirPathToAbs(c)
	_ = expandPathForEmbedded(metaUrl)

	var attrs godaemon.DaemonAttr
	logfile := c.String("log")
	attrs.OnExit = func(stage int) error {
		return nil
	}

	// the current dir will be changed to root in daemon,
	// so the mount point has to be an absolute path.
	if godaemon.Stage() == 0 {
		var err error
		attrs.Stdout, err = os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		logger.Infof("open log file %s: %s", logfile, err)
		if err != nil {
			logger.Errorf("open log file %s: %s", logfile, err)
		}
	}
	if godaemon.Stage() <= 1 {
		err := m.Shutdown()
		if err != nil {
			logger.Errorf("shutdown: %s", err)
		}
	}
	_, _, err := godaemon.MakeDaemon(&attrs)
	return err
}

func fuseFlags() []cli.Flag {
	return addCategories("FUSE", []cli.Flag{
		&cli.BoolFlag{
			Name:  "enable-xattr",
			Usage: "enable extended attributes (xattr)",
		},
		&cli.BoolFlag{
			Name:  "enable-ioctl",
			Usage: "enable ioctl (support GETFLAGS/SETFLAGS only)",
		},
		&cli.StringFlag{
			Name:  "root-squash",
			Usage: "mapping local root user (uid = 0) to another one specified as <uid>:<gid>",
		},
		&cli.BoolFlag{
			Name:  "prefix-internal",
			Usage: "add '.jfs' prefix to all internal files",
		},
		&cli.BoolFlag{
			Name:   "non-default-permission",
			Usage:  "disable `default_permissions` option, only for testing",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "o",
			Usage: "other FUSE options",
		},
	})
}

func mountFlags() []cli.Flag {
	selfFlags := []cli.Flag{
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
			Name:  "log",
			Value: path.Join(getDefaultLogDir(), "juicefs.log"),
			Usage: "path of log file when running in background",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "force to mount even if the mount point is already mounted by the same filesystem",
		},
	}
	if runtime.GOOS == "linux" {
		selfFlags = append(selfFlags, &cli.BoolFlag{
			Name:  "update-fstab",
			Usage: "add / update entry in /etc/fstab, will create a symlink from /sbin/mount.juicefs to JuiceFS executable if not existing",
		})
	}
	return append(selfFlags, fuseFlags()...)
}

func disableUpdatedb() {
	path := "/etc/updatedb.conf"
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	// obtain exclusive and not block flock
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EAGAIN {
			return
		}
	} else {
		defer func() {
			// release flock
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		}()
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return
	}
	fstype := "fuse.juicefs"
	if bytes.Contains(data, []byte(fstype)) {
		return
	}
	// assume that fuse.sshfs is already in PRUNEFS
	knownFS := "fuse.sshfs"
	p1 := bytes.Index(data, []byte("PRUNEFS"))
	p2 := bytes.Index(data, []byte(knownFS))
	if p1 > 0 && p2 > p1 {
		var nd []byte
		nd = append(nd, data[:p2]...)
		nd = append(nd, fstype...)
		nd = append(nd, ' ')
		nd = append(nd, data[p2:]...)
		err = os.WriteFile(path, nd, 0644)
		if err != nil {
			logger.Warnf("update %s: %s", path, err)
		} else {
			logger.Infof("Add %s into PRUNEFS of %s", fstype, path)
		}
	}
}

func getFuserMountVersion() string {
	var version = "0.0.0"
	out, _ := exec.Command("fusermount", "-V").CombinedOutput()
	ps := strings.Split(string(out), ":")
	if len(ps) > 1 {
		return strings.TrimSpace(ps[1])
	}
	return version
}

func setFuseOption(c *cli.Context, format *meta.Format, vfsConf *vfs.Config) {
	rawOpts, mt, noxattr, noacl := genFuseOptExt(c, format.Name)
	options := vfs.FuseOptions(fuse.GenFuseOpt(vfsConf, rawOpts, mt, noxattr, noacl))
	vfsConf.FuseOpts = &options
}

func genFuseOpt(c *cli.Context, name string) string {
	fuseOpt := c.String("o")
	// todo: remove ?
	prefix := os.Getenv("FSTAB_NAME_PREFIX")
	if prefix == "" {
		prefix = "JuiceFS:"
	}
	fuseOpt += ",fsname=" + prefix + name
	if c.Bool("allow-other") || os.Getuid() == 0 && !strings.Contains(fuseOpt, "allow_other") {
		fuseOpt += ",allow_other"
	}
	switch runtime.GOOS {
	case "darwin":
		fuseOpt += ",allow_recursion"
	case "linux":
		// nonempty has been removed since 3.0.0
		if getFuserMountVersion() < "3.0.0" {
			fuseOpt += ",nonempty"
		}
	}
	fuseOpt = strings.TrimLeft(fuseOpt, ",")
	return fuseOpt
}

func prepareMp(mp string) {
	var fi os.FileInfo
	err := utils.WithTimeout(func() error {
		var err error
		fi, err = os.Stat(mp)
		return err
	}, time.Second*3)
	if !strings.Contains(mp, ":") && err != nil {
		err2 := utils.WithTimeout(func() error {
			return os.MkdirAll(mp, 0777)
		}, time.Second*3)
		if err2 != nil {
			if os.IsExist(err2) || strings.Contains(err2.Error(), "timeout after 3s") {
				// a broken mount point, umount it
				logger.Infof("mountpoint %s is broken: %s, umount it", mp, err)
				_ = doUmount(mp, true)
			} else {
				logger.Fatalf("create %s: %s", mp, err2)
			}
		}
	} else if err == nil {
		ino, _ := utils.GetFileInode(mp)
		if ino <= uint64(meta.RootInode) && fi.Size() == 0 {
			// a broken mount point, umount it
			logger.Infof("mountpoint %s is broken (ino=%d, size=%d), umount it", mp, ino, fi.Size())
			_ = doUmount(mp, true)
		}
	}

	if os.Getuid() == 0 {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		if fi, err := os.Stat(mp); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				if st.Uid != uint32(os.Getuid()) {
					logger.Fatalf("current user should own %s", mp)
				}
			}
		}
	case "linux":
		f, err := os.CreateTemp(mp, ".test")
		if err != nil && (os.IsPermission(err) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EROFS)) {
			logger.Fatalf("Do not have write permission on %s", mp)
		} else if f != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}
}

func genFuseOptExt(c *cli.Context, name string) (fuseOpt string, mt int, noxattr, noacl bool) {
	enableXattr := c.Bool("enable-xattr")
	// todo: wait for the implementation of acl
	if c.Bool("enable-acl") {
		enableXattr = true
	}
	return genFuseOpt(c, name), 1, !enableXattr, !c.Bool("enable-acl")
}

func shutdownGraceful(mp string) {
	_, conf, err := loadConfig(mp)
	if err != nil {
		logger.Warnf("load config from %s: %s", mp, err)
		return
	}
	fuseFd, fuseSetting = getFuseFd(conf.CommPath)
	if fuseFd == 0 {
		logger.Warnf("fail to recv FUSE fd from %s", conf.CommPath)
		return
	}
	for i := 0; i < 600; i++ {
		if err := syscall.Kill(conf.Pid, syscall.SIGHUP); err != nil {
			os.Setenv("_FUSE_STATE_PATH", conf.StatePath)
			os.Setenv("_JFS_META_SID", strconv.Itoa(int(conf.Meta.Sid)))
			return
		}
		time.Sleep(time.Millisecond * 100)
	}
	logger.Infof("mount point %s is busy, stop upgrade, mount on top of it", mp)
	err = sendFuseFd(conf.CommPath, string(fuseSetting), fuseFd)
	if err != nil {
		logger.Warnf("send FUSE fd: %s", err)
	}
	fuseFd = 0
}

func canShutdownGracefully(mp string, newConf *vfs.Config) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	var ino uint64
	var err error
	err = utils.WithTimeout(func() error {
		ino, err = utils.GetFileInode(mp)
		return err
	}, time.Second*3)
	if err != nil {
		logger.Warnf("get inode of %s: %s", mp, err)
		_ = doUmount(mp, true)
		return false
	} else if ino != 1 {
		return false
	}
	_, oldConf, err := loadConfig(mp)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warnf("load config: %s", err)
		}
		return false
	}
	if oldConf.Pid == 0 || oldConf.CommPath == "" {
		logger.Infof("mount point %s is not ready for upgrade, mount on top of it", mp)
		return false
	}
	if oldConf.Format.Name != newConf.Format.Name {
		logger.Infof("different volume %s != %s, mount on top of it", oldConf.Format.Name, newConf.Format.Name)
		return false
	}
	if oldConf.FuseOpts != nil && !reflect.DeepEqual(oldConf.FuseOpts.StripOptions(), newConf.FuseOpts.StripOptions()) {
		logger.Infof("different options, mount on top of it: %v != %v", oldConf.FuseOpts.StripOptions(), newConf.FuseOpts.StripOptions())
		return false
	}
	if oldConf.FuseOpts.DisableXAttrs && !newConf.FuseOpts.DisableXAttrs {
		logger.Infof("Xattr is enabled, mount on top of it")
		return false
	}
	return true
}

func absPath(d string) string {
	if strings.HasPrefix(d, "/") {
		return d
	}
	if strings.HasPrefix(d, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, d[1:])
		} else {
			logger.Fatalf("Expand user home dir of %s: %s", d, err)
		}
	}
	d, err := filepath.Abs(d)
	if err != nil {
		logger.Fatalf("Expand %s: %s", d, err)
	}
	return d
}

func fixCacheDirs(c *cli.Context) {
	cd := c.String("cache-dir")
	if cd == "memory" || strings.HasPrefix(cd, "/") {
		return
	}
	ds := utils.SplitDir(cd)
	for i, d := range ds {
		ds[i] = absPath(d)
	}
	for i, a := range os.Args {
		if i > 0 && os.Args[i-1] == "--cache-dir" && a == cd || a == "--cache-dir="+cd {
			os.Args[i] = a[:len(a)-len(cd)] + strings.Join(ds, string(os.PathListSeparator))
		}
	}
}

func makeDaemon(c *cli.Context, conf *vfs.Config) error {
	var attrs godaemon.DaemonAttr
	logfile := c.String("log")
	mp := conf.Meta.MountPoint
	attrs.OnExit = func(stage int) error {
		if stage == 0 {
			checkMountpoint(conf.Format.Name, mp, logfile, true)
		}
		return nil
	}

	// the current dir will be changed to root in daemon,
	// so the mount point has to be an absolute path.
	if godaemon.Stage() == 0 {
		mp := c.Args().Get(1)
		amp, err := filepath.Abs(mp)
		if err == nil && amp != mp {
			for i := len(os.Args) - 1; i > 2; i-- {
				if os.Args[i] == mp {
					// FIXME: it could be other options
					os.Args[i] = amp
					break
				}
			}
		}
		fixCacheDirs(c)

		_ = os.MkdirAll(filepath.Dir(logfile), 0755)
		attrs.Stdout, err = os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Errorf("open log file %s: %s", logfile, err)
		}
	}
	_, _, err := godaemon.MakeDaemon(&attrs)
	return err
}

func increaseRlimit() {
	var n uint64 = 100000
	err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Max: n, Cur: n})
	for err != nil && n > 1024 {
		n = n * 2 / 3
		err = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Max: n, Cur: n})
	}
	if err != nil {
		logger.Warnf("setrlimit to %d: %s", n, err)
	}
}

// change oom_score_adj to avoid OOM-killer
func adjustOOMKiller(score int) {
	if os.Getuid() != 0 {
		return
	}
	f, err := os.OpenFile("/proc/self/oom_score_adj", os.O_WRONLY, 0666)
	if err != nil {
		if !os.IsNotExist(err) {
			println(err)
		}
		return
	}
	defer f.Close()
	_, err = f.WriteString(strconv.Itoa(score))
	if err != nil {
		println("adjust OOM score:", err)
	}
}

func installHandler(mp string, v *vfs.VFS) {
	// Go will catch all the signals
	signal.Ignore(syscall.SIGPIPE)
	signalChan := make(chan os.Signal, 10)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for {
			sig := <-signalChan
			logger.Infof("Received signal %s, exiting...", sig.String())
			if sig == syscall.SIGHUP {
				path := fmt.Sprintf("/tmp/state%d.json", os.Getppid())
				if err := v.FlushAll(path); err == nil {
					fuse.Shutdown()
					err = v.FlushAll(path)
					if err != nil {
						logger.Fatalf("flush buffered data failed: %s", err)
					}
					logger.Warnf("exit with code 1")
					os.Exit(1)
				} else {
					logger.Warnf("flush buffered data failed: %s, don't restart", err)
					continue
				}
			}
			go func() {
				time.Sleep(time.Second * 30)
				if err := v.FlushAll(""); err != nil {
					logger.Errorf("flush all: %s", err)
				}
				logger.Fatalf("exit after received %s,but umount not finished after 30 seconds, force exit", sig)
			}()
			go func() { _ = doUmount(mp, true) }()
		}
	}()
}
func launchMount(mp string, conf *vfs.Config) error {
	increaseRlimit()
	if runtime.GOOS == "linux" {
		adjustOOMKiller(-1000)
		if canShutdownGracefully(mp, conf) {
			shutdownGraceful(mp)
		}
		os.Setenv("_FUSE_FD_COMM", serverAddress)
		serveFuseFD(serverAddress)
	}

	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %s", err)
	}
	start := time.Now()
	for c := 0; ; c++ {
		if c == 3 && time.Since(start) < time.Second*10 {
			return fmt.Errorf("fail 3 times in %s, give up", time.Since(start))
		}
		// For volcengine VKE serverless container, no umount before mount when
		// `JFS_NO_UMOUNT` environment provided
		noUmount := os.Getenv("JFS_NO_UMOUNT")
		if fuseFd == 0 && (c > 0 || noUmount == "0") {
			_ = doUmount(mp, true)
		}
		if runtime.GOOS == "linux" {
			if !utils.Exists(serverAddress) {
				serveFuseFD(serverAddress)
			}
		}

		cmd := exec.Command(path, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Start()
		if err != nil {
			logger.Errorf("start process %s: %s", path, err)
			time.Sleep(time.Second)
			continue
		}
		os.Unsetenv("_FUSE_STATE_PATH")

		ctx, cancel := context.WithCancel(context.TODO())
		go watchdog(ctx, mp)
		err = cmd.Wait()
		cancel()
		if err == nil {
			return nil
		} else {
			var exitError *exec.ExitError
			if ok := errors.As(err, &exitError); ok {
				if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok && waitStatus.ExitStatus() == meta.UmountCode {
					logger.Errorf("received umount exit code")
					_ = doUmount(mp, true)
					return nil
				}
			}
			if fuseFd < 0 {
				logger.Info("transfer FUSE session to others")
				return nil
			}
			time.Sleep(time.Second)
		}
	}
}

func mountMain(v *vfs.VFS, c *cli.Context) {
	if os.Getuid() == 0 {
		disableUpdatedb()
	}
	conf := v.Conf
	conf.AttrTimeout = utils.Duration(c.String("attr-cache"))
	conf.EntryTimeout = utils.Duration(c.String("entry-cache"))
	conf.DirEntryTimeout = utils.Duration(c.String("dir-entry-cache"))
	conf.NonDefaultPermission = c.Bool("non-default-permission")
	rootSquash := c.String("root-squash")
	if rootSquash != "" {
		var uid, gid uint32 = 65534, 65534
		if u, err := user.Lookup("nobody"); err == nil {
			nobody, err := strconv.ParseUint(u.Uid, 10, 32)
			if err != nil {
				logger.Fatalf("invalid uid: %s", u.Uid)
			}
			uid = uint32(nobody)
		}
		if g, err := user.LookupGroup("nogroup"); err == nil {
			nogroup, err := strconv.ParseUint(g.Gid, 10, 32)
			if err != nil {
				logger.Fatalf("invalid gid: %s", g.Gid)
			}
			gid = uint32(nogroup)
		}

		ss := strings.SplitN(strings.TrimSpace(rootSquash), ":", 2)
		if ss[0] != "" {
			u, err := strconv.ParseUint(ss[0], 10, 32)
			if err != nil {
				logger.Fatalf("invalid uid: %s", ss[0])
			}
			uid = uint32(u)
		}
		if len(ss) == 2 && ss[1] != "" {
			g, err := strconv.ParseUint(ss[1], 10, 32)
			if err != nil {
				logger.Fatalf("invalid gid: %s", ss[1])
			}
			gid = uint32(g)
		}
		conf.RootSquash = &vfs.RootSquash{Uid: uid, Gid: gid}
	}
	logger.Infof("Mounting volume %s at %s ...", conf.Format.Name, conf.Meta.MountPoint)
	err := fuse.Serve(v, c.String("o"), c.Bool("enable-xattr"), c.Bool("enable-ioctl"))
	if err != nil {
		logger.Fatalf("fuse: %s", err)
	}
}
