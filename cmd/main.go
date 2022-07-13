/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"

	"github.com/erikdubbelboer/gspt"
	"github.com/google/gops/agent"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/pyroscope-io/client/pyroscope"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

var logger = utils.GetLogger("juicefs")

func Main(args []string) error {
	// we have to call this because gspt removes all arguments
	gspt.SetProcTitle(strings.Join(os.Args, " "))
	cli.VersionFlag = &cli.BoolFlag{
		Name: "version", Aliases: []string{"V"},
		Usage: "print version only",
	}
	app := &cli.App{
		Name:                 "juicefs",
		Usage:                "A POSIX file system built on Redis and object storage.",
		Version:              version.Version(),
		Copyright:            "Apache License 2.0",
		HideHelpCommand:      true,
		EnableBashCompletion: true,
		Flags:                globalFlags(),
		Commands: []*cli.Command{
			cmdFormat(),
			cmdConfig(),
			cmdDestroy(),
			cmdGC(),
			cmdFsck(),
			cmdDump(),
			cmdLoad(),
			cmdVersion(),
			cmdStatus(),
			cmdStats(),
			cmdProfile(),
			cmdInfo(),
			cmdMount(),
			cmdUmount(),
			cmdGateway(),
			cmdWebDav(),
			cmdBench(),
			cmdObjbench(),
			cmdMdtest(),
			cmdWarmup(),
			cmdRmr(),
			cmdSync(),
			cmdDoctor(),
		},
	}

	if calledViaMount(args) {
		args = handleSysMountArgs(args)
		if len(args) < 1 {
			args = []string{"mount", "--help"}
		}
	}
	return app.Run(reorderOptions(app, args))
}

func calledViaMount(args []string) bool {
	return strings.HasSuffix(args[0], "/mount.juicefs")
}

func handleSysMountArgs(args []string) []string {
	optionToCmdFlag := map[string]string{
		"attrcacheto":     "attr-cache",
		"entrycacheto":    "entry-cache",
		"direntrycacheto": "dir-entry-cache",
	}
	newArgs := []string{"juicefs", "mount", "-d"}
	if len(args) < 3 {
		return nil
	}
	mountOptions := args[3:]
	sysOptions := []string{"_netdev", "rw", "defaults", "remount"}
	fuseOptions := make([]string, 0, 20)
	cmdFlagsLookup := make(map[string]bool, 20)
	for _, f := range append(cmdMount().Flags, globalFlags()...) {
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
			if opt == "" || opt == "background" || utils.StringContains(sysOptions, opt) {
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
	newArgs = append(newArgs, args[1], args[2])
	logger.Debug("Parsed mount args: ", strings.Join(newArgs, " "))
	return newArgs
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
			if hasValue && len(args[i+1:]) > 0 {
				i++
				newArgs = append(newArgs, args[i])
			}
		} else {
			if strings.HasPrefix(option, "-") && !utils.StringContains(args, "--generate-bash-completion") {
				logger.Fatalf("unknown option: %s", option)
			}
			others = append(others, option)
		}
	}
	return append(newArgs, others...)
}

// Check number of positional arguments, set logger level and setup agent if needed
func setup(c *cli.Context, n int) {
	if c.NArg() < n {
		fmt.Printf("ERROR: This command requires at least %d arguments\n", n)
		fmt.Printf("USAGE:\n   juicefs %s [command options] %s\n", c.Command.Name, c.Command.ArgsUsage)
		os.Exit(1)
	}

	if c.Bool("trace") {
		utils.SetLogLevel(logrus.TraceLevel)
	} else if c.Bool("verbose") {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if c.Bool("quiet") {
		utils.SetLogLevel(logrus.WarnLevel)
	} else {
		utils.SetLogLevel(logrus.InfoLevel)
	}
	if c.Bool("no-color") {
		utils.DisableLogColor()
	}

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

	if c.IsSet("pyroscope") {
		tags := make(map[string]string)
		appName := fmt.Sprintf("juicefs.%s", c.Command.Name)
		if c.Command.Name == "mount" {
			tags["mountpoint"] = c.Args().Get(1)
		}
		if hostname, err := os.Hostname(); err == nil {
			tags["hostname"] = hostname
		}
		tags["pid"] = strconv.Itoa(os.Getpid())
		tags["version"] = version.Version()

		if _, err := pyroscope.Start(pyroscope.Config{
			ApplicationName: appName,
			ServerAddress:   c.String("pyroscope"),
			Logger:          logger,
			Tags:            tags,
			AuthToken:       os.Getenv("PYROSCOPE_AUTH_TOKEN"),
			ProfileTypes:    pyroscope.DefaultProfileTypes,
		}); err != nil {
			logger.Errorf("start pyroscope agent: %v", err)
		}
	}
}

func removePassword(uri string) {
	var uri2 string
	if strings.Contains(uri, "://") {
		uri2 = utils.RemovePassword(uri)
	} else {
		uri2 = utils.RemovePassword("redis://" + uri)
	}
	if uri2 != uri {
		for i, a := range os.Args {
			if a == uri {
				os.Args[i] = uri2
				break
			}
		}
	}
	gspt.SetProcTitle(strings.Join(os.Args, " "))
}
