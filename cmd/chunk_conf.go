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

package cmd

import (
	"os"
	"strconv"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

// chunkConfFromFormat maps volume settings to a chunk config without SelfCheck.
func chunkConfFromFormat(format *meta.Format) *chunk.Config {
	return &chunk.Config{
		BlockSize:     format.BlockSize * 1024,
		Compress:      format.Compression,
		HashPrefix:    format.HashPrefix,
		UploadLimit:   format.UploadLimit * 1e6 / 8,
		DownloadLimit: format.DownloadLimit * 1e6 / 8,
	}
}

// newToolChunkConf returns a memory-cache chunk config for tools without client flags (gc, fsck, jfs:// storage).
func newToolChunkConf(format *meta.Format) *chunk.Config {
	conf := chunkConfFromFormat(format)
	conf.GetTimeout = time.Minute
	conf.PutTimeout = time.Minute
	conf.MaxUpload = 50
	conf.MaxDownload = 200
	conf.MaxRetries = 10
	conf.BufferSize = 300 << 20
	conf.SelfCheck(format.UUID)
	return conf
}

// newClientChunkConf returns a chunk config built from client flags, falling back to volume settings.
func newClientChunkConf(c *cli.Context, format *meta.Format) *chunk.Config {
	cm, err := strconv.ParseUint(c.String("cache-mode"), 8, 32)
	if err != nil {
		logger.Warnf("Invalid cache-mode %q, using default value 0600", c.String("cache-mode"))
		cm = 0600
	}
	conf := chunkConfFromFormat(format)

	conf.GetTimeout = utils.Duration(c.String("get-timeout"))
	conf.PutTimeout = utils.Duration(c.String("put-timeout"))
	conf.MaxUpload = c.Int("max-uploads")
	conf.MaxDownload = c.Int("max-downloads")
	conf.MaxStageWrite = c.Int("max-stage-write")
	conf.MaxRetries = c.Int("io-retries")
	conf.Writeback = c.Bool("writeback")
	conf.WritebackThresholdSize = int(utils.ParseBytes(c, "writeback-threshold-size", 'B'))
	conf.Prefetch = c.Int("prefetch")
	conf.BufferSize = utils.ParseBytes(c, "buffer-size", 'M')
	if c.IsSet("upload-limit") {
		conf.UploadLimit = utils.ParseMbps(c, "upload-limit") * 1e6 / 8
	}
	if c.IsSet("download-limit") {
		conf.DownloadLimit = utils.ParseMbps(c, "download-limit") * 1e6 / 8
	}
	conf.UploadDelay = utils.Duration(c.String("upload-delay"))
	conf.UploadHours = c.String("upload-hours")

	conf.CacheDir = c.String("cache-dir")
	conf.CacheSize = utils.ParseBytes(c, "cache-size", 'M')
	conf.CacheItems = c.Int64("cache-items")
	conf.FreeSpace = float32(c.Float64("free-space-ratio"))
	conf.CacheMode = os.FileMode(cm)
	conf.CacheFullBlock = !c.Bool("cache-partial-only")
	conf.CacheLargeWrite = c.Bool("cache-large-write")
	conf.CacheChecksum = c.String("verify-cache-checksum")
	conf.CacheEviction = c.String("cache-eviction")
	conf.CacheScanInterval = utils.Duration(c.String("cache-scan-interval"))
	conf.CacheExpire = utils.Duration(c.String("cache-expire"))
	conf.OSCache = os.Getenv("JFS_DROP_OSCACHE") == ""
	conf.AutoCreate = true

	if c.IsSet("max-readahead") {
		conf.Readahead = int(utils.ParseBytes(c, "max-readahead", 'M'))
	} else {
		conf.Readahead = 8 * conf.BlockSize
	}
	conf.SelfCheck(format.UUID)
	return conf
}
