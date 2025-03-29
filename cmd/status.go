/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"

	"github.com/urfave/cli/v2"
)

func cmdStatus() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Action:    status,
		Category:  "INSPECTOR",
		Usage:     "Show status of a volume",
		ArgsUsage: "META-URL",
		Description: `
It shows basic setting of the target volume, and a list of active sessions (including mount, SDK,
S3-gateway and WebDAV) that are connected with the metadata engine.

NOTE: Read-only session is not listed since it cannot register itself in the metadata.

Examples:
$ juicefs status redis://localhost`,
		Flags: []cli.Flag{
			&cli.Uint64Flag{
				Name:    "session",
				Aliases: []string{"s"},
				Usage:   "show detailed information (sustained inodes, locks) of the specified session (sid)",
			},
			&cli.BoolFlag{
				Name:    "more",
				Aliases: []string{"m"},
				Usage:   "show more statistic information, may take a long time",
			},
		},
	}
}

func printJson(data []byte) {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		logger.Fatalf("format JSON: %s", err)
	}
	formatted := out.Bytes()
	fmt.Println(string(formatted))
}

func initForStatus(c *cli.Context, metaUrl string) *fs.FileSystem {
	metaConf := getMetaConf(c, "status", false)
	m := meta.NewClient(metaUrl, metaConf)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	if st := m.Chroot(meta.Background(), metaConf.Subdir); st != 0 {
		logger.Fatalf("Chroot to %s: %s", metaConf.Subdir, st)
	}

	blob, err := NewReloadableStorage(format, m, updateFormat(c))
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}

	chunkConf := getChunkConf(c, format)
	store := chunk.NewCachedStore(blob, *chunkConf, nil)
	conf := getVfsConf(c, metaConf, format, chunkConf)
	conf.AccessLog = c.String("access-log")
	conf.AttrTimeout = utils.Duration(c.String("attr-cache"))
	conf.EntryTimeout = utils.Duration(c.String("entry-cache"))
	conf.DirEntryTimeout = utils.Duration(c.String("dir-entry-cache"))
	jfs, err := fs.NewFileSystem(conf, m, store)
	if err != nil {
		logger.Fatalf("initialize failed: %s", err)
	}
	return jfs
}

func status(ctx *cli.Context) error {
	setup(ctx, 1)
	metaUrl := ctx.Args().Get(0)
	removePassword(metaUrl)

	jfs := initForStatus(ctx, metaUrl)
	sessionId := ctx.Uint64("session")
	context := meta.NewContext(1, uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	output, err := jfs.Status(context, ctx.Bool("more"), sessionId)
	if err != nil {
		logger.Fatalf("status: %s", err)
	}

	printJson(output)
	return nil
}
