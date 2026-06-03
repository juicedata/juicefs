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
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/spf13/cast"
	"github.com/urfave/cli/v2"
)

func cmdTier() *cli.Command {
	return &cli.Command{
		Name:            "tier",
		Category:        "ADMIN",
		Usage:           "manage storage tier",
		ArgsUsage:       "META-URL",
		HideHelpCommand: true,
		Description: `
Examples:
$ juicefs tier list redis://localhost
$ juicefs tier set redis://localhost --tier 1 /dir1
$ juicefs tier set redis://localhost --tier 2 /dir1 -r
$ juicefs tier set redis://localhost --tier 3 /file1
$ juicefs tier set redis://localhost --tier 0 /file1
$ juicefs tier restore redis://localhost /file1
$ juicefs tier restore redis://localhost /file1 --days 7
$ juicefs tier restore redis://localhost /dir1`,
		Subcommands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "list storage tiers",
				ArgsUsage: "META-URL",
				Action:    listTier,
			},
			{
				Name:      "set",
				Usage:     "set storage tier to a file or directory",
				ArgsUsage: "META-URL PATH",
				Action:    setTier,
			},
			{
				Name:      "restore",
				Usage:     "restore objects of a file or directory",
				ArgsUsage: "META-URL PATH",
				Action:    objRestore,
			},
		},
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "tier",
				Usage: "tier (0-3, 0 is reserved for default tier)",
				Action: func(ctx *cli.Context, v int) error {
					if !ctx.IsSet("tier") {
						return nil
					}
					if v < 0 || v > 3 {
						return fmt.Errorf("--tier should be between 0 and 3")
					}
					return nil
				},
			},
			&cli.BoolFlag{
				Name:    "recursive",
				Aliases: []string{"r"},
				Usage:   "recursively set storage tier for all files and directories under the target directory",
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "force rewriting objects to the tier's current storage class (useful after --storage-class config changes), even when the tier id is unchanged",
			},
			&cli.IntFlag{
				Name:  "days",
				Value: object.DefaultRestoreDays,
				Usage: "the duration within which the restored object remains in the restored state",
				Action: func(ctx *cli.Context, v int) error {
					if !ctx.IsSet("days") {
						return nil
					}
					if v < 1 {
						return fmt.Errorf("--days should be at least 1")
					}
					return nil
				},
			},
		},
	}
}

func listTier(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), nil)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	results := make([][]string, 0, 1+len(format.Tiers))
	results = append(results, []string{"tier", "storageClass", "tag"})
	for id, t := range format.Tiers {
		results = append(results, []string{fmt.Sprintf("%d", id), t.Sc, t.Tag})
	}
	dataRows := results[1:]
	sort.Slice(dataRows, func(i, j int) bool {
		return cast.ToUint8(dataRows[i][0]) < cast.ToUint8(dataRows[j][0])
	})
	printResult(results, 1, false)
	return nil

}

func setTier(ctx *cli.Context) error {
	setup(ctx, 2)
	removePassword(ctx.Args().Get(0))
	path := ctx.Args().Get(1)
	if !ctx.IsSet("tier") {
		logger.Fatal("missing required flag: --tier")
	}
	id := ctx.Uint("tier")
	m := meta.NewClient(ctx.Args().Get(0), nil)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	newTier, ok := format.Tiers[uint8(id)]
	if !ok {
		logger.Fatalf("unknown tier %d", id)
	}
	var ino meta.Ino
	var attr meta.Attr
	eno := m.Resolve(meta.Background(), meta.RootInode, path, &ino, &attr, true)
	if eno != 0 {
		return eno
	}
	errno := m.GetAttr(meta.Background(), ino, &attr)
	if errno != 0 {
		return errno
	}
	if attr.Typ != meta.TypeFile && attr.Typ != meta.TypeDirectory {
		logger.Fatal("only file and directory are supported to set storage tier")
	}
	oldTier := format.Tiers[attr.Tier]
	logger.Infof("set storage tier of %q from %d(storage-class: %s;tag: %s) to %d(storage-class: %s;tag: %s)", path, attr.Tier, oldTier.Sc, oldTier.Tag, id, newTier.Sc, newTier.Tag)
	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}

	metaFunc := func(ino meta.Ino) error {
		if eno := m.SetAttr(meta.Background(), ino, meta.SetAttrTier, 0, &meta.Attr{Tier: newTier.ID}); eno != 0 {
			return eno
		}
		return nil
	}

	objectFunc := func(key string) error {
		if !ctx.Bool("force") {
			if head, err := blob.Head(context.Background(), key); err == nil {
				if newTier.Sc == head.StorageClass() {
					return nil
				}
			}
		}

		fullPath := format.Name + "/" + key
		ctx := context.WithValue(context.Background(), object.TierKey{}, uint8(id))
		return blob.Copy(ctx, fullPath, fullPath)
	}
	checkFunc := func(ino meta.Ino, oriTier uint8) bool {
		if id == uint(oriTier) && !ctx.Bool("force") {
			logger.Debugf("inode:%d storage tier is already %d, no change needed", ino, oriTier)
			return true
		}
		return false
	}
	switch attr.Typ {
	case meta.TypeFile:
		err = visitEntry(m, format, ino, attr, objectFunc, metaFunc, checkFunc)
	case meta.TypeDirectory:
		if ctx.Bool("recursive") {
			if err = visitDir(m, format, ino, ctx.Bool("recursive"), objectFunc, metaFunc, checkFunc); err != nil {
				return err
			}
		}
		if err = metaFunc(ino); err != nil {
			return fmt.Errorf("set tier for inode %d tier:%d failed: %w", ino, newTier.ID, err)
		}

	default:
		logger.Fatal("only file and directory are supported to set storage tier")
	}
	return err
}

func objRestore(ctx *cli.Context) error {
	setup(ctx, 2)
	removePassword(ctx.Args().Get(0))
	path := ctx.Args().Get(1)
	days := ctx.Int("days")
	m := meta.NewClient(ctx.Args().Get(0), nil)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	var ino meta.Ino
	var attr meta.Attr
	eno := m.Resolve(meta.Background(), meta.RootInode, path, &ino, &attr, true)
	if eno != 0 {
		return eno
	}
	errno := m.GetAttr(meta.Background(), ino, &attr)
	if errno != 0 {
		return errno
	}
	if attr.Typ != meta.TypeFile && attr.Typ != meta.TypeDirectory {
		logger.Fatalf("only file and directory are supported to set storage tier")
	}
	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}

	objectFunc := func(key string) error {
		return blob.Restore(context.Background(), key, int32(days))
	}
	if attr.Typ == meta.TypeFile {
		err = visitEntry(m, format, ino, attr, objectFunc, nil, nil)
	}
	if attr.Typ == meta.TypeDirectory {
		err = visitDir(m, format, ino, ctx.Bool("recursive"), objectFunc, nil, nil)
	}
	return err
}

func visitDir(m meta.Meta, format *meta.Format, ino meta.Ino, recursive bool, objectFunc func(key string) error, metaFunc func(ino meta.Ino) error, checkFunc func(ino meta.Ino, oriTier uint8) bool) error {
	handler, errno := m.NewDirHandler(meta.Background(), ino, true, nil)
	if errno != 0 {
		return errno
	}
	offset := 0
	for {
		batchEntries, batchEno := handler.List(meta.Background(), offset)
		if batchEno != 0 {
			return batchEno
		}
		if len(batchEntries) == 0 {
			break
		}
		for _, e := range batchEntries {
			if string(e.Name) == "." || string(e.Name) == ".." {
				continue
			}
			if e.Attr.Typ == meta.TypeFile {
				err := visitEntry(m, format, e.Inode, *e.Attr, objectFunc, metaFunc, checkFunc)
				if err != nil {
					return err
				}
			}
			if e.Attr.Typ == meta.TypeDirectory {
				if recursive {
					if err := visitDir(m, format, e.Inode, recursive, objectFunc, metaFunc, checkFunc); err != nil {
						return err
					}
				}
				if metaFunc != nil {
					if err := metaFunc(e.Inode); err != nil {
						return err
					}
				}
			}
		}
		offset += len(batchEntries)
	}
	return nil
}

func getObjKeys(m meta.Meta, format *meta.Format, ino meta.Ino, length uint64) []string {
	var objs []string
	for index := uint64(0); index*meta.ChunkSize < length; index++ {
		var cs []meta.Slice
		if eno := m.Read(meta.Background(), ino, uint32(index), &cs); eno == 0 {
			for _, c := range cs {
				for _, o := range vfs.CalcObjects(*format, c.Id, c.Size, c.Off, c.Len) {
					k := strings.TrimPrefix(o.Key, format.Name+"/")
					objs = append(objs, k)
				}
			}
		} else {
			logger.Errorf("read chunk %d of ino %d failed: %s", index, ino, eno)
		}
	}
	return objs
}
func visitEntry(m meta.Meta, format *meta.Format, ino meta.Ino, attr meta.Attr, objectFunc func(key string) error, metaFunc func(ino meta.Ino) error, checkFunc func(ino meta.Ino, oriTier uint8) bool) error {
	if checkFunc != nil && checkFunc(ino, attr.Tier) {
		return nil
	}
	objs := getObjKeys(m, format, ino, attr.Length)
	if objectFunc != nil {
		for _, key := range objs {
			if key != "" {
				err := objectFunc(key)
				if err != nil {
					return fmt.Errorf("apply object action failed in inode:%d key:%v: err:%s", ino, key, err)
				}
			}
		}
	}
	if metaFunc != nil {
		if err := metaFunc(ino); err != nil {
			return fmt.Errorf("set tier for inode %d failed: %w", ino, err)
		}
	}
	return nil
}
