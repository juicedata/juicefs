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

package meta

import (
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/version"
	"golang.org/x/mod/semver"
)

// Config for clients.
type Config struct {
	Strict      bool // update ctime
	Retries     int
	CaseInsensi bool
	ReadOnly    bool
	OpenCache   time.Duration
	MountPoint  string
	Subdir      string
	MaxDeletes  int
}

type Format struct {
	Name           string
	UUID           string
	Storage        string
	Bucket         string
	AccessKey      string
	SecretKey      string `json:",omitempty"`
	BlockSize      int
	Compression    string
	Shards         int
	Partitions     int
	Capacity       uint64
	Inodes         uint64
	EncryptKey     string `json:",omitempty"`
	TrashDays      int
	MetaVersion    int
	ClientVersions string
}

func (f *Format) RemoveSecret() {
	if f.SecretKey != "" {
		f.SecretKey = "removed"
	}
	if f.EncryptKey != "" {
		f.EncryptKey = "removed"
	}
}

func (f *Format) CheckVersion() bool {
	if f.MetaVersion > 1 {
		return false
	}

	if f.ClientVersions == "" {
		return true
	}
	ps := strings.Split(f.ClientVersions, " ")
	if version.Canonical(ps[0]) == "" {
		logger.Errorf("Invalid version string: %s", ps[0])
		return false
	}
	v := version.Version()
	ok := semver.Compare(v, ps[0]) >= 0
	if ok && len(ps) > 1 {
		if version.Canonical(ps[1]) == "" {
			logger.Errorf("Invalid version string: %s", ps[1])
			return false
		}
		ok = semver.Compare(v, ps[1]) <= 0
	}
	return ok
}
