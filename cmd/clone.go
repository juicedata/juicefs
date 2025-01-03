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
	"path"
	"path/filepath"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdClone() *cli.Command {
	return &cli.Command{
		Name:      "clone",
		Action:    clone,
		Usage:     "clone a file or directory without copying the underlying data",
		ArgsUsage: "SRC DST",
		Category:  "TOOL",
		Description: `
This command can clone a file or directory without copying the underlying data,similar to the cp command but very fast.
Examples:
# Clone a file
$ juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
$ juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# Clone with preserving the uid, gid, and mode of the file
$ juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "preserve",
				Aliases: []string{"p"},
				Usage:   "preserve the uid, gid, and mode of the file"},
		},
	}
}

func clone(ctx *cli.Context) error {
	setup(ctx, 2)
	srcPath := ctx.Args().Get(0)
	srcAbsPath, err := filepath.Abs(srcPath)
	if err != nil {
		return fmt.Errorf("abs of %s: %s", srcPath, err)
	}
	srcIno, err := utils.GetFileInode(srcPath)
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", srcPath, err)
	}
	srcParentIno, err := utils.GetFileInode(filepath.Dir(srcAbsPath))
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", filepath.Dir(srcAbsPath), err)
	}
	dst := ctx.Args().Get(1)
	if strings.HasSuffix(dst, string(filepath.Separator)) {
		dst = filepath.Join(dst, filepath.Base(srcPath))
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %s", dst, err)
	}
	dstAbsPath, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("abs of %s: %s", dst, err)
	}

	srcMp, err := findMountpoint(srcAbsPath)
	if err != nil {
		return err
	}
	dstMp, err := findMountpoint(filepath.Dir(dstAbsPath))
	if err != nil {
		return err
	}
	if srcMp != dstMp {
		return fmt.Errorf("the clone DST path should be at the same mount point as the SRC path")
	}
	if strings.HasPrefix(dstAbsPath, path.Clean(srcAbsPath)+"/") {
		return fmt.Errorf("the clone DST path should not be under the SRC path")
	}

	dstParent := filepath.Dir(dstAbsPath)
	dstName := filepath.Base(dstAbsPath)
	dstParentIno, err := utils.GetFileInode(dstParent)
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", dstParent, err)
	}
	var cmode uint8
	umask := utils.GetUmask()
	if ctx.Bool("preserve") {
		cmode |= meta.CLONE_MODE_PRESERVE_ATTR
	}
	headerSize := 4 + 4
	contentSize := 8 + 8 + 8 + 1 + uint32(len(dstName)) + 2 + 1
	wb := utils.NewBuffer(uint32(headerSize) + contentSize)
	wb.Put32(meta.Clone)
	wb.Put32(contentSize)
	wb.Put64(srcIno)
	wb.Put64(srcParentIno)
	wb.Put64(dstParentIno)
	wb.Put8(uint8(len(dstName)))
	wb.Put([]byte(dstName))
	wb.Put16(uint16(umask))
	wb.Put8(cmode)
	f, err := openController(srcMp)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(wb.Bytes()); err != nil {
		return fmt.Errorf("write message: %s", err)
	}

	progress := utils.NewProgress(false)
	defer progress.Done()
	bar := progress.AddCountBar("Cloning entries", 0)
	if _, errno := readProgress(f, func(count uint64, total uint64) {
		bar.SetTotal(int64(total))
		bar.SetCurrent(int64(count))
	}); errno != 0 {
		return fmt.Errorf("clone failed: %v", errno)
	}
	return nil
}

func findMountpoint(fpath string) (string, error) {
	for p := fpath; p != "/"; p = filepath.Dir(p) {
		inode, err := utils.GetFileInode(p)
		if err != nil {
			return "", fmt.Errorf("get inode of %s: %s", p, err)
		}
		if inode == uint64(meta.RootInode) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not inside JuiceFS", fpath)
}
