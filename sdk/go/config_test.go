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
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	want := &Config{
		Bucket:              "",
		StorageClass:        "",
		ReadOnly:            false,
		NoSession:           false,
		NoBGJob:             true,
		OpenCache:           0,
		BackupMeta:          time.Hour,
		BackupSkipTrash:     false,
		Heartbeat:           12 * time.Second,
		CacheDir:            "memory",
		CacheSize:           100 << 20,
		FreeSpaceRatio:      0.1,
		CachePartialOnly:    false,
		VerifyCacheChecksum: "extend",
		CacheEviction:       "2-random",
		CacheScanInterval:   time.Hour,
		CacheExpire:         0,
		Writeback:           false,
		BufferSize:          300 << 20,
		Prefetch:            1,
		MaxReadahead:        0,
		UploadLimit:         0,
		DownloadLimit:       0,
		MaxUploads:          20,
		MaxDeletes:          10,
		SkipDirNlink:        20,
		SkipDirMtime:        100 * time.Millisecond,
		IORetries:           10,
		GetTimeout:          5 * time.Second,
		PutTimeout:          60 * time.Second,
		FastResolve:         false,
		AttrCache:           time.Second,
		EntryCache:          0,
		DirEntryCache:       time.Second,
		Debug:               false,
		NoUsageReport:       false,
		AccessLog:           "",
		PushGateway:         "",
		PushInterval:        10 * time.Second,
		PushAuth:            "",
		PushLabels:          "",
	}
	if got := DefaultConfig(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultConfig() = %+v\nwant %+v", got, want)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().validate(); err != nil {
		t.Fatalf("default config: %s", err)
	}
	if err := (&Config{}).validate(); err != nil {
		t.Fatalf("zero config: %s", err)
	}
	conf := DefaultConfig()
	conf.Heartbeat = -time.Second
	err := conf.validate()
	if err == nil || !strings.Contains(err.Error(), "Heartbeat") {
		t.Fatalf("negative Heartbeat: got %v", err)
	}
	conf = DefaultConfig()
	conf.MaxUploads = -1
	err = conf.validate()
	if err == nil || !strings.Contains(err.Error(), "MaxUploads") {
		t.Fatalf("negative MaxUploads: got %v", err)
	}

	conf = DefaultConfig()
	conf.PushGateway = "127.0.0.1:1"
	conf.PushInterval = 0
	if err = conf.validate(); err == nil || !strings.Contains(err.Error(), "PushInterval") {
		t.Fatalf("zero PushInterval: got %v", err)
	}

	for _, good := range []string{"", "env:prod", "env:prod;vol_name:other", "k:"} {
		conf = DefaultConfig()
		conf.PushLabels = good
		if err = conf.validate(); err != nil {
			t.Fatalf("PushLabels %q: %s", good, err)
		}
	}
	for _, bad := range []string{"env", "env:a:b", "env:prod;"} {
		conf = DefaultConfig()
		conf.PushLabels = bad
		if err = conf.validate(); err == nil || !strings.Contains(err.Error(), "push label") {
			t.Fatalf("PushLabels %q: got %v", bad, err)
		}
	}
}
