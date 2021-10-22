/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/urfave/cli/v2"
)

func umountFlags() *cli.Command {
	return &cli.Command{
		Name:      "umount",
		Usage:     "unmount a volume",
		ArgsUsage: "MOUNTPOINT",
		Action:    umount,
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
			cmd = exec.Command("diskutil", "umount", "force", mp)
		} else {
			cmd = exec.Command("diskutil", "umount", mp)
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
	if err != nil {
		log.Print(string(out))
	}
	return err
}

func umount(ctx *cli.Context) error {
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("MOUNTPOINT is needed")
	}
	mp := ctx.Args().Get(0)
	force := ctx.Bool("force")
	return doUmount(mp, force)
}
