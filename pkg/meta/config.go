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

package meta

import "time"

// Config for clients.
type Config struct {
	Strict      bool // update ctime
	Retries     int
	CaseInsensi bool
	ReadOnly    bool
	OpenCache   time.Duration
	MountPoint  string
	Subdir      string
}

type Format struct {
	Name        string
	UUID        string
	Storage     string
	Bucket      string
	AccessKey   string
	SecretKey   string `json:",omitempty"`
	BlockSize   int
	Compression string
	Shards      int
	Partitions  int
	Capacity    uint64
	Inodes      uint64
	EncryptKey  string `json:",omitempty"`
}

func (f *Format) RemoveSecret() {
	if f.SecretKey != "" {
		f.SecretKey = "removed"
	}
	if f.EncryptKey != "" {
		f.EncryptKey = "removed"
	}
}
