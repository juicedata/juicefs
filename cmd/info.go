/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"bytes"
	"encoding/gob"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func infoFlags() *cli.Command {
	return &cli.Command{
		Name:      "info",
		Usage:     "info DIR or FILE",
		ArgsUsage: "DIR or FILE",
		Action:    info,
	}
}

func info(ctx *cli.Context) error {
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	if ctx.Args().Len() < 1 {
		logger.Infof("DIR or FILE is needed")
		return nil
	}

	path := ctx.Args().Get(0)
	p, err := filepath.Abs(path)
	if err != nil {
		logger.Errorf("abs of %s: %s", path, err)
	}
	d := filepath.Dir(p)
	name := filepath.Base(p)
	inode, err := utils.GetFileInode(d)
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", d, err)
	}

	f := openControler(d)
	if f == nil {
		logger.Errorf("%s is not inside JuiceFS", path)
	}

	wb := utils.NewBuffer(8 + 8)
	wb.Put32(meta.Info)
	wb.Put32(8)
	wb.Put64(inode)
	_, err = f.Write(wb.Bytes())
	if err != nil {
		logger.Fatalf("write message: %s", err)
	}

	data := make([]byte, 100)
	n, err := f.Read(data)
	if err != nil {
		logger.Fatalf("read message: %d %s", n, err)
	}
	_ = f.Close()
	var summary meta.Summary
	dec := gob.NewDecoder(bytes.NewReader(data))
	err = dec.Decode(&summary)
	if err != nil {
		logger.Fatalf("decode error: %s", err)
	}

	fmt.Printf("name:\t%s\n", name)
	fmt.Printf("files:\t%d\n", summary.Files)
	fmt.Printf("dirs:\t%d\n", summary.Dirs)
	fmt.Printf("size:\t%d\n", summary.Size)
	fmt.Printf("length:\t%d\n", summary.Length)

	return nil
}
