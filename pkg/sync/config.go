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
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

type Config struct {
	Start       string
	End         string
	Threads     int
	HTTPPort    int
	Update      bool
	ForceUpdate bool
	Perms       bool
	Dry         bool
	DeleteSrc   bool
	DeleteDst   bool
	Dirs        bool
	Exclude     []string
	Include     []string
	Links       bool
	Limit       int64
	Manager     string
	Workers     []string
	BWLimit     int
	NoHTTPS     bool
	Verbose     bool
	Quiet       bool
	CheckAll    bool
	CheckNew    bool
	Env         map[string]string
}

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
		"KRB5_CONFIG",
		"KRB5CCNAME",

		"AWS_REGION",
		"AWS_DEFAULT_REGION",

		"HWCLOUD_DEFAULT_REGION",
		"HWCLOUD_ACCESS_KEY",
		"HWCLOUD_SECRET_KEY",

		"ALICLOUD_REGION_ID",
		"ALICLOUD_ACCESS_KEY_ID",
		"ALICLOUD_ACCESS_KEY_SECRET",
		"SECURITY_TOKEN",

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

	cfg := &Config{
		Start:       c.String("start"),
		End:         c.String("end"),
		Threads:     c.Int("threads"),
		Update:      c.Bool("update"),
		ForceUpdate: c.Bool("force-update"),
		Perms:       c.Bool("perms"),
		Dirs:        c.Bool("dirs"),
		Dry:         c.Bool("dry"),
		DeleteSrc:   c.Bool("delete-src"),
		DeleteDst:   c.Bool("delete-dst"),
		Exclude:     c.StringSlice("exclude"),
		Include:     c.StringSlice("include"),
		Links:       c.Bool("links"),
		Limit:       c.Int64("limit"),
		Workers:     c.StringSlice("worker"),
		Manager:     c.String("manager"),
		BWLimit:     c.Int("bwlimit"),
		NoHTTPS:     c.Bool("no-https"),
		Verbose:     c.Bool("verbose"),
		Quiet:       c.Bool("quiet"),
		CheckAll:    c.Bool("check-all"),
		CheckNew:    c.Bool("check-new"),
		Env:         make(map[string]string),
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

	return cfg
}
