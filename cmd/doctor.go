/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"

	"github.com/urfave/cli/v2"
)

var defaultOutDir = path.Join(".", "doctor")

const (
	defaultTraceTime   = 5
	defaultProfileTime = 30
)

func cmdDoctor() *cli.Command {
	return &cli.Command{
		Name:      "doctor",
		Action:    doctor,
		Category:  "INSPECTOR",
		ArgsUsage: "MOUNTPOINT",
		Usage:     "Collect and show system static and runtime information",
		Description: `
It collects and show information from multiple dimensions such as the running environment and system logs, etc.

Examples:
$ juicefs doctor /mnt/jfs

# Result will be output to /var/log/
$ juicefs doctor --out-dir=/var/log /mnt/jfs

# Get log file up to 1000 entries
$ juicefs doctor --out-dir=/var/log --collect-log --limit=1000 /mnt/jfs

# Get pprof information
$ juicefs doctor --out-dir=/var/log --collect-log --limit=1000 --collect-pprof /mnt/jfs
`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "out-dir",
				Value: defaultOutDir,
				Usage: "the output directory of the result file",
			},
			&cli.BoolFlag{
				Name:  "collect-log",
				Usage: "enable log collection",
			},
			&cli.Uint64Flag{
				Name:  "limit",
				Usage: "the number of last entries to be collected",
			},
			&cli.BoolFlag{
				Name:  "collect-pprof",
				Usage: "enable pprof collection",
			},
			&cli.Uint64Flag{
				Name:  "trace-sec",
				Value: defaultTraceTime,
				Usage: "trace sampling duration",
			},
			&cli.Uint64Flag{
				Name:  "profile-sec",
				Value: defaultProfileTime,
				Usage: "profile sampling duration",
			},
		},
	}
}

func getVolumeConf(mp string) (string, error) {
	confPath := path.Join(mp, ".config")
	conf, err := os.ReadFile(confPath)
	if err != nil {
		return "", fmt.Errorf("error reading config %s: %v", confPath, err)
	}
	return string(conf), nil
}

func getCmdMount(mp string) (pid, cmd string, err error) {
	if !isUnix() {
		logger.Warnf("Failed to get command mount: %s is not supported", runtime.GOOS)
		return "", "", nil
	}

	ret, err := exec.Command("bash", "-c", "ps -ef | grep -v grep | grep 'juicefs mount' | grep "+mp).CombinedOutput()
	// `exit status 1"` occurs when there is no matching item for `grep`
	if err != nil {
		return "", "", fmt.Errorf("failed to execute command `ps -ef | grep juicefs | grep %s`: %v", mp, err)
	}

	lines := strings.Split(string(ret), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		fields := strings.Fields(line)
		if len(fields) <= 7 {
			continue
		}
		cmdFields := fields[7:]
		for _, arg := range cmdFields {
			if mp == arg {
				cmd = strings.Join(fields[7:], " ")
				pid = fields[1]
				break
			}
		}
	}
	if cmd == "" {
		return "", "", fmt.Errorf("not found mount point: %s", mp)
	}
	return pid, cmd, nil
}

func getDefaultLogDir() (string, error) {
	var defaultLogDir = "/var/log"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("faild to get home directory")
		}
		defaultLogDir = path.Join(homeDir, ".juicefs")
	}
	return defaultLogDir, nil
}

var logArg = regexp.MustCompile(`--log([=|\s])(\S+)`)

func getLogPath(cmd string) (string, error) {
	if !isUnix() {
		logger.Warnf("Failed to get log path: %s is not supported", runtime.GOOS)
		return "", nil
	}

	var logPath string
	tmp := logArg.FindStringSubmatch(cmd)
	if len(tmp) == 3 {
		logPath = tmp[2]
	} else {
		defaultLogDir, err := getDefaultLogDir()
		if err != nil {
			return "", err
		}
		logPath = path.Join(defaultLogDir, "juicefs.log")
	}

	return logPath, nil
}

func closeFile(file *os.File) {
	if err := file.Close(); err != nil {
		logger.Fatalf("error closing log file %s: %v", file.Name(), err)
	}
}

func copyLogFile(logPath, retLogPath string, limit uint64) error {
	logFile, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("error opening log file %s: %v", logPath, err)
	}
	defer closeFile(logFile)

	tmpFile, err := ioutil.TempFile("", "juicefs-")
	if err != nil {
		return fmt.Errorf("error creating log file %s: %v", tmpFile.Name(), err)
	}
	defer closeFile(tmpFile)
	writer := bufio.NewWriter(tmpFile)

	if limit > 0 {
		cmdStr := fmt.Sprintf("tail -n %d %s", limit, logPath)
		ret, err := exec.Command("bash", "-c", cmdStr).Output()
		if err != nil {
			return fmt.Errorf("tailing log error: %v", err)
		}
		if _, err = writer.Write(ret); err != nil {
			return fmt.Errorf("failed to copy log file: %v", err)
		}
	} else {
		reader := bufio.NewReader(logFile)
		if _, err := io.Copy(writer, reader); err != nil {
			return fmt.Errorf("failed to copy log file: %v", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %v", err)
	}
	if err := os.Rename(tmpFile.Name(), retLogPath); err != nil {
		return err
	}
	return nil
}

func getPprofPort(pid, mp string) (int, error) {
	if !isUnix() {
		logger.Warnf("Failed to get pprof port: %s is not supported", runtime.GOOS)
		return 0, nil
	}

	cmdStr := "lsof -i -nP | grep -v grep | grep LISTEN | grep " + pid
	if os.Getuid() == 0 {
		cmdStr = "sudo " + cmdStr
	}
	ret, err := exec.Command("bash", "-c", cmdStr).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to execute command `%s`: %v", cmdStr, err)
	}
	lines := strings.Split(string(ret), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("pprof will be collected, but no listen port")
	}

	var listenPort = -1
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 0 {
			port, err := strconv.Atoi(strings.Split(fields[len(fields)-2], ":")[1])
			if err != nil {
				logger.Errorf("failed to parse port %v: %v", port, err)
			}
			if port >= 6060 && port <= 6099 && port > listenPort {
				if err := checkPort(port, mp); err == nil {
					listenPort = port
				}
				continue
			}
		}
	}

	if listenPort == -1 {
		return 0, fmt.Errorf("no valid pprof port found")
	}
	return listenPort, nil
}

func getRequest(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error GET request: %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error GET request, status code %d", resp.StatusCode)
	}

	defer func(body io.ReadCloser) {
		if err := body.Close(); err != nil {
			logger.Errorf("error closing body: %v", err)
		}
	}(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	return body, nil
}

// check pprof service status
func checkPort(port int, mp string) error {
	url := fmt.Sprintf("http://localhost:%d/debug/pprof/cmdline?debug=1", port)
	resp, err := getRequest(url)
	if err != nil {
		return fmt.Errorf("error checking pprof alive: %v", err)
	}
	resp = bytes.ReplaceAll(resp, []byte{0}, []byte{' '})
	fields := strings.Fields(string(resp))
	flag := false
	for _, field := range fields {
		if mp == field {
			flag = true
		}
	}
	if !flag {
		return fmt.Errorf("mount point mismatch: \n%s\n%s", resp, mp)
	}

	return nil
}

type metricItem struct {
	name, url string
}

func reqAndSaveMetric(name string, metric metricItem, outDir string) error {
	resp, err := getRequest(metric.url)
	if err != nil {
		return fmt.Errorf("error getting metric: %v", err)
	}
	retPath := path.Join(outDir, fmt.Sprintf("juicefs.%s", metric.name))
	retFile, err := os.Create(retPath)
	if err != nil {
		logger.Fatalf("error creating metric file %s: %v", retPath, err)
	}
	defer closeFile(retFile)

	if name == "cmdline" {
		resp = bytes.ReplaceAll(resp, []byte{0}, []byte{' '})
	}

	writer := bufio.NewWriter(retFile)
	if _, err := writer.Write(resp); err != nil {
		return fmt.Errorf("error writing metric %s: %v", name, err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %v", err)
	}

	return nil
}

func isUnix() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

func checkAgent(cmd string) bool {
	fields := strings.Fields(cmd)
	for _, field := range fields {
		if field == "--no-agent" {
			return false
		}
	}
	return true
}

func doctor(ctx *cli.Context) error {
	currTime := time.Now().Format("20060102150405")
	setup(ctx, 1)
	mp := ctx.Args().First()
	inode, err := utils.GetFileInode(mp)
	if err != nil {
		return fmt.Errorf("lookup inode for %s: %s", mp, err)
	}
	if inode != 1 {
		return fmt.Errorf("path %s is not a mount point", mp)
	}

	outDir := ctx.String("out-dir")
	// special treatment for non-existing out dir

	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		if err := os.Mkdir(outDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create out dir %s: %v", outDir, err)
		}
	}

	// double check the file stat
	outDirInfo, err := os.Stat(outDir)
	if err != nil {
		return fmt.Errorf("failed to stat out dir %s: %v", outDir, err)
	}

	if !outDirInfo.IsDir() {
		return fmt.Errorf("argument --out-dir must be directory %s", outDir)
	}

	filePath := path.Join(outDir, fmt.Sprintf("system-info-%s.log", currTime))
	file, err := os.Create(filePath)
	defer closeFile(file)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %v", filePath, err)
	}

	osEntry, err := utils.GetEntry()
	if err != nil {
		return fmt.Errorf("failed to get system info: %v", err)
	}

	result := fmt.Sprintf(`Platform: 
%s %s
%s
JuiceFS Version:
%s`, runtime.GOOS, runtime.GOARCH, osEntry, ctx.App.Version)

	if _, err = file.WriteString(result); err != nil {
		return fmt.Errorf("failed to write system info %s: %v", filePath, err)
	}
	fmt.Printf("\n%s\n", result)

	mp, _ = filepath.Abs(mp)
	conf, err := getVolumeConf(mp)
	if err != nil {
		return err
	}
	prefix := strings.Trim(strings.Join(strings.Split(mp, "/"), "-"), "-")
	confPath := path.Join(outDir, fmt.Sprintf("%s-%s.config", prefix, currTime))
	confFile, err := os.Create(confPath)
	defer closeFile(confFile)
	if err != nil {
		return fmt.Errorf("failed to create config file %s: %v", confPath, err)
	}
	if _, err = confFile.WriteString(conf); err != nil {
		return fmt.Errorf("failed to write config %s: %v", confPath, err)
	}

	pid, cmd, err := getCmdMount(mp)
	if err != nil {
		return err
	}
	fmt.Printf("\nMount Command:\n%s\n\n", cmd)

	if ctx.Bool("collect-log") {
		logPath, err := getLogPath(cmd)
		if err != nil {
			return err
		}

		limit := ctx.Uint64("limit")
		retLogPath := path.Join(outDir, fmt.Sprintf("%s-%s.log", prefix, currTime))

		if err := copyLogFile(logPath, retLogPath, limit); err != nil {
			return fmt.Errorf("error copying log file: %v", err)
		}
		logger.Infof("Log %s is collected", logPath)
	}

	enableAgent := checkAgent(cmd)
	if !enableAgent {
		logger.Infof("No agent found")
	}

	if enableAgent && ctx.Bool("collect-pprof") {
		port, err := getPprofPort(pid, mp)
		if err != nil {
			return err
		}
		baseUrl := fmt.Sprintf("http://localhost:%d/debug/pprof/", port)
		trace := ctx.Uint64("trace-sec")
		profile := ctx.Uint64("profile-sec")
		metrics := map[string]metricItem{
			"allocs":       {name: "allocs.pb.gz", url: baseUrl + "allocs"},
			"blocks":       {name: "block.pb.gz", url: baseUrl + "block"},
			"cmdline":      {name: "cmdline.txt", url: baseUrl + "cmdline"},
			"goroutine":    {name: "goroutine.pb.gz", url: baseUrl + "goroutine"},
			"fullstack":    {name: "full.goroutine.stack.txt", url: baseUrl + "goroutine?debug=2"},
			"heap":         {name: "heap.pb.gz", url: baseUrl + "heap"},
			"mutex":        {name: "mutex.pb.gz", url: baseUrl + "mutex"},
			"threadcreate": {name: "threadcreate.pb.gz", url: baseUrl + "threadcreate"},
			"trace":        {name: fmt.Sprintf("trace.%ds.pb.gz", trace), url: fmt.Sprintf("%strace?seconds=%d", baseUrl, trace)},
			"profile":      {name: fmt.Sprintf("profile.%ds.pb.gz", profile), url: fmt.Sprintf("%sprofile?seconds=%d", baseUrl, profile)},
		}

		pprofOutDir := path.Join(outDir, fmt.Sprintf("pprof-%s-%s", prefix, currTime))
		if err := os.Mkdir(pprofOutDir, os.ModePerm); err != nil {
			return fmt.Errorf("error creating directory: %v", err)
		}

		var wg sync.WaitGroup
		for name, metric := range metrics {
			wg.Add(1)
			go func(name string, metric metricItem) {
				defer wg.Done()

				if name == "profile" {
					logger.Infof("Profile metrics are being sampled, sampling duration: %ds", profile)
				}
				if name == "trace" {
					logger.Infof("Trace metrics are being sampled, sampling duration: %ds", trace)
				}
				if err := reqAndSaveMetric(name, metric, pprofOutDir); err != nil {
					logger.Errorf("Error saving metric %s: %v", name, err)
				}
			}(name, metric)
		}
		wg.Wait()
	}
	return nil
}
