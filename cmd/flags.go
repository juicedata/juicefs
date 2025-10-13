/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"fmt"
	"os"
	"path"
	"runtime"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/urfave/cli/v2"
)

func globalFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    "verbose",
			Aliases: []string{"debug", "v"},
			Usage:   "enable debug log",
		},
		&cli.BoolFlag{
			Name:    "quiet",
			Aliases: []string{"q"},
			Usage:   "show warning and errors only",
		},
		&cli.BoolFlag{
			Name:  "trace",
			Usage: "enable trace log",
		},
		&cli.StringFlag{
			Name:   "log-level",
			Usage:  "set log level (trace, debug, info, warn, error, fatal, panic)",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "log-id",
			Usage: "append the given log id in log, use \"random\" to use random uuid",
		},
		&cli.BoolFlag{
			Name:  "no-agent",
			Usage: "disable pprof (:6060) agent",
		},
		&cli.StringFlag{
			Name:  "pyroscope",
			Usage: "pyroscope address",
		},
		&cli.BoolFlag{
			Name:  "no-color",
			Usage: "disable colors",
		},
	}
}

func addCategory(f cli.Flag, cat string) {
	switch ff := f.(type) {
	case *cli.StringFlag:
		ff.Category = cat
	case *cli.BoolFlag:
		ff.Category = cat
	case *cli.IntFlag:
		ff.Category = cat
	case *cli.Int64Flag:
		ff.Category = cat
	case *cli.Uint64Flag:
		ff.Category = cat
	case *cli.Float64Flag:
		ff.Category = cat
	case *cli.StringSliceFlag:
		ff.Category = cat
	default:
		panic(f)
	}
}

func addCategories(cat string, flags []cli.Flag) []cli.Flag {
	for _, f := range flags {
		addCategory(f, cat)
	}
	return flags
}

func storageFlags() []cli.Flag {
	return addCategories("DATA STORAGE", []cli.Flag{
		&cli.StringFlag{
			Name:  "storage",
			Usage: "customized storage type (e.g. s3, gs, oss, cos) to access object store",
		},
		&cli.StringFlag{
			Name:  "bucket",
			Usage: "customized endpoint to access object store",
		},
		&cli.StringFlag{
			Name:  "storage-class",
			Usage: "the storage class for data written by current client",
		},
		&cli.StringFlag{
			Name:  "get-timeout",
			Value: "60s",
			Usage: "the timeout to download an object",
		},
		&cli.StringFlag{
			Name:  "put-timeout",
			Value: "60s",
			Usage: "the timeout to upload an object",
		},
		&cli.IntFlag{
			Name:  "io-retries",
			Value: 10,
			Usage: "number of retries after network failure",
		},
		&cli.IntFlag{
			Name:  "max-uploads",
			Value: 20,
			Usage: "number of connections to upload",
		},
		&cli.IntFlag{
			Name:  "max-stage-write",
			Value: 1000, // large enough for normal cases, also prevents unlimited concurrency in abnormal cases
			Usage: "number of threads allowed to write staged files, other requests will be uploaded directly (this option is only effective when 'writeback' mode is enabled)",
		},
		&cli.IntFlag{
			Name:  "max-deletes",
			Value: 10,
			Usage: "number of threads to delete objects",
		},
		&cli.StringFlag{
			Name:  "upload-limit",
			Usage: "bandwidth limit for upload in Mbps",
		},
		&cli.StringFlag{
			Name:  "download-limit",
			Usage: "bandwidth limit for download in Mbps",
		},
		&cli.BoolFlag{
			Name: "check-storage",
			// AK/SK should have been checked before creating volume, here checks client access to the storage
			Usage: "test storage before mounting to expose access issues early",
		},
	})
}

func dataCacheFlags() []cli.Flag {
	var defaultCacheDir = "/var/jfsCache"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Warn(err)
			homeDir = defaultCacheDir
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	}
	return addCategories("DATA CACHE", []cli.Flag{
		&cli.StringFlag{
			Name:  "buffer-size",
			Value: "300M",
			Usage: "total read/write buffering in MiB",
		},
		&cli.StringFlag{
			Name:  "max-readahead",
			Usage: "max buffering for read ahead in MiB per read session",
		},
		&cli.IntFlag{
			Name:  "prefetch",
			Value: 1,
			Usage: "prefetch N blocks in parallel",
		},
		&cli.BoolFlag{
			Name:  "writeback",
			Usage: "upload blocks in background",
		},
		&cli.StringFlag{
			Name:  "upload-delay",
			Value: "0s",
			Usage: "delayed duration for uploading blocks",
		},
		&cli.StringFlag{
			Name:  "upload-hours",
			Usage: "(start-end) hour of a day between which the delayed blocks can be uploaded",
		},
		&cli.StringFlag{
			Name:  "cache-dir",
			Value: defaultCacheDir,
			Usage: "directory paths of local cache, use colon to separate multiple paths",
		},
		&cli.StringFlag{
			Name:  "cache-mode",
			Value: "0600", // only owner can read/write cache
			Usage: "file permissions for cached blocks",
		},
		&cli.StringFlag{
			Name:  "cache-size",
			Value: "100G",
			Usage: "size of cached object for read in MiB",
		},
		&cli.Int64Flag{
			Name:  "cache-items",
			Value: 0,
			Usage: "max number of cached items (0 will be automatically calculated based on the `free‑space‑ratio`.)",
		},
		&cli.Float64Flag{
			Name:  "free-space-ratio",
			Value: 0.1,
			Usage: "min free space (ratio)",
		},
		&cli.BoolFlag{
			Name:  "cache-partial-only",
			Usage: "cache only random/small read",
		},
		&cli.BoolFlag{
			Name:  "cache-large-write",
			Usage: "cache full blocks after uploading",
		},
		&cli.StringFlag{
			Name:  "verify-cache-checksum",
			Value: "extend",
			Usage: "checksum level (none, full, shrink, extend)",
		},
		&cli.StringFlag{
			Name:  "cache-eviction",
			Value: chunk.Eviction2Random,
			Usage: fmt.Sprintf("cache eviction policy [%s, %s, %s]", chunk.EvictionNone, chunk.Eviction2Random, chunk.EvictionLRU),
		},
		&cli.StringFlag{
			Name:  "cache-scan-interval",
			Value: "1h",
			Usage: "interval to scan cache-dir to rebuild in-memory index",
		},
		&cli.StringFlag{
			Name:  "cache-expire",
			Value: "0s",
			Usage: "cached blocks not accessed for longer than this option will be automatically evicted (0 means never)",
		},
	})
}

func metaFlags() []cli.Flag {
	return addCategories("META", []cli.Flag{
		&cli.StringFlag{
			Name:  "subdir",
			Usage: "mount a sub-directory as root",
		},
		&cli.StringFlag{
			Name:  "backup-meta",
			Value: "1h",
			Usage: "interval to automatically backup metadata in the object storage (0 means disable backup)",
		},
		&cli.BoolFlag{
			Name:  "backup-skip-trash",
			Usage: "skip files in trash when backup metadata",
		},
		&cli.StringFlag{
			Name:  "heartbeat",
			Value: "12s",
			Usage: "interval to send heartbeat; it's recommended that all clients use the same heartbeat value",
		},
		&cli.BoolFlag{
			Name:  "read-only",
			Usage: "allow lookup/read operations only",
		},
		&cli.BoolFlag{
			Name:  "no-bgjob",
			Usage: "disable background jobs (clean-up, backup, etc.)",
		},
		&cli.StringFlag{
			Name:  "atime-mode",
			Value: "noatime",
			Usage: "when to update atime, supported mode includes: noatime, relatime, strictatime",
		},
		&cli.IntFlag{
			Name:  "skip-dir-nlink",
			Value: 20,
			Usage: "number of retries after which the update of directory nlink will be skipped (used for tkv only, 0 means never)",
		},
		&cli.StringFlag{
			Name:  "skip-dir-mtime",
			Value: "100ms",
			Usage: "skip updating attribute of a directory if the mtime difference is smaller than this value",
		},
		&cli.BoolFlag{
			Name:  "sort-dir",
			Usage: "sort entries within a directory by name",
		},
		&cli.BoolFlag{
			Name:  "fast-statfs",
			Value: false,
			Usage: "Use local counters for statfs instead of querying metadata service",
		},
		&cli.StringFlag{
			Name:  "network-interfaces",
			Usage: "comma-separated list of network interfaces to use for IP discovery (e.g. eth0,en0), empty means all",
		},
	})
}

func clientFlags(defaultEntryCache float64) []cli.Flag {
	return expandFlags(
		metaFlags(),
		metaCacheFlags(defaultEntryCache),
		storageFlags(),
		dataCacheFlags(),
	)
}

func shareInfoFlags() []cli.Flag {
	return addCategories("METRICS", []cli.Flag{
		&cli.StringFlag{
			Name:  "metrics",
			Value: "127.0.0.1:9567",
			Usage: "address to export metrics",
		},
		&cli.StringFlag{
			Name:  "custom-labels",
			Usage: "custom labels for metrics",
		},
		&cli.StringFlag{
			Name:  "consul",
			Value: "127.0.0.1:8500",
			Usage: "consul address to register",
		},
		&cli.BoolFlag{
			Name:  "no-usage-report",
			Usage: "do not send usage report",
		},
	})
}

func metaCacheFlags(defaultEntryCache float64) []cli.Flag {
	return addCategories("META CACHE", []cli.Flag{
		&cli.StringFlag{
			Name:  "attr-cache",
			Value: "1.0s",
			Usage: "attributes cache timeout",
		},
		&cli.StringFlag{
			Name:  "entry-cache",
			Value: fmt.Sprintf("%.1fs", defaultEntryCache),
			Usage: "file entry cache timeout",
		},
		&cli.StringFlag{
			Name:  "dir-entry-cache",
			Value: "1.0s",
			Usage: "dir entry cache timeout",
		},
		&cli.StringFlag{
			Name:  "negative-entry-cache",
			Usage: "cache timeout for negative entry lookups",
		},
		&cli.BoolFlag{
			Name:  "readdir-cache",
			Usage: "enable kernel caching of readdir entries, with timeout controlled by attr-cache flag (require linux kernel 4.20+)",
		},
		&cli.StringFlag{
			Name:  "open-cache",
			Value: "0s",
			Usage: "The cache time to reuse open file without checking update (0 means disable this feature)",
		},
		&cli.Uint64Flag{
			Name:  "open-cache-limit",
			Value: 10000,
			Usage: "max number of open files to cache (soft limit, 0 means unlimited)",
		},
	})
}

func expandFlags(compoundFlags ...[]cli.Flag) []cli.Flag {
	var flags []cli.Flag
	for _, flag := range compoundFlags {
		flags = append(flags, flag...)
	}
	return flags
}
