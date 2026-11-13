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
	"bytes"
	"io/fs"
	"syscall"
	"time"
)

type XattrFlag int

const (
	XattrCreate  XattrFlag = 1
	XattrReplace XattrFlag = 2
)

func (c *Client) Getxattr(name, attr string) ([]byte, error) {
	if err := c.check(); err != nil {
		return nil, pathError("getxattr", name, err)
	}
	data, err := c.backend.getxattr(name, attr)
	if err != nil {
		return nil, pathError("getxattr", name, err)
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

func (c *Client) Listxattr(name string) ([]string, error) {
	if err := c.check(); err != nil {
		return nil, pathError("listxattr", name, err)
	}
	buf, err := c.backend.listxattr(name)
	if err != nil {
		return nil, pathError("listxattr", name, err)
	}
	attrs := []string{}
	for _, a := range bytes.Split(buf, []byte{0}) {
		if len(a) > 0 {
			attrs = append(attrs, string(a))
		}
	}
	return attrs, nil
}

func (c *Client) Setxattr(name, attr string, data []byte, flags XattrFlag) error {
	err := c.check()
	if err == nil {
		switch flags {
		case 0, XattrCreate, XattrReplace:
			err = c.backend.setxattr(name, attr, data, flags)
		default:
			err = syscall.EINVAL
		}
	}
	return pathError("setxattr", name, err)
}

func (c *Client) Removexattr(name, attr string) error {
	err := c.check()
	if err == nil {
		err = c.backend.removexattr(name, attr)
	}
	return pathError("removexattr", name, err)
}

func (c *Client) Clone(src, dst string, preserve bool) error {
	err := c.check()
	if err == nil {
		err = c.backend.clone(src, dst, preserve)
	}
	return linkError("clone", src, dst, err)
}

type InfoOptions struct {
	Recursive bool
	Strict    bool
}

type InfoResult struct {
	Length, Size, Files, Dirs uint64
}

func (c *Client) Info(name string, opts *InfoOptions) (*InfoResult, error) {
	if opts == nil {
		opts = &InfoOptions{}
	}
	if err := c.check(); err != nil {
		return nil, pathError("info", name, err)
	}
	r, err := c.backend.info(name, opts.Recursive, opts.Strict)
	if err != nil {
		return nil, pathError("info", name, err)
	}
	return r, nil
}

type TreeSummary struct {
	Path              string
	Type              fs.FileMode // type bits only, as fs.DirEntry.Type
	Size, Files, Dirs uint64
	Children          []*TreeSummary // sorted by Size in descending order
}

func (c *Client) Summary(name string, depth, entries int) (*TreeSummary, error) {
	if err := c.check(); err != nil {
		return nil, pathError("summary", name, err)
	}
	if depth < 0 || depth > 255 || entries < 0 || entries > 255 {
		return nil, pathError("summary", name, syscall.EINVAL)
	}
	s, err := c.backend.summary(name, uint8(depth), uint8(entries))
	if err != nil {
		return nil, pathError("summary", name, err)
	}
	return s, nil
}

type Quota struct {
	MaxSpace, MaxInodes   int64
	UsedSpace, UsedInodes int64
}

type QuotaOptions struct {
	Capacity uint64 // bytes, 0 leaves it unchanged
	Inodes   uint64 // 0 leaves it unchanged
	Create   bool   // create the directory and its parents if missing
	Strict   bool   // compute the usage of a new directory quota strictly
}

type CheckQuotaOptions struct {
	Repair bool // fix the recorded usage
	Strict bool // compute the usage strictly
}

const (
	quotaSet uint8 = iota
	quotaGet
	quotaDel
	quotaList
	quotaCheck
)

func (c *Client) SetQuota(name string, opts QuotaOptions) error {
	err := c.check()
	if err == nil {
		_, err = c.backend.quota(quotaSet, name, opts.Capacity, opts.Inodes, opts.Strict, false, opts.Create)
	}
	return pathError("setquota", name, err)
}

func (c *Client) GetQuota(name string) (*Quota, error) {
	if err := c.check(); err != nil {
		return nil, pathError("getquota", name, err)
	}
	qs, err := c.backend.quota(quotaGet, name, 0, 0, false, false, false)
	if err != nil {
		return nil, pathError("getquota", name, err)
	}
	for _, q := range qs {
		return q, nil
	}
	return nil, nil
}

func (c *Client) DelQuota(name string) error {
	err := c.check()
	if err == nil {
		_, err = c.backend.quota(quotaDel, name, 0, 0, false, false, false)
	}
	return pathError("delquota", name, err)
}

func (c *Client) ListQuota() (map[string]*Quota, error) {
	if err := c.check(); err != nil {
		return nil, syscallError("listquota", err)
	}
	qs, err := c.backend.quota(quotaList, "", 0, 0, false, false, false)
	if err != nil {
		return nil, syscallError("listquota", err)
	}
	return qs, nil
}

func (c *Client) CheckQuota(name string, opts *CheckQuotaOptions) (map[string]*Quota, error) {
	if opts == nil {
		opts = &CheckQuotaOptions{}
	}
	if err := c.check(); err != nil {
		return nil, pathError("checkquota", name, err)
	}
	qs, err := c.backend.quota(quotaCheck, name, 0, 0, opts.Strict, opts.Repair, false)
	if err != nil {
		return nil, pathError("checkquota", name, err)
	}
	return qs, nil
}

type WarmupOptions struct {
	Threads    int
	Evict      bool
	Check      bool
	Background bool
}

type WarmupResult struct {
	FileCount, SliceCount, TotalBytes, MissBytes uint64
	Locations                                    map[string]uint64
}

func (c *Client) Warmup(paths []string, opts *WarmupOptions) (*WarmupResult, error) {
	if opts == nil {
		opts = &WarmupOptions{}
	}
	if err := c.check(); err != nil {
		return nil, syscallError("warmup", err)
	}
	threads := opts.Threads
	switch {
	case threads < 0:
		return nil, syscallError("warmup", syscall.EINVAL)
	case threads == 0:
		threads = 10
	}
	r, err := c.backend.warmup(paths, threads, opts.Evict, opts.Check, opts.Background)
	if err != nil {
		return nil, syscallError("warmup", err)
	}
	if opts.Background {
		return nil, nil
	}
	return r, nil
}

type StatusResult struct {
	Setting  VolumeSetting
	Sessions []*Session // without Sustained, Flocks and Plocks
	Stat     VolumeStat
}

type VolumeSetting struct {
	Name          string
	UUID          string
	Storage       string
	Bucket        string
	StorageClass  string
	BlockSize     int    // bytes
	Compression   string // "" means none
	EncryptAlgo   string // "" means not encrypted
	Capacity      uint64 // bytes, 0 means no limit
	Inodes        uint64 // 0 means no limit
	TrashDays     int    // 0 means no trash
	UploadLimit   int64  // Mbps, 0 means no limit
	DownloadLimit int64  // Mbps, 0 means no limit
	EnableACL     bool
}

type VolumeStat struct {
	UsedSpace, AvailableSpace   uint64 // bytes
	UsedInodes, AvailableInodes uint64
	Trash                       *TrashStat // only filled by Status(true)
}

type TrashStat struct {
	TrashFileCount, TrashFileSize                     int64
	PendingDeletedFileCount, PendingDeletedFileSize   int64
	TrashSliceCount, TrashSliceSize                   int64
	PendingDeletedSliceCount, PendingDeletedSliceSize int64
}

type Session struct {
	Sid        uint64
	Expire     time.Time
	Version    string
	HostName   string
	IPAddrs    []string
	MountPoint string
	MountTime  time.Time
	ProcessID  int
	// Only filled by SessionInfo.
	Sustained []uint64 // inodes deleted but still open in the session
	Flocks    []Flock
	Plocks    []Plock
}

type Flock struct {
	Inode, Owner uint64
	Type         string // "R" or "W"
}

type Plock struct {
	Inode, Owner uint64
	Records      []PlockRecord
}

type PlockRecord struct {
	Type       string // "R" or "W"
	Pid        uint32
	Start, End uint64
}

func (c *Client) Status(trash bool) (*StatusResult, error) {
	if err := c.check(); err != nil {
		return nil, syscallError("status", err)
	}
	r, err := c.backend.status(trash)
	if err != nil {
		return nil, syscallError("status", err)
	}
	return r, nil
}

func (c *Client) SessionInfo(sid uint64) (*Session, error) {
	if err := c.check(); err != nil {
		return nil, syscallError("sessioninfo", err)
	}
	s, err := c.backend.sessionInfo(sid)
	if err != nil {
		return nil, syscallError("sessioninfo", err)
	}
	return s, nil
}
