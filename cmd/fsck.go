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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"

	"github.com/urfave/cli/v2"
)

func cmdFsck() *cli.Command {
	return &cli.Command{
		Name:      "fsck",
		Action:    fsck,
		Category:  "ADMIN",
		Usage:     "Check consistency of a volume",
		ArgsUsage: "META-URL",
		Description: `
It scans all objects in data storage and slices in metadata, comparing them to see if there is any
lost object or broken file.

Examples:
$ juicefs fsck redis://localhost

# Repair broken directories
$ juicefs fsck redis://localhost --path /d1/d2 --repair

# recursively check
$ juicefs fsck redis://localhost --path /d1/d2 --recursive`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "check",
				Value: "both",
				Usage: `check "meta", "data" or "both", default is "both"`,
			},
			&cli.StringFlag{
				Name:  "path",
				Value: "/",
				Usage: "absolute path within JuiceFS to check",
			},
			&cli.BoolFlag{
				Name:    "recursive",
				Aliases: []string{"r"},
				Usage:   "recursively check or repair",
			},
			&cli.BoolFlag{
				Name:  "repair",
				Usage: "repair specified path if it's broken (Note: repair attr and stat only)",
			},
			&cli.BoolFlag{
				Name:  "sync-dir-stat",
				Usage: "sync stat of all directories, even if they are existed and not broken (NOTE: it may take a long time for huge trees)",
			},
			&cli.UintFlag{
				Name:  "threads",
				Value: 20,
				Usage: "number of concurrent threads",
			},
		},
	}
}

const (
	checkMetaFlag = 1 << iota
	checkDataFlag
)

func fsck(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))

	var flag int
	switch ctx.String("check") {
	case "meta":
		flag = checkMetaFlag
	case "data":
		flag = checkDataFlag
	case "both":
		flag = checkMetaFlag | checkDataFlag
	default:
		logger.Fatalf("invalid check flag: %s", ctx.String("check"))
	}

	if ctx.Bool("repair") && ctx.String("path") == "" {
		logger.Fatalf("Please provide the path to repair with `--path` option")
	}

	path := ctx.String("path")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		logger.Fatalf("File path should be the absolute path within JuiceFS")
	}
	defer logger.Infof("Check %s [%s] finished, check log for more details", path, ctx.String("check"))

	m := meta.NewClient(ctx.Args().Get(0), nil)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	if ctx.Bool("sync-dir-stat") && !format.DirStats {
		logger.Warn("dir stats is disabled, flag '--sync-dir-stat' will be ignored")
	}

	threads := ctx.Uint("threads")
	if threads == 0 {
		threads = 20
		logger.Warnf("threads number set to %d", threads)
	}

	var c = meta.NewContext(0, 0, []uint32{0})
	progress := utils.NewProgress(false)

	var metaChecker func(meta.Ino, string, *meta.Attr) error
	if flag&checkMetaFlag != 0 {
		metaChecker = m.GetMetaChecker(c, ctx.Bool("repair"), ctx.Bool("sync-dir-stat"))
	}

	var dataChecker func(meta.Ino, []meta.Slice) error
	brokens := make(map[meta.Ino]string)
	if flag&checkDataFlag != 0 {
		sliceCBar := progress.AddCountBar("Scanned slices", 0)
		sliceBSpin := progress.AddByteSpinner("Scanned slices")
		lostDSpin := progress.AddDoubleSpinner("Lost blocks")
		defer func() {
			if progress.Quiet {
				logger.Infof("Used by %d slices (%d bytes)", sliceCBar.Current(), sliceBSpin.Current())
			}
			if lc, lb := lostDSpin.Current(); lc > 0 {
				msg := fmt.Sprintf("%d objects are lost (%d bytes), %d broken files:\n", lc, lb, len(brokens))
				msg += fmt.Sprintf("%13s: PATH\n", "INODE")
				var fileList []string
				for i, p := range brokens {
					fileList = append(fileList, fmt.Sprintf("%13d: %s", i, p))
				}
				sort.Strings(fileList)
				msg += strings.Join(fileList, "\n")
				logger.Error(msg)
			}
		}()

		chunkConf := chunk.Config{
			BlockSize:  format.BlockSize * 1024,
			Compress:   format.Compression,
			GetTimeout: time.Second * 60,
			PutTimeout: time.Second * 60,
			MaxUpload:  20,
			BufferSize: 300 << 20,
			CacheDir:   "memory",
		}

		blob, err := createStorage(*format)
		if err != nil {
			logger.Fatalf("object storage: %s", err)
		}
		logger.Infof("Data use %s", blob)
		blob = object.WithPrefix(blob, "chunks/")

		var blocks = make(map[string]int64)
		if path != "/" {
			objs, err := osync.ListAll(blob, "", "", "", true)
			if err != nil {
				logger.Fatalf("list all blocks: %s", err)
			}

			// Find all blocks in object storage
			blockDSpin := progress.AddDoubleSpinner("Found blocks")
			for obj := range objs {
				if obj == nil {
					break // failed listing
				}
				if obj.IsDir() {
					continue
				}

				parts := strings.Split(obj.Key(), "/")
				if len(parts) != 3 {
					continue
				}
				name := parts[2]
				blocks[name] = obj.Size()
				blockDSpin.IncrInt64(obj.Size())
			}
			blockDSpin.Done()
		}

		dataChecker = func(inode meta.Ino, ss []meta.Slice) error {
			sliceCBar.IncrTotal(int64(len(ss)))
			for _, s := range ss {
				n := (s.Size - 1) / uint32(chunkConf.BlockSize)
				for i := uint32(0); i <= n; i++ {
					sz := chunkConf.BlockSize
					if i == n {
						sz = int(s.Size) - int(i)*chunkConf.BlockSize
					}
					key := fmt.Sprintf("%d_%d_%d", s.Id, i, sz)
					if _, ok := blocks[key]; !ok {
						var objKey string
						if format.HashPrefix {
							objKey = fmt.Sprintf("%02X/%v/%s", s.Id%256, s.Id/1000/1000, key)
						} else {
							objKey = fmt.Sprintf("%v/%v/%s", s.Id/1000/1000, s.Id/1000, key)
						}
						if _, err := blob.Head(objKey); err != nil {
							if _, ok := brokens[inode]; !ok {
								if ps := m.GetPaths(meta.Background, inode); len(ps) > 0 {
									brokens[inode] = ps[0]
								} else {
									brokens[inode] = fmt.Sprintf("inode:%d", inode)
								}
							}
							logger.Errorf("can't find block %s for file %s: %s", objKey, brokens[inode], err)
							lostDSpin.IncrInt64(int64(sz))
						}
					}
				}
				sliceCBar.Increment()
				sliceBSpin.IncrInt64(int64(s.Size))
			}
			return nil
		}
	}

	err = m.Check(c, path, ctx.Bool("recursive"), threads, metaChecker, dataChecker, progress)
	progress.Done()
	return err
}
