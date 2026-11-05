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

// The expected values are the defaults of Client.__init__ in juicefs.py, as
// libjfs parses them.
func TestDefaultConfig(t *testing.T) {
	want := &Config{
		Bucket:              "",                     // bucket=""
		StorageClass:        "",                     // storage_class=""
		ReadOnly:            false,                  // read_only=False
		NoSession:           false,                  // no_session=False
		NoBGJob:             true,                   // no_bgjob=True
		OpenCache:           0,                      // open_cache="0"
		BackupMeta:          time.Hour,              // backup_meta="3600"
		BackupSkipTrash:     false,                  // backup_skip_trash=False
		Heartbeat:           12 * time.Second,       // heartbeat="12"
		CacheDir:            "memory",               // cache_dir="memory"
		CacheSize:           100 << 20,              // cache_size="100M"
		FreeSpaceRatio:      0.1,                    // free_space_ratio="0.1"
		CachePartialOnly:    false,                  // cache_partial_only=False
		VerifyCacheChecksum: "extend",               // verify_cache_checksum="extend"
		CacheEviction:       "2-random",             // cache_eviction="2-random"
		CacheScanInterval:   time.Hour,              // cache_scan_interval="3600"
		CacheExpire:         0,                      // cache_expire="0"
		Writeback:           false,                  // writeback=False
		BufferSize:          300 << 20,              // buffer_size="300M"
		Prefetch:            1,                      // prefetch=1
		MaxReadahead:        0,                      // max_readahead="0"
		UploadLimit:         0,                      // upload_limit="0"
		DownloadLimit:       0,                      // download_limit="0"
		MaxUploads:          20,                     // max_uploads=20
		MaxDeletes:          10,                     // max_deletes=10
		SkipDirNlink:        20,                     // skip_dir_nlink=20
		SkipDirMtime:        100 * time.Millisecond, // skip_dir_mtime="100ms"
		IORetries:           10,                     // io_retries=10
		GetTimeout:          5 * time.Second,        // get_timeout="5"
		PutTimeout:          60 * time.Second,       // put_timeout="60"
		FastResolve:         false,                  // fast_resolve=False
		AttrCache:           time.Second,            // attr_cache="1s"
		EntryCache:          0,                      // entry_cache="0s"
		DirEntryCache:       time.Second,            // dir_entry_cache="1s"
		Debug:               false,                  // debug=False
		NoUsageReport:       false,                  // no_usage_report=False
		AccessLog:           "",                     // access_log=""
		PushGateway:         "",                     // push_gateway=""
		PushInterval:        10 * time.Second,       // push_interval="10"
		PushAuth:            "",                     // push_auth=""
		PushLabels:          "",                     // push_labels=""
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
