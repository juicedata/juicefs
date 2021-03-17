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
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/urfave/cli/v2"
	"reflect"
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
		Version:   version.Version(),
		Copyright: "AGPLv3",
		Flags: []cli.Flag{
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
		},
		Commands: []*cli.Command{
			formatFlags(),
			mountFlags(),
			umountFlags(),
			gatewayFlags(),
			syncFlags(),
			rmrFlags(),
			benchmarkFlags(),
			gcFlags(),
			checkFlags(),
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

	log.Printf("origin:%v", os.Args)

	os.Args = reorderArgs(app, os.Args)

	log.Printf("reorder:%v", os.Args)
	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}


func flagComplete(flags []string) []string {
	newFlags := []string {}
	for _, flag := range flags {
		if len(flag) > 1 {
			newFlags = append(newFlags, fmt.Sprintf("--%s", flag))
		}
		if len(flag) == 1 {
			newFlags = append(newFlags, fmt.Sprintf("-%s", flag))
		}
	}
	return newFlags
}

func processGlobalOptions(gm map[string]bool, args []string) []string {
	newArgs  := []string{args[0]}
	tailArgs := []string{}
	log.Printf("processGlobalOptions begin %v", args)

	for _, t := range args[1:] {
		if _, ok := gm[t]; ok {
			newArgs = append(newArgs, t)
		}else {
			tailArgs =append(tailArgs,t)
		}
	}
	newArgs = append(newArgs, tailArgs...)
	log.Printf("processGlobalOptions end %v", newArgs )
	return newArgs
}
func processCommand(cm map[string]string, args []string) []string {
	newArgs  := []string{}
	headArgs := []string{args[0]}
	tailArgs := []string{}

	log.Printf("processCommand begin %v", args)


	changeToTail := false
	for _, t := range args[1:] {
		if _, ok := cm[t]; ok {
			newArgs = append(newArgs, t)
			changeToTail = true
		}else {
			if changeToTail {
				tailArgs =append(tailArgs,t )
			} else {
				headArgs = append(headArgs, t)
			}
		}
	}
	headArgs = append(append(headArgs, newArgs...), tailArgs...)
	log.Printf("processCommand end %v", headArgs )
	return headArgs
}
func processCommandOptions(cfm map[string]bool, args []string) ([]string,[]string,[]string) {

	mergedArgs  := []string{}
	headArgs := []string{args[0]}
	tailArgs := []string{}
	cmfArgs := []string{}

	log.Printf("processCommandOptions begin %v", args)


	// merge command options
	j := len(args)
	for i:=1 ; i < j; i++ {
		p := args[i]
		if b, ok := cfm[p]; ok {
			if b { // 是bool型的
				if i+1 < j && (args[i+1] == "false" || args[i+1] == "true" ) {
					mergedArgs = append(mergedArgs, fmt.Sprintf("%s=%s", p, args[i+1]))
					cfm[fmt.Sprintf("%s=%s", p, args[i+1])] = false
					i++
					continue
				}
			} else { // 值型
				if i+1 < j {
					mergedArgs = append(mergedArgs, fmt.Sprintf("%s=%s", p, args[i+1]))
					cfm[fmt.Sprintf("%s=%s", p, args[i+1])] = false
					i++
					continue
				}
			}
		}
		mergedArgs = append(mergedArgs, p)
	}

	changeToTail := false
	log.Printf("mergeArgs:%v", mergedArgs)
	keys := reflect.ValueOf(cfm).MapKeys()
	log.Printf("commandFlagMap:%v",keys)
	for _, t := range mergedArgs {
		if _, ok := cfm[t] ; ok {
			cmfArgs = append(cmfArgs, t)
			changeToTail = true
		}else {
			if changeToTail {
				tailArgs = append(tailArgs, t)
			} else {
				headArgs = append(headArgs, t)
			}
		}
	}
	log.Printf("processCommandOptions 3 head:%v cmf:%v tail:%v", headArgs, cmfArgs,tailArgs )
	//headArgs = append(append(headArgs, cmfArgs...), tailArgs...)
	log.Printf("processCommandOptions end %v", headArgs )
	return headArgs, cmfArgs, tailArgs
}

// juicefs [global options] command [command options] [arguments...]
func reorderArgs(app *cli.App, args []string) []string {
	if len(args) <= 1 {
		return args
	}
	allOrdered :=[]string{}
	// init dictionary
	globalFlagMap := make(map[string]bool,0)
	commandMap := map[string]string{}
	commandFlagMap := make(map[string]bool,0)
	for _, f := range app.Flags {
		switch f.(type) {
		case *cli.BoolFlag:
			for _,fc := range flagComplete(f.Names()) {
				globalFlagMap[fc] = true
			}
		default:
			for _,fc := range flagComplete(f.Names()) {
				globalFlagMap[fc] = false
			}
		}
	}
	for _, c := range app.Commands{
		commandMap[c.Name] = c.Name
		for _, f := range c.Flags {
			switch f.(type) {
			case *cli.BoolFlag:
				for _,fc := range flagComplete(f.Names()) {
					commandFlagMap[fc] = true
				}
			default:
				for _,fc := range flagComplete(f.Names()) {
					commandFlagMap[fc] = false
				}
			}
		}
	}

	keys := reflect.ValueOf(globalFlagMap).MapKeys()
	log.Printf("globalFlagMap:%v",keys)


	keys = reflect.ValueOf(commandMap).MapKeys()
	log.Printf("commandMap:%v",keys)

	keys = reflect.ValueOf(commandFlagMap).MapKeys()
	log.Printf("commandFlagMap:%v",keys)

	globalOptionOrdered := processGlobalOptions(globalFlagMap, args)
	globalOptionAndcommandOrdered := processCommand(commandMap, globalOptionOrdered)
	h, c, t := processCommandOptions(commandFlagMap, globalOptionAndcommandOrdered)
	for _, item := range h[1:] {
		if _, ok :=globalFlagMap[item]; ok {
			allOrdered = append(allOrdered, item)
			continue
		}
		if _, ok :=commandMap[item]; ok {
			allOrdered = append(allOrdered, item)
			continue
		}
		th := []string{item}
		th = append(th, t...)
		t  = th
	}
	return append(append(allOrdered, c...), t...)
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
	} else if c.Bool("verbose") {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if c.Bool("quiet") {
		utils.SetLogLevel(logrus.WarnLevel)
	}
}
