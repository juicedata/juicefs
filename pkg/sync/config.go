/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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

package sync

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cast"
	"github.com/urfave/cli/v2"
)

type Config struct {
	StorageClass   string
	Start          string
	End            string
	Threads        int
	Update         bool
	ForceUpdate    bool
	Perms          bool
	MaxFailure     int64
	Dry            bool
	DeleteSrc      bool
	DeleteDst      bool
	MatchFullPath  bool
	Dirs           bool
	Exclude        []string
	Include        []string
	Existing       bool
	IgnoreExisting bool
	Links          bool
	Inplace        bool
	Limit          int64
	Manager        string
	Workers        []string
	ManagerAddr    string
	ListThreads    int
	ListDepth      int
	BWLimit        int64
	NoHTTPS        bool
	Verbose        bool
	Quiet          bool
	CheckAll       bool
	CheckNew       bool
	CheckChange    bool
	MaxSize        int64
	MinSize        int64
	MaxAge         time.Duration
	MinAge         time.Duration
	StartTime      time.Time
	EndTime        time.Time
	Env            map[string]string

	FilesFrom string

	rules          []rule
	concurrentList chan int
	Registerer     prometheus.Registerer
}

const JFS_UMASK = "JFS_UMASK"

func envList() []string {
	return []string{
		"ACCESS_KEY",
		"SECRET_KEY",
		"SESSION_TOKEN",

		"MINIO_ACCESS_KEY",
		"MINIO_SECRET_KEY",
		"MINIO_REGION",

		"META_PASSWORD",
		"REDIS_PASSWORD",
		"SENTINEL_PASSWORD",
		"SENTINEL_PASSWORD_FOR_OBJ",

		"AZURE_STORAGE_CONNECTION_STRING",

		"BDCLOUD_DEFAULT_REGION",
		"BDCLOUD_ACCESS_KEY",
		"BDCLOUD_SECRET_KEY",

		"COS_SECRETID",
		"COS_SECRETKEY",

		"EOS_ACCESS_KEY",
		"EOS_SECRET_KEY",
		"EOS_TOKEN",

		"GOOGLE_CLOUD_PROJECT",

		"HADOOP_USER_NAME",
		"HADOOP_SUPER_USER",
		"HADOOP_SUPER_GROUP",
		"HADOOP_CONF_DIR",
		"HADOOP_HOME",
		"KRB5_CONFIG",
		"KRB5CCNAME",
		"KRB5KEYTAB",
		"KRB5KEYTAB_BASE64",
		"KRB5PRINCIPAL",

		"AWS_REGION",
		"AWS_DEFAULT_REGION",

		"HWCLOUD_DEFAULT_REGION",
		"HWCLOUD_ACCESS_KEY",
		"HWCLOUD_SECRET_KEY",

		"ALICLOUD_REGION_ID",
		"ALICLOUD_ACCESS_KEY_ID",
		"ALICLOUD_ACCESS_KEY_SECRET",
		"SECURITY_TOKEN",

		"CEPH_ADMIN_SOCKET",
		"CEPH_LOG_FILE",

		"QINIU_DOMAIN",

		"SCW_ACCESS_KEY",
		"SCW_SECRET_KEY",

		"SSH_PRIVATE_KEY_PATH",
		"SSH_AUTH_SOCK",

		"JFS_RSA_PASSPHRASE",
		"PYROSCOPE_AUTH_TOKEN",
		"DISPLAY_PROGRESSBAR",
		"CGOFUSE_TRACE",
		"JUICEFS_DEBUG",
		"JUICEFS_LOGLEVEL",
	}
}

func NewConfigFromCli(c *cli.Context) *Config {
	if c.Int64("limit") < -1 {
		logger.Fatal("limit should not be less than -1")
	}
	var startTime, endTime time.Time
	var err error
	if c.IsSet("start-time") {
		startTime, err = cast.ToTimeInDefaultLocationE(c.String("start-time"), time.Local)
		if err != nil {
			logger.Fatalf("failed to parse start time: %v", err)
		}
	}
	if c.IsSet("end-time") {
		endTime, err = cast.ToTimeInDefaultLocationE(c.String("end-time"), time.Local)
		if err != nil {
			logger.Fatalf("failed to parse end time: %v", err)
		}
	}
	cfg := &Config{
		StorageClass:   c.String("storage-class"),
		Start:          c.String("start"),
		End:            c.String("end"),
		Threads:        c.Int("threads"),
		ListThreads:    c.Int("list-threads"),
		ListDepth:      c.Int("list-depth"),
		Update:         c.Bool("update"),
		ForceUpdate:    c.Bool("force-update"),
		Perms:          c.Bool("perms"),
		Dirs:           c.Bool("dirs"),
		Dry:            c.Bool("dry"),
		MaxFailure:     c.Int64("max-failure"),
		DeleteSrc:      c.Bool("delete-src"),
		DeleteDst:      c.Bool("delete-dst"),
		Exclude:        c.StringSlice("exclude"),
		Include:        c.StringSlice("include"),
		MatchFullPath:  c.Bool("match-full-path"),
		Existing:       c.Bool("existing"),
		IgnoreExisting: c.Bool("ignore-existing"),
		Links:          c.Bool("links"),
		Inplace:        c.Bool("inplace"),
		Limit:          c.Int64("limit"),
		Workers:        c.StringSlice("worker"),
		ManagerAddr:    c.String("manager-addr"),
		Manager:        c.String("manager"),
		BWLimit:        utils.ParseMbps(c, "bwlimit"),
		NoHTTPS:        c.Bool("no-https"),
		Verbose:        c.Bool("verbose"),
		Quiet:          c.Bool("quiet"),
		CheckAll:       c.Bool("check-all"),
		CheckNew:       c.Bool("check-new"),
		CheckChange:    c.Bool("check-change"),
		MaxSize:        int64(utils.ParseBytes(c, "max-size", 'B')),
		MinSize:        int64(utils.ParseBytes(c, "min-size", 'B')),
		MaxAge:         utils.Duration(c.String("max-age")),
		MinAge:         utils.Duration(c.String("min-age")),
		StartTime:      startTime,
		EndTime:        endTime,
		FilesFrom:      c.String("files-from"),
		Env:            make(map[string]string),
	}
	if !c.IsSet("max-size") {
		cfg.MaxSize = math.MaxInt64
	}
	if cfg.MinSize > cfg.MaxSize {
		logger.Fatal("min-size should not be larger than max-size")
	}
	if cfg.MaxAge > 0 && cfg.MinAge > cfg.MaxAge {
		logger.Fatal("min-age should not be larger than max-age")
	}
	if cfg.Threads <= 0 {
		logger.Warnf("threads should be larger than 0, reset it to 1")
		cfg.Threads = 1
	}
	for _, key := range envList() {
		if os.Getenv(key) != "" {
			cfg.Env[key] = os.Getenv(key)
		}
	}
	// pass all the variable that contains "JFS"
	for _, ekv := range os.Environ() {
		key := strings.Split(ekv, "=")[0]
		if strings.Contains(key, "JFS") {
			cfg.Env[key] = os.Getenv(key)
		}
	}
	// pass umask to workers
	cfg.Env[JFS_UMASK] = strconv.Itoa(utils.GetUmask())

	// workers: set umask for the current process
	if umask := os.Getenv(JFS_UMASK); umask != "" {
		utils.SetUmask(cast.ToInt(umask))
	}

	return cfg
}
