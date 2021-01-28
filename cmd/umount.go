package main

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"

	"github.com/urfave/cli/v2"
)

func umountFlags() *cli.Command {
	return &cli.Command{
		Name:      "umount",
		Usage:     "umount a volume",
		ArgsUsage: "MOUNTPOINT",
		Action:    umount,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "umount a busy mountpoint by force",
			},
		},
	}
}

func umount(ctx *cli.Context) error {
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("MOUNTPOINT is needed")
	}
	mp := ctx.Args().Get(0)
	force := ctx.Bool("force")

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if force {
			cmd = exec.Command("diskutil", "umount", "force", mp)
		} else {
			cmd = exec.Command("diskutil", "umount", mp)
		}
	case "linux":
		if force {
			cmd = exec.Command("umount", "-l", mp)
		} else {
			cmd = exec.Command("umount", mp)
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
