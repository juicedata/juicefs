/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func cmdTier() *cli.Command {
	return &cli.Command{
		Name:            "tier",
		Category:        "ADMIN",
		Usage:           "Manage storage classes",
		ArgsUsage:       "",
		HideHelpCommand: true,
		Description:     `juicefs tier set-storage-class redis://localhost /dir1/ IA -r`,
		Subcommands: []*cli.Command{
			{
				Name:      "set-storage-class",
				Usage:     "",
				ArgsUsage: "",
				Action:    tier,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "recursive",
						Aliases: []string{"r"},
						Usage:   "",
					},
				},
			},
			{
				Name:      "restore",
				Usage:     "",
				ArgsUsage: "",
				Action:    tier,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "recursive",
						Aliases: []string{"r"},
						Usage:   "",
					},
				},
			},
		},
	}
}

var c = meta.NewContext(0, 0, []uint32{0})

func tier(ctx *cli.Context) error {
	setup(ctx, 3)
	removePassword(ctx.Args().Get(0))
	//path := ctx.Args().Get(1)
	scStr := ctx.Args().Get(2)
	m := meta.NewClient(ctx.Args().Get(0), nil)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	scStr = "STANDARD_IA"
	var ino meta.Ino = 1027
	var attr meta.Attr
	//eno := m.Resolve(c, 1, path, &ino, &attr)
	//if eno != 0 {
	//	return eno
	//}
	errno := m.GetAttr(c, ino, &attr)
	if errno != 0 {
		return errno
	}
	logger.Infof("current storage class of: %d", attr.Tier.GetStorageClassID())
	scInfo, ok := object.ScInfo.GetByName(format.Storage, scStr)
	if !ok {
		return fmt.Errorf("invalid storage class: %s", scStr)
	}
	logger.Infof("new storage class of: %s->%d", scInfo.Name, scInfo.Id)

	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	//get, err := blob.Get(context.Background(), "chunks/0/0/1_0_3", 0, -1)
	//if err != nil {
	//	logger.Fatalf("get object: %s", err)
	//}

	//fmt.Println("get object:", get)

	var metaFunc func(ino meta.Ino) error
	var objectFunc func(key string) error
	if ctx.Command.Name == "set-storage-class" {
		metaFunc = func(ino meta.Ino) error {
			var t meta.TierInfo
			err := t.SetStorageClass(scInfo.Id)
			if err != nil {
				return err
			}
			return m.SetAttr(c, ino, meta.SetAttrTier, 0, &meta.Attr{Tier: t})
		}
		objectFunc = func(key string) error {
			fullPath := format.Name + "/" + key
			return blob.Copy(context.Background(), fullPath, fullPath, object.WithStorageClass(scInfo.Id))
		}

	} else if ctx.Command.Name == "restore" {
		objectFunc = func(key string) error {
			return blob.Restore(context.Background(), key)
		}
	}
	if attr.Typ == meta.TypeFile || attr.Typ == meta.TypeDirectory {
		err = visitEntry(m, format, objectFunc, metaFunc, ino, attr.Length)
		if err != nil {
			return err
		}
	}
	if attr.Typ == meta.TypeDirectory {
		err := visitDir(m, format, objectFunc, metaFunc, ino, ctx.Bool("recursive"))
		if err != nil {
			return err
		}
	}
	return nil
}

func visitDir(m meta.Meta, format *meta.Format, objectFunc func(key string) error, metaFunc func(ino meta.Ino) error, ino meta.Ino, recursive bool) error {
	handler, errno := m.NewDirHandler(c, ino, true, nil)
	if errno != 0 {
		return errno
	}
	offset := 0
	for {
		batchEntries, batchEno := handler.List(c, offset)
		if batchEno != 0 {
			return batchEno
		}
		if len(batchEntries) == 0 {
			break
		}
		for _, e := range batchEntries {
			if e.Attr.Typ == meta.TypeDirectory || e.Attr.Typ == meta.TypeFile {
				err := visitEntry(m, format, objectFunc, metaFunc, e.Inode, e.Attr.Length)
				if err != nil {
					return err
				}
			}
			if e.Attr.Typ == meta.TypeDirectory && recursive {
				err := visitDir(m, format, objectFunc, metaFunc, e.Inode, recursive)
				if err != nil {
					return err
				}
			}
		}
		offset += len(batchEntries)
	}
	return nil
}

func getObjKeys(m meta.Meta, format *meta.Format, ino meta.Ino, length uint64) []string {
	var objs []string
	for indx := uint64(0); indx*meta.ChunkSize < length; indx++ {
		var cs []meta.Slice
		_ = m.Read(c, ino, uint32(indx), &cs)
		for _, c := range cs {
			for _, o := range vfs.CalcObjects(*format, c.Id, c.Size, c.Off, c.Len) {
				k := strings.TrimPrefix(o.Key, format.Name+"/")
				objs = append(objs, k)
			}
		}
	}
	return objs
}

func visitEntry(m meta.Meta, format *meta.Format, objectFunc func(key string) error, metaFunc func(ino meta.Ino) error, ino meta.Ino, length uint64) error {
	objs := getObjKeys(m, format, ino, length)
	if objectFunc != nil {
		for _, obj := range objs {
			err := objectFunc(obj)
			if err != nil {
				logger.Errorf("copy %s: %s", obj, err)
				return err
			}
		}
	}
	if metaFunc != nil {
		return metaFunc(ino)
	}
	return nil
}
