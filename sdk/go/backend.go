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
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// backend accesses the volume: pkg/fs by default, libjfs with -tags libjfs.
// Each backend also provides newBackend(name, metaURL string, conf *Config).
// Both must give the same semantics: paths are passed as given and resolved
// as POSIX on Linux does (design 6.2), errors are syscall.Errno or nil.
type backend interface {
	stat(path string, follow bool) (*Stat, error)
	// readdir returns all entries without "." and "..", unsorted.
	readdir(path string) ([]*fileInfo, error)
	mkdir(path string, mode, umask uint16) error
	// mkdirAll returns EEXIST if path exists.
	mkdirAll(path string, mode, umask uint16) error
	remove(path string) error
	rmdir(path string) error
	removeAll(path string) error
	rename(oldpath, newpath string) error
	// link does not follow a symbolic link oldpath.
	link(oldpath, newpath string) error
	symlink(target, path string) error
	readlink(path string) (string, error)
	truncate(path string, size uint64) error
	chmod(path string, mode uint16) error
	chown(path string, uid, gid uint32, follow bool) error
	// utime leaves a zero time unchanged.
	utime(path string, atime, mtime time.Time) error
	gatherer() prometheus.Gatherer
	close() error
}
