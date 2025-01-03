/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"math"
	"path/filepath"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdCompact() *cli.Command {
	return &cli.Command{
		Name:      "compact",
		Action:    compact,
		Category:  "TOOL",
		Usage:     "Trigger compaction of chunks",
		ArgsUsage: "PATH...",
		Description: `
 Examples:
 # compact with path
 $ juicefs compact /mnt/jfs/foo
 `,
		Flags: []cli.Flag{
			&cli.UintFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   10,
				Usage:   "compact concurrency",
			},
		},
	}
}

func compact(ctx *cli.Context) error {
	setup(ctx, 1)

	coCnt := ctx.Int("threads")
	if coCnt <= 0 {
		logger.Warn("threads should be > 0")
		coCnt = 1
	} else if coCnt >= math.MaxUint16 {
		logger.Warn("threads should be < MaxUint16")
		coCnt = math.MaxUint16
	}

	paths := ctx.Args().Slice()
	for i := 0; i < len(paths); i++ {
		path, err := filepath.Abs(paths[i])
		if err != nil {
			logger.Fatalf("get absolute path of %s error: %v", paths[i], err)
		}

		inodeNo, err := utils.GetFileInode(path)
		if err != nil {
			logger.Errorf("lookup inode for %s error: %v", path, err)
			continue
		}
		inode := meta.Ino(inodeNo)

		if !inode.IsValid() {
			logger.Fatalf("inode numbe %d not valid", inode)
		}

		if err = doCompact(inode, path, uint16(coCnt)); err != nil {
			logger.Error(err)
		}
	}
	return nil
}

func doCompact(inode meta.Ino, path string, coCnt uint16) error {
	f, err := openController(path)
	if err != nil {
		return fmt.Errorf("open control file for [%d:%s]: %w", inode, path, err)
	}
	defer f.Close()

	headerLen, bodyLen := uint32(8), uint32(8+2)
	wb := utils.NewBuffer(headerLen + bodyLen)
	wb.Put32(meta.CompactPath)
	wb.Put32(bodyLen)
	wb.Put64(uint64(inode))
	wb.Put16(coCnt)

	_, err = f.Write(wb.Bytes())
	if err != nil {
		logger.Fatalf("write message: %s", err)
	}

	progress := utils.NewProgress(false)
	bar := progress.AddCountBar("Compacted chunks", 0)
	_, errno := readProgress(f, func(totalChunks, currChunks uint64) {
		bar.SetTotal(int64(totalChunks))
		bar.SetCurrent(int64(currChunks))
	})

	bar.Done()
	progress.Done()

	if errno == syscall.EINVAL {
		logger.Fatalf("compact is not supported, please upgrade and mount again")
	}
	if errno != 0 {
		return fmt.Errorf("compact [%d:%s] error: %s", inode, path, errno)
	}

	logger.Infof("compact [%d:%s] success.", inode, path)
	return nil
}
