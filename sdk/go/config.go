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
	"fmt"
	"strings"
	"time"
)

// Config holds the client options, which match the keyword arguments of the
// Python SDK's Client. Start from DefaultConfig: a zero field means zero, not
// the default.
type Config struct {
	Bucket          string
	StorageClass    string
	ReadOnly        bool
	NoSession       bool
	NoBGJob         bool
	OpenCache       time.Duration
	BackupMeta      time.Duration
	BackupSkipTrash bool
	Heartbeat       time.Duration

	CacheDir            string
	CacheSize           uint64
	FreeSpaceRatio      float64
	CachePartialOnly    bool
	VerifyCacheChecksum string
	CacheEviction       string
	CacheScanInterval   time.Duration
	CacheExpire         time.Duration
	Writeback           bool

	BufferSize    uint64
	Prefetch      int
	MaxReadahead  uint64
	UploadLimit   int64
	DownloadLimit int64
	MaxUploads    int
	MaxDeletes    int
	SkipDirNlink  int
	SkipDirMtime  time.Duration
	IORetries     int
	GetTimeout    time.Duration
	PutTimeout    time.Duration

	FastResolve   bool
	AttrCache     time.Duration
	EntryCache    time.Duration
	DirEntryCache time.Duration

	Debug         bool
	NoUsageReport bool
	AccessLog     string

	PushGateway  string
	PushInterval time.Duration
	PushAuth     string
	PushLabels   string
}

func DefaultConfig() *Config {
	return &Config{
		NoBGJob:             true,
		BackupMeta:          time.Hour,
		Heartbeat:           12 * time.Second,
		CacheDir:            "memory",
		CacheSize:           100 << 20,
		FreeSpaceRatio:      0.1,
		VerifyCacheChecksum: "extend",
		CacheEviction:       "2-random",
		CacheScanInterval:   time.Hour,
		BufferSize:          300 << 20,
		Prefetch:            1,
		MaxUploads:          20,
		MaxDeletes:          10,
		SkipDirNlink:        20,
		SkipDirMtime:        100 * time.Millisecond,
		IORetries:           10,
		GetTimeout:          5 * time.Second,
		PutTimeout:          60 * time.Second,
		AttrCache:           time.Second,
		DirEntryCache:       time.Second,
		PushInterval:        10 * time.Second,
	}
}

func (c *Config) validate() error {
	for _, f := range []struct {
		name     string
		negative bool
	}{
		{"OpenCache", c.OpenCache < 0},
		{"BackupMeta", c.BackupMeta < 0},
		{"Heartbeat", c.Heartbeat < 0},
		{"FreeSpaceRatio", c.FreeSpaceRatio < 0},
		{"CacheScanInterval", c.CacheScanInterval < 0},
		{"CacheExpire", c.CacheExpire < 0},
		{"Prefetch", c.Prefetch < 0},
		{"UploadLimit", c.UploadLimit < 0},
		{"DownloadLimit", c.DownloadLimit < 0},
		{"MaxUploads", c.MaxUploads < 0},
		{"MaxDeletes", c.MaxDeletes < 0},
		{"SkipDirNlink", c.SkipDirNlink < 0},
		{"SkipDirMtime", c.SkipDirMtime < 0},
		{"IORetries", c.IORetries < 0},
		{"GetTimeout", c.GetTimeout < 0},
		{"PutTimeout", c.PutTimeout < 0},
		{"AttrCache", c.AttrCache < 0},
		{"EntryCache", c.EntryCache < 0},
		{"DirEntryCache", c.DirEntryCache < 0},
		{"PushInterval", c.PushInterval < 0},
	} {
		if f.negative {
			return fmt.Errorf("invalid config: %s must not be negative", f.name)
		}
	}
	// time.NewTicker panics on a non-positive interval.
	if c.PushGateway != "" && c.PushInterval == 0 {
		return fmt.Errorf("invalid config: PushInterval must be positive when PushGateway is set")
	}
	// "key1:value1;key2:value2", as jfs_init accepts.
	if c.PushLabels != "" {
		for _, kv := range strings.Split(c.PushLabels, ";") {
			if _, v, ok := strings.Cut(kv, ":"); !ok || strings.Contains(v, ":") {
				return fmt.Errorf("invalid config: push label %q, want key:value", kv)
			}
		}
	}
	return nil
}
