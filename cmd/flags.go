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
	"os"
	"path"
	"runtime"
	"strconv"
	"time"

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
			Usage: "customized storage type (e.g. s3, gcs, oss, cos) to access object store",
		},
		&cli.StringFlag{
			Name:  "bucket",
			Usage: "customized endpoint to access object store",
		},
		&cli.StringFlag{
			Name:  "storage-class",
			Usage: "the storage class for data written by current client",
		},
		&cli.IntFlag{
			Name:  "get-timeout",
			Value: 60,
			Usage: "the max number of seconds to download an object",
		},
		&cli.IntFlag{
			Name:  "put-timeout",
			Value: 60,
			Usage: "the max number of seconds to upload an object",
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
			Name:  "max-deletes",
			Value: 10,
			Usage: "number of threads to delete objects",
		},
		&cli.Int64Flag{
			Name:  "upload-limit",
			Usage: "bandwidth limit for upload in Mbps",
		},
		&cli.Int64Flag{
			Name:  "download-limit",
			Usage: "bandwidth limit for download in Mbps",
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
		fallthrough
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	}
	return addCategories("DATA CACHE", []cli.Flag{
		&cli.IntFlag{
			Name:  "buffer-size",
			Value: 300,
			Usage: "total read/write buffering in MB",
		},
		&cli.IntFlag{
			Name:  "prefetch",
			Value: 1,
			Usage: "prefetch N blocks in parallel",
		},
		&cli.BoolFlag{
			Name:  "writeback",
			Usage: "upload objects in background",
		},
		&cli.StringFlag{
			Name:  "upload-delay",
			Value: "0",
			Usage: "delayed duration (in seconds) for uploading objects",
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
		&cli.IntFlag{
			Name:  "cache-size",
			Value: 100 << 10,
			Usage: "size of cached object for read in MiB",
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
		&cli.StringFlag{
			Name:  "verify-cache-checksum",
			Value: "full",
			Usage: "checksum level (none, full, shrink, extend)",
		},
		&cli.StringFlag{
			Name:  "cache-eviction",
			Value: "2-random",
			Usage: "cache eviction policy (none or 2-random)",
		},
		&cli.StringFlag{
			Name:  "cache-scan-interval",
			Value: "3600",
			Usage: "interval (in seconds) to scan cache-dir to rebuild in-memory index",
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
			Value: "3600",
			Usage: "interval (in seconds) to automatically backup metadata in the object storage (0 means disable backup)",
		},
		&cli.StringFlag{
			Name:  "heartbeat",
			Value: "12",
			Usage: "interval (in seconds) to send heartbeat; it's recommended that all clients use the same heartbeat value",
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
		&cli.Float64Flag{
			Name:  "attr-cache",
			Value: 1.0,
			Usage: "attributes cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "entry-cache",
			Value: defaultEntryCache,
			Usage: "file entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "dir-entry-cache",
			Value: 1.0,
			Usage: "dir entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "open-cache",
			Value: 0.0,
			Usage: "The seconds to reuse open file without checking update (0 means disable this feature)",
		},
		&cli.IntFlag{
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

func duration(s string) time.Duration {
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Second * time.Duration(v)
	}
	if v, err := time.ParseDuration(s); err == nil {
		return v
	}
	return 0
}
