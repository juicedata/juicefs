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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"

	"github.com/urfave/cli/v2"
)

func cmdClone() *cli.Command {
	return &cli.Command{
		Name:   "clone",
		Action: clone,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "cp",
				Usage: "create files with current uid,gid,umask (like 'cp')"},
		},
		Category:    "TOOL",
		Description: `This command can clone a file or directory without copying the underlying data.`,
	}
}
func clone(ctx *cli.Context) error {
	setup(ctx, 2)
	if runtime.GOOS == "windows" {
		logger.Infof("Windows is not supported")
		return nil
	}
	srcPath := ctx.Args().Get(0)
	srcAbsPath, err := filepath.Abs(srcPath)
	if err != nil {
		return fmt.Errorf("abs of %s: %s", srcPath, err)
	}
	srcParent := filepath.Dir(srcAbsPath)
	srcIno, err := utils.GetFileInode(srcPath)
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", srcPath, err)
	}
	dst := ctx.Args().Get(1)
	if strings.HasSuffix(dst, "/") {
		dst = filepath.Join(dst, filepath.Base(srcPath))
	}
	if _, err := os.Stat(dst); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("%s already exists", dst)
	}
	dstAbsPath, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("abs of %s: %s", dstAbsPath, err)
	}
	dstParent := filepath.Dir(dstAbsPath)
	dstName := filepath.Base(dstAbsPath)
	dstParentIno, err := utils.GetFileInode(dstParent)
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", dstParent, err)
	}
	var smode uint8
	var umask int
	if ctx.Bool("cp") {
		smode |= meta.CLONE_MODE_CPLIKE_ATTR
		umask = syscall.Umask(0)
		syscall.Umask(umask)
	}
	wb := utils.NewBuffer(4 + 4 + 8 + 8 + 1 + uint32(len(dstName)) + 4 + 4 + 2 + 1)
	wb.Put32(meta.Clone)
	wb.Put32(8 + +8 + 1 + uint32(len(dstName)) + 4 + 4 + 2 + 1)
	wb.Put64(srcIno)
	wb.Put64(dstParentIno)
	wb.Put8(uint8(len(dstName)))
	wb.Put([]byte(dstName))
	wb.Put32(uint32(os.Getuid()))
	wb.Put32(uint32(os.Getgid()))
	wb.Put16(uint16(umask))
	wb.Put8(smode)
	f := openController(srcParent)
	if f == nil {
		logger.Errorf("%s is not inside JuiceFS", srcPath)
	}
	_, err = f.Write(wb.Bytes())
	if err != nil {
		logger.Fatalf("write message: %s", err)
	}
	return nil
}
