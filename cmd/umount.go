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

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/urfave/cli/v2"
)

func cmdUmount() *cli.Command {
	return &cli.Command{
		Name:      "umount",
		Action:    umount,
		Category:  "SERVICE",
		Usage:     "Unmount a volume",
		ArgsUsage: "MOUNTPOINT",
		Description: `
Examples:
$ juicefs umount /mnt/jfs`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "unmount a busy mount point by force",
			},
		},
	}
}

func doUmount(mp string, force bool) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if force {
			cmd = exec.Command("umount", "-f", mp)
		} else {
			cmd = exec.Command("umount", mp)
		}
	case "linux":
		if _, err := exec.LookPath("fusermount"); err == nil {
			if force {
				cmd = exec.Command("fusermount", "-uz", mp)
			} else {
				cmd = exec.Command("fusermount", "-u", mp)
			}
		} else {
			if force {
				cmd = exec.Command("umount", "-l", mp)
			} else {
				cmd = exec.Command("umount", mp)
			}
		}
	case "windows":
		if !force {
			_ = os.Mkdir(filepath.Join(mp, ".UMOUNTIT"), 0755)
			return nil
		} else {
			cmd = exec.Command("taskkill", "/IM", "juicefs.exe", "/F")
		}
	default:
		return fmt.Errorf("OS %s is not supported", runtime.GOOS)
	}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) != 0 {
		err = errors.New(string(out))
	}
	return err
}

func umount(ctx *cli.Context) error {
	setup(ctx, 1)
	mp := ctx.Args().Get(0)
	force := ctx.Bool("force")
	return doUmount(mp, force)
}
