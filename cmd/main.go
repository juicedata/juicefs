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
	_ "net/http/pprof"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

var logger = utils.GetLogger("juicefs")

func main() {
	cli.VersionFlag = &cli.BoolFlag{
		Name: "version", Aliases: []string{"V"},
		Usage: "print only the version",
	}
	app := &cli.App{
		Name:      "juicefs",
		Usage:     "A POSIX file system built on Redis and object storage.",
		Version:   Version(),
		Copyright: "AGPLv3",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"v"},
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
		},
		Commands: []*cli.Command{
			formatFlags(),
			mountFlags(),
			syncFlags(),
			benchmarkFlags(),
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

	err := app.Run(os.Args)
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
	for _, f := range mountFlags().Flags {
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
			} else {
				fuseOptions = append(fuseOptions, opt)
				if opt == "debug" {
					tmpArgs := []string{"juicefs", "--debug", "mount", "-d"}
					newArgs = append(tmpArgs, newArgs[3:]...)
				}
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

func setLoggerLevel(c *cli.Context) {
	if c.Bool("trace") {
		utils.SetLogLevel(logrus.TraceLevel)
	} else if c.Bool("debug") {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if c.Bool("quiet") {
		utils.SetLogLevel(logrus.WarnLevel)
	}
}
