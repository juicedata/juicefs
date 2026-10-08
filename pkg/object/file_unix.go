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

package object

import (
	"os"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/pkg/sftp"
)

func getOwnerGroup(info os.FileInfo) (string, string) {
	var owner, group string
	switch st := info.Sys().(type) {
	case *syscall.Stat_t:
		owner = utils.UserName(int(st.Uid))
		group = utils.GroupName(int(st.Gid))
	case *sftp.FileStat:
		owner = utils.UserName(int(st.UID))
		group = utils.GroupName(int(st.GID))
	}
	return owner, group
}

func (d *filestore) Chtimes(key string, mtime time.Time) error {
	p := d.path(key)
	return lchtimes(p, time.Time{}, mtime)
}
