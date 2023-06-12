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
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/pkg/errors"
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
			&cli.BoolFlag{
				Name:  "flush",
				Usage: "wait for all staging chunks to be flushed",
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
			_ = os.Mkdir(filepath.Join(mp, ".UMOUNTIT"), 0777)
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
	if ctx.Bool("flush") {
		raw, err := readConfig(mp)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("not a JuiceFS mount point")
			}
			return errors.Wrap(err, "failed to read config")
		}

		var conf vfs.Config
		if err = json.Unmarshal(raw, &conf); err != nil {
			return errors.Wrap(err, "failed to parse config")
		}
		if conf.Chunk.Writeback {
			stagingDir := path.Join(conf.Chunk.CacheDir, "rawstaging")
			if err := waitWritebackComplete(stagingDir); err != nil {
				return err
			}
			defer func() {
				size, _ := fileSizeInDir(stagingDir)
				clearLastLine()
				if size == 0 {
					fmt.Println("\rAll staging chunks are flushed")
				} else {
					fmt.Printf("\r%s staging chunks are not flushed\n", humanize.IBytes(size))
				}
			}()
		}
	}
	return doUmount(mp, ctx.Bool("force"))
}

func waitWritebackComplete(stagingDir string) error {
	lastLeft := uint64(0)
	for {
		_, err := os.Stat(stagingDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errors.Wrap(err, "failed to read staging directory")
		}
		start := time.Now()
		size, err := fileSizeInDir(stagingDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return errors.Wrap(err, "failed to read staging directory")
		}
		if lastLeft == 0 {
			lastLeft = size
		}

		if size == 0 && lastLeft == 0 {
			return nil
		}

		speed := uint64(0)
		if lastLeft > size {
			speed = lastLeft - size
		}

		leftTime := 720 * time.Hour
		if speed != 0 {
			leftTime = time.Duration(size/speed) * time.Second
		}
		clearLastLine()
		fmt.Printf("\r%s staging chunks are being flushed... %s/s, left %s", humanize.IBytes(size), humanize.IBytes(speed), leftTime)
		lastLeft = size
		time.Sleep(time.Second - time.Since(start))
	}
}

func fileSizeInDir(dir string) (uint64, error) {
	var size uint64
	err := filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if d != nil && !d.IsDir() {
			fi, _ := d.Info()
			if fi != nil {
				size += uint64(fi.Size())
			}
		}
		return nil
	})
	return size, err
}

func clearLastLine() {
	fmt.Printf("\r                                                                             ")
}
