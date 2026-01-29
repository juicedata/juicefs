package cmd

import (
	"fmt"
	"runtime"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdPrintSID() *cli.Command {
	return &cli.Command{
		Name:     "printsid",
		Category: "TOOL",
		Action:   printSID,
		Usage:    "Show SID info and the convected UID/GID for the current user.",
		Hidden:   true,
	}
}

func printSID(ctx *cli.Context) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("printsid command is only supported on Windows")
	}

	userSid := utils.GetCurrentUserSIDStr()
	groupSid := utils.GetCurrentUserGroupSIDStr()
	fmt.Printf("Current User SID: %s, UID: %d\n", userSid, utils.GetCurrentUID())
	fmt.Printf("Current Group SID: %s, GID: %d\n", groupSid, utils.GetCurrentGID())

	return nil
}
