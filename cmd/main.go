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
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"

	"github.com/google/gops/agent"
	"github.com/sirupsen/logrus"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/urfave/cli/v2"
)

var logger = utils.GetLogger("juicefs")

func globalFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    "verbose",
			Aliases: []string{"debug", "v"},
			Usage:   "enable debug log",
		},
		&cli.BoolFlag{
			Name:    "quiet",
			Aliases: []string{"q"},
			Usage:   "only warning and errors",
		},
		&cli.BoolFlag{
			Name:  "trace",
			Usage: "enable trace log",
		},
		&cli.BoolFlag{
			Name:  "no-agent",
			Usage: "Disable pprof (:6060) and gops (:6070) agent",
		},
	}
}

func main() {
	cli.VersionFlag = &cli.BoolFlag{
		Name: "version", Aliases: []string{"V"},
		Usage: "print only the version",
	}
	app := &cli.App{
		Name:                 "juicefs",
		Usage:                "A POSIX file system built on Redis and object storage.",
		Version:              version.Version(),
		Copyright:            "AGPLv3",
		EnableBashCompletion: true,
		Flags:                globalFlags(),
		Commands: []*cli.Command{
			formatFlags(),
			mountFlags(),
			umountFlags(),
			gatewayFlags(),
			syncFlags(),
			rmrFlags(),
			infoFlags(),
			benchFlags(),
			gcFlags(),
			checkFlags(),
			profileFlags(),
			statsFlags(),
			statusFlags(),
			warmupFlags(),
			dumpFlags(),
			loadFlags(),
		},
	}

	// Called via mount or fstab.
	if strings.HasSuffix(os.Args[0], "/mount.juicefs") {
		if newArgs, err := handleSysMountArgs(); err != nil {
			log.Fatal(err)
		} else {
			os.Args = newArgs
		}
	}

	err := app.Run(reorderOptions(app, os.Args))
	if err != nil {
		log.Fatal(err)
	}
}

func handleSysMountArgs() ([]string, error) {
	optionToCmdFlag := map[string]string{
		"attrcacheto":     "attr-cache",
		"entrycacheto":    "entry-cache",
		"direntrycacheto": "dir-entry-cache",
	}
	newArgs := []string{"juicefs", "mount", "-d"}
	mountOptions := os.Args[3:]
	sysOptions := []string{"_netdev", "rw", "defaults", "remount"}
	fuseOptions := make([]string, 0, 20)
	cmdFlagsLookup := make(map[string]bool, 20)
	for _, f := range append(mountFlags().Flags, globalFlags()...) {
		if names := f.Names(); len(names) > 0 && len(names[0]) > 1 {
			_, cmdFlagsLookup[names[0]] = f.(*cli.BoolFlag)
		}
	}

	parseFlag := false
	for _, option := range mountOptions {
		if option == "-o" {
			parseFlag = true
			continue
		}
		if !parseFlag {
			continue
		}

		opts := strings.Split(option, ",")
		for _, opt := range opts {
			opt = strings.TrimSpace(opt)
			if opt == "" || stringContains(sysOptions, opt) {
				continue
			}
			// Lower case option name is preferred, but if it's the same as flag name, we also accept it
			if strings.Contains(opt, "=") {
				fields := strings.SplitN(opt, "=", 2)
				if flagName, ok := optionToCmdFlag[fields[0]]; ok {
					newArgs = append(newArgs, fmt.Sprintf("--%s=%s", flagName, fields[1]))
				} else if isBool, ok := cmdFlagsLookup[fields[0]]; ok && !isBool {
					newArgs = append(newArgs, fmt.Sprintf("--%s=%s", fields[0], fields[1]))
				} else {
					fuseOptions = append(fuseOptions, opt)
				}
			} else if flagName, ok := optionToCmdFlag[opt]; ok {
				newArgs = append(newArgs, fmt.Sprintf("--%s", flagName))
			} else if isBool, ok := cmdFlagsLookup[opt]; ok && isBool {
				newArgs = append(newArgs, fmt.Sprintf("--%s", opt))
				if opt == "debug" {
					fuseOptions = append(fuseOptions, opt)
				}
			} else {
				fuseOptions = append(fuseOptions, opt)
			}
		}

		parseFlag = false
	}
	if len(fuseOptions) > 0 {
		newArgs = append(newArgs, "-o", strings.Join(fuseOptions, ","))
	}
	newArgs = append(newArgs, os.Args[1], os.Args[2])
	logger.Debug("Parsed mount args: ", strings.Join(newArgs, " "))
	return newArgs, nil
}

func stringContains(s []string, e string) bool {
	for _, item := range s {
		if item == e {
			return true
		}
	}
	return false
}

func isFlag(flags []cli.Flag, option string) (bool, bool) {
	if !strings.HasPrefix(option, "-") {
		return false, false
	}
	// --V or -v work the same
	option = strings.TrimLeft(option, "-")
	for _, flag := range flags {
		_, isBool := flag.(*cli.BoolFlag)
		for _, name := range flag.Names() {
			if option == name || strings.HasPrefix(option, name+"=") {
				return true, !isBool && !strings.Contains(option, "=")
			}
		}
	}
	return false, false
}

func reorderOptions(app *cli.App, args []string) []string {
	var newArgs = []string{args[0]}
	var others []string
	globalFlags := append(app.Flags, cli.VersionFlag)
	for i := 1; i < len(args); i++ {
		option := args[i]
		if ok, hasValue := isFlag(globalFlags, option); ok {
			newArgs = append(newArgs, option)
			if hasValue {
				i++
				newArgs = append(newArgs, args[i])
			}
		} else {
			others = append(others, option)
		}
	}
	// no command
	if len(others) == 0 {
		return newArgs
	}
	cmdName := others[0]
	var cmd *cli.Command
	for _, c := range app.Commands {
		if c.Name == cmdName {
			cmd = c
		}
	}
	if cmd == nil {
		// can't recognize the command, skip it
		return append(newArgs, others...)
	}

	newArgs = append(newArgs, cmdName)
	args, others = others[1:], nil
	// -h is valid for all the commands
	cmdFlags := append(cmd.Flags, cli.HelpFlag)
	for i := 0; i < len(args); i++ {
		option := args[i]
		if ok, hasValue := isFlag(cmdFlags, option); ok {
			newArgs = append(newArgs, option)
			if hasValue {
				i++
				newArgs = append(newArgs, args[i])
			}
		} else {
			if strings.HasPrefix(option, "-") && !stringContains(args, "--generate-bash-completion") {
				logger.Fatalf("unknown option: %s", option)
			}
			others = append(others, option)
		}
	}
	return append(newArgs, others...)
}

func setupAgent(c *cli.Context) {
	if !c.Bool("no-agent") {
		go func() {
			for port := 6060; port < 6100; port++ {
				_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), nil)
			}
		}()
		go func() {
			for port := 6070; port < 6100; port++ {
				_ = agent.Listen(agent.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
			}
		}()
	}
}

func setLoggerLevel(c *cli.Context) {
	if c.Bool("trace") {
		utils.SetLogLevel(logrus.TraceLevel)
	} else if c.Bool("verbose") {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if c.Bool("quiet") {
		utils.SetLogLevel(logrus.WarnLevel)
	} else {
		utils.SetLogLevel(logrus.InfoLevel)
	}
	setupAgent(c)
}
