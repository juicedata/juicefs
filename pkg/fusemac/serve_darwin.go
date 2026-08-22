//go:build darwin

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

package fusemac

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
	"github.com/winfsp/cgofuse/fuse"
)

const (
	fuseTURL   = "https://www.fuse-t.org/"
	macfuseURL = "https://osxfuse.github.io/"
)

var (
	activeHostMu sync.Mutex
	activeHost   *fuse.FileSystemHost
	activeFS     *FS
	signalled    atomic.Bool
)

var ErrGracefulUnmount = errors.New("fuse session ended by unmount")

func MarkSignalShutdown() {
	signalled.Store(true)
}

func UnmountActive() bool {
	activeHostMu.Lock()
	defer activeHostMu.Unlock()
	if activeHost == nil {
		return false
	}
	if activeFS != nil && activeFS.destroyed.Load() != 0 {
		return true
	}
	return activeHost.Unmount()
}

// allowUnsafeFskit is the -o escape hatch that opts into macFUSE's FSKit
// backend despite its broken write path.
const allowUnsafeFskit = "allow_unsafe_fskit_macfuse"

func hasOpt(opts, name string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		if strings.TrimSpace(opt) == name {
			return true
		}
	}
	return false
}

// installedLibs is a variable so tests can fake which FUSE libraries exist.
var installedLibs = func() (fuseT, macfuse bool) {
	if _, err := os.Stat("/usr/local/lib/libfuse-t.dylib"); err == nil {
		fuseT = true
	}
	if _, err := os.Stat("/usr/local/lib/libfuse.2.dylib"); err == nil {
		macfuse = true
	}
	if _, err := os.Stat("/usr/local/lib/libosxfuse.2.dylib"); err == nil {
		macfuse = true
	}
	return
}

func CheckBackend(backend, opts string) error {
	fuseT, macfuse := installedLibs()

	switch backend {
	case "nfs", "smb":
		if macfuse {
			return fmt.Errorf("backend=%s is designed for FUSE-T (%s); to prevent cgofuse from defaulting to macFUSE, please temporarily remove the macFUSE dylibs from /usr/local/lib", backend, fuseTURL)
		}
		if !fuseT {
			return fmt.Errorf("backend=%s requires FUSE-T; please install it from %s to continue", backend, fuseTURL)
		}

	case "fskit":
		if macfuse {
			if !hasOpt(opts, allowUnsafeFskit) {
				return fmt.Errorf("backend=fskit has known compatibility issues with macFUSE; please install FUSE-T (%s) for stable operation, or add '%s' to your -o options to proceed anyway", fuseTURL, allowUnsafeFskit)
			}
			logger.Warnf("Running backend=fskit with macFUSE via the '%s' flag; note that write operations may be unstable", allowUnsafeFskit)
			return nil
		}
		if !fuseT {
			return fmt.Errorf("backend=fskit requires a compatible FUSE runtime; please install either FUSE-T (%s) or macFUSE (%s)", fuseTURL, macfuseURL)
		}
	}

	return nil
}
func buildDarwinMountOptions(conf *vfs.Config, userOpts string) []string {
	var opts []string
	if volName := conf.Format.Name; volName != "" {
		opts = append(opts, "-o", "volname="+volName)
	}
	// daemon_timeout keeps slow operations (e.g. flushes over NFS/SMB) from
	// getting killed; making defaults it to 10 minutes on macOS.
	opts = append(opts, "-o", "daemon_timeout=600")
	opts = append(opts, "-o", "atomic_o_trunc")
	opts = append(opts, "-o", fmt.Sprintf("attr_timeout=%g", conf.AttrTimeout.Seconds()))
	opts = append(opts, "-o", fmt.Sprintf("entry_timeout=%g", conf.EntryTimeout.Seconds()))
	opts = append(opts, "-o", fmt.Sprintf("max_readahead=%d", 1024*1024))
	if conf.FuseOpts != nil {
		if conf.FuseOpts.AllowOther {
			opts = append(opts, "-o", "allow_other")
		}
	}
	for opt := range strings.SplitSeq(userOpts, ",") {
		if opt = strings.TrimSpace(opt); opt != "" {
			opts = append(opts, "-o", opt)
		}
	}
	return opts
}

func mountFailure(jfs *FS, mp string) error {
	if jfs.destroyed.Load() != 0 || signalled.Load() {
		return ErrGracefulUnmount
	}
	return fmt.Errorf("cgofuse host.Mount failed on %s", mp)
}

func Serve(v *vfs.VFS, userOpts string, xattrs, ioctl bool, c *cli.Context) error {
	jfs := NewFS(v.Conf, v)
	jfs.xattrs = xattrs
	if c != nil {
		jfs.asRoot = c.Bool("as-root")
		if c.IsSet("readdir-batch-size") {
			jfs.readdirBatchSize = c.Int("readdir-batch-size")
		}
	}

	host := fuse.NewFileSystemHost(jfs)
	jfs.host = host

	activeHostMu.Lock()
	activeHost = host
	activeFS = jfs
	activeHostMu.Unlock()
	defer func() {
		activeHostMu.Lock()
		if activeHost == host {
			activeHost = nil
			activeFS = nil
		}
		activeHostMu.Unlock()
	}()

	options := buildDarwinMountOptions(v.Conf, userOpts)
	logger.Infof("Mounting with Kext-less mode on macOS")

	if !host.Mount(v.Conf.Meta.MountPoint, options) {
		return mountFailure(jfs, v.Conf.Meta.MountPoint)
	}
	return nil
}
