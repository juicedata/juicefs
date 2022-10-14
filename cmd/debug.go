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
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

var defaultOutDir = filepath.Join(".", "debug")

func cmdDebug() *cli.Command {
	return &cli.Command{
		Name:      "debug",
		Action:    debug,
		Category:  "INSPECTOR",
		ArgsUsage: "MOUNTPOINT",
		Usage:     "Collect and display system static and runtime information",
		Description: `
It collects and displays information from multiple dimensions such as the running environment and system logs, etc.

Examples:
$ juicefs debug /mnt/jfs

# Result will be output to /var/log/
$ juicefs debug --out-dir=/var/log /mnt/jfs

# Get the last up to 1000 log entries
$ juicefs debug --out-dir=/var/log --limit=1000 /mnt/jfs
`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "out-dir",
				Value: defaultOutDir,
				Usage: "the output directory of the result file",
			},
			&cli.Uint64Flag{
				Name:  "limit",
				Usage: "the number of last entries to be collected",
			},
			&cli.Uint64Flag{
				Name:  "stats-sec",
				Value: 5,
				Usage: "stats sampling duration",
			},
			&cli.Uint64Flag{
				Name:  "trace-sec",
				Value: 5,
				Usage: "trace sampling duration",
			},
			&cli.Uint64Flag{
				Name:  "profile-sec",
				Value: 30,
				Usage: "profile sampling duration",
			},
		},
	}
}

func copyFileOnWindows(srcPath, destPath string) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer closeFile(srcFile)
	destFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer closeFile(destFile)
	if _, err := io.Copy(destFile, srcFile); err != nil {
		return err
	}
	return nil
}

func copyFile(srcPath, destPath string, rootPrivileges bool) error {
	if runtime.GOOS == "windows" {
		return copyFileOnWindows(srcPath, destPath)
	}

	var copyArgs []string
	if rootPrivileges {
		copyArgs = append(copyArgs, "sudo")
	}
	copyArgs = append(copyArgs, "/bin/sh", "-c", fmt.Sprintf("cat %s > %s", srcPath, destPath))
	return exec.Command(copyArgs[0], copyArgs[1:]...).Run()
}

func getCmdMount(mp string) (uid, pid, cmd string, err error) {
	psArgs := []string{"/bin/sh", "-c", "ps -ef | grep -v grep | grep 'juicefs mount' | grep " + mp}
	ret, err := exec.Command(psArgs[0], psArgs[1:]...).CombinedOutput()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to execute command `%s`: %v", strings.Join(psArgs, " "), err)
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
				uid, pid = strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
				break
			}
		}
	}
	if cmd == "" {
		return "", "", "", fmt.Errorf("no mount command found for %s", mp)
	}
	return uid, pid, cmd, nil
}

func getDefaultLogDir(rootPrivileges bool) (string, error) {
	var defaultLogDir = "/var/log"
	switch runtime.GOOS {
	case "linux":
		if rootPrivileges {
			break
		}
		fallthrough
	case "darwin":
		if rootPrivileges {
			defaultLogDir = "/var/root/.juicefs"
			break
		}
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory")
		}
		defaultLogDir = filepath.Join(homeDir, ".juicefs")
	}
	return defaultLogDir, nil
}

var logArg = regexp.MustCompile(`--log(\s*=?\s*)(\S+)`)

func getLogPath(cmd string, rootPrivileges bool) (string, error) {
	var logPath string
	tmp := logArg.FindStringSubmatch(cmd)
	if len(tmp) == 3 {
		logPath = tmp[2]
	} else {
		defaultLogDir, err := getDefaultLogDir(rootPrivileges)
		if err != nil {
			return "", err
		}
		logPath = filepath.Join(defaultLogDir, "juicefs.log")
	}

	return logPath, nil
}

func closeFile(file *os.File) {
	if err := file.Close(); err != nil {
		logger.Fatalf("failed to close file %s: %v", file.Name(), err)
	}
}

func copyLogFile(logPath, retLogPath string, limit uint64, rootPrivileges bool) error {
	var copyArgs []string
	if rootPrivileges {
		copyArgs = append(copyArgs, "sudo")
	}
	copyArgs = append(copyArgs, "/bin/sh", "-c")
	if limit > 0 {
		copyArgs = append(copyArgs, fmt.Sprintf("tail -n %d %s > %s", limit, logPath, retLogPath))
	} else {
		copyArgs = append(copyArgs, fmt.Sprintf("cat %s > %s", logPath, retLogPath))
	}
	return exec.Command(copyArgs[0], copyArgs[1:]...).Run()
}

func getPprofPort(pid, amp string, rootPrivileges bool) (int, error) {
	content, err := os.ReadFile(filepath.Join(amp, ".config"))
	if err != nil {
		logger.Warnf("failed to read config file: %v", err)
	}
	cfg := vfs.Config{}
	if err := json.Unmarshal(content, &cfg); err != nil {
		logger.Warnf("failed to unmarshal config file: %v", err)
	}
	if cfg.Port.DebugAgent != "" {
		if len(strings.Split(cfg.Port.DebugAgent, ":")) >= 2 {
			if port, err := strconv.Atoi(strings.Split(cfg.Port.DebugAgent, ":")[1]); err != nil {
				logger.Warnf("failed to parse debug agent port: %v", err)
			} else {
				return port, nil
			}
		}
	}

	var lsofArgs []string
	if rootPrivileges {
		lsofArgs = append(lsofArgs, "sudo")
	}
	lsofArgs = append(lsofArgs, "/bin/sh", "-c", "lsof -i -nP | grep -v grep | grep LISTEN | grep "+pid)
	ret, err := exec.Command(lsofArgs[0], lsofArgs[1:]...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to execute command `%s`: %v", strings.Join(lsofArgs, " "), err)
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
				if err := checkPort(port, amp); err == nil {
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
func checkPort(port int, amp string) error {
	url := fmt.Sprintf("http://localhost:%d/debug/pprof/cmdline?debug=1", port)
	resp, err := getRequest(url)
	if err != nil {
		return fmt.Errorf("error checking pprof alive: %v", err)
	}
	resp = bytes.ReplaceAll(resp, []byte{0}, []byte{' '})
	fields := strings.Fields(string(resp))
	flag := false
	for _, field := range fields {
		if amp == field {
			flag = true
		}
	}
	if !flag {
		return fmt.Errorf("mount point mismatch: \n%s\n%s", resp, amp)
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
	retPath := filepath.Join(outDir, fmt.Sprintf("juicefs.%s", metric.name))
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
		return fmt.Errorf("failed to write metric %s: %v", name, err)
	}
	return writer.Flush()
}

func isUnix() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

func checkAgent(cmd string) bool {
	for _, field := range strings.Fields(cmd) {
		if field == "--no-agent" {
			return false
		}
	}
	return true
}

func geneZipFile(srcPath, destPath string) error {
	zipFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer closeFile(zipFile)
	archive := zip.NewWriter(zipFile)
	defer func() {
		if err := archive.Close(); err != nil {
			logger.Fatalf("error closing zip archive: %v", err)
		}
	}()

	return filepath.Walk(srcPath, func(path string, info os.FileInfo, _ error) error {
		if path == srcPath {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = strings.TrimPrefix(path, srcPath+`/`)
		if info.IsDir() {
			header.Name += `/`
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer closeFile(file)
			if _, err := io.Copy(writer, file); err != nil {
				return err
			}
		}
		return nil
	})
}

func collectPprof(ctx *cli.Context, cmd string, pid string, amp string, rootPrivileges bool, currDir string, wg *sync.WaitGroup) error {
	if !checkAgent(cmd) {
		logger.Warnf("No agent found, the pprof metrics will not be collected")
		return nil
	}

	if !isUnix() {
		logger.Warnf("Collecting pprof currently only support Linux/macOS")
		return nil
	}

	port, err := getPprofPort(pid, amp, rootPrivileges)
	if err != nil {
		return fmt.Errorf("failed to get pprof port: %v", err)
	}
	baseUrl := fmt.Sprintf("http://localhost:%d/debug/pprof/", port)
	trace := ctx.Uint64("trace-sec")
	profile := ctx.Uint64("profile-sec")
	metrics := map[string]metricItem{
		"allocs":       {name: "allocs.pb.gz", url: baseUrl + "allocs"},
		"blocks":       {name: "block.pb.gz", url: baseUrl + "block"},
		"cmdline":      {name: "cmdline.txt", url: baseUrl + "cmdline"},
		"goroutine":    {name: "goroutine.pb.gz", url: baseUrl + "goroutine"},
		"stack":        {name: "goroutine.stack.txt", url: baseUrl + "goroutine?debug=1"},
		"heap":         {name: "heap.pb.gz", url: baseUrl + "heap"},
		"mutex":        {name: "mutex.pb.gz", url: baseUrl + "mutex"},
		"threadcreate": {name: "threadcreate.pb.gz", url: baseUrl + "threadcreate"},
		"trace":        {name: fmt.Sprintf("trace.%ds.pb.gz", trace), url: fmt.Sprintf("%strace?seconds=%d", baseUrl, trace)},
		"profile":      {name: fmt.Sprintf("profile.%ds.pb.gz", profile), url: fmt.Sprintf("%sprofile?seconds=%d", baseUrl, profile)},
	}

	pprofOutDir := filepath.Join(currDir, "pprof")
	if err := os.Mkdir(pprofOutDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create out directory: %v", err)
	}

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
				logger.Errorf("Failed to get and save metric %s: %v", name, err)
			}
		}(name, metric)
	}
	return nil
}

func collectLog(ctx *cli.Context, cmd string, rootPrivileges bool, currDir string) error {
	if !isUnix() {
		logger.Warnf("Collecting log currently only support Linux/macOS")
		return nil
	}
	logPath, err := getLogPath(cmd, rootPrivileges)
	if err != nil {
		return fmt.Errorf("failed to get log path: %v", err)
	}
	limit := ctx.Uint64("limit")
	retLogPath := filepath.Join(currDir, "juicefs.log")

	logger.Infof("Log %s is being collected", logPath)
	return copyLogFile(logPath, retLogPath, limit, rootPrivileges)
}

func collectSysInfo(ctx *cli.Context, currDir string) error {
	sysInfo, err := utils.GetSysInfo()
	if err != nil {
		return err
	}

	result := fmt.Sprintf(`Platform: 
%s %s
%s
JuiceFS Version:
%s`, runtime.GOOS, runtime.GOARCH, sysInfo, ctx.App.Version)

	sysPath := filepath.Join(currDir, "system-info.log")
	sysFile, err := os.Create(sysPath)
	if err != nil {
		return fmt.Errorf("failed to create system info file %s: %v", sysPath, err)
	}
	defer closeFile(sysFile)
	if _, err = sysFile.WriteString(result); err != nil {
		return fmt.Errorf("failed to write system info file %s: %v", sysPath, err)
	}

	fmt.Printf("\n%s\n", result)
	return nil
}

func collectSpecialFile(ctx *cli.Context, amp string, currDir string, rootPrivileges bool, wg *sync.WaitGroup) error {
	configName := ".config"
	if err := copyFile(filepath.Join(amp, configName), filepath.Join(currDir, "config.txt"), rootPrivileges); err != nil {
		return fmt.Errorf("failed to get volume config %s: %v", configName, err)
	}

	statsName := ".stats"
	stats := ctx.Uint64("stats-sec")
	wg.Add(1)
	go func() {
		defer wg.Done()
		srcPath := filepath.Join(amp, statsName)
		destPath := filepath.Join(currDir, "stats.txt")
		if err := copyFile(srcPath, destPath, rootPrivileges); err != nil {
			logger.Errorf("Failed to get volume config %s: %v", statsName, err)
		}

		logger.Infof("Stats metrics are being sampled, sampling duration: %ds", stats)
		time.Sleep(time.Second * time.Duration(stats))
		destPath = filepath.Join(currDir, fmt.Sprintf("stats.%ds.txt", stats))
		if err := copyFile(srcPath, destPath, rootPrivileges); err != nil {
			logger.Errorf("Failed to get volume config %s: %v", statsName, err)
		}
	}()
	return nil
}

func debug(ctx *cli.Context) error {
	setup(ctx, 1)
	mp := ctx.Args().First()
	inode, err := utils.GetFileInode(mp)
	if err != nil {
		return fmt.Errorf("failed to lookup inode for %s: %s", mp, err)
	}
	if inode != uint64(meta.RootInode) {
		return fmt.Errorf("path %s is not a mount point", mp)
	}

	amp, err := filepath.Abs(mp)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}
	timestamp := time.Now().Format("20060102150405")
	prefix := strings.Trim(strings.Join(strings.Split(amp, "/"), "-"), "-")
	outDir := ctx.String("out-dir")
	currDir := filepath.Join(outDir, fmt.Sprintf("%s-%s", prefix, timestamp))
	if err := os.MkdirAll(currDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create current out dir %s: %v", currDir, err)
	}

	if err := collectSysInfo(ctx, currDir); err != nil {
		return err
	}

	uid, pid, cmd, err := getCmdMount(amp)
	if err != nil {
		return fmt.Errorf("failed to get mount command: %v", err)
	}
	fmt.Printf("\nMount Command:\n%s\n\n", cmd)

	rootPrivileges := false
	if (uid == "0" || uid == "root") && os.Getuid() != 0 {
		fmt.Println("Mount point is mounted by the root user, may ask for root privilege...")
		rootPrivileges = true
	}

	var wg sync.WaitGroup
	if err := collectSpecialFile(ctx, amp, currDir, rootPrivileges, &wg); err != nil {
		return err
	}

	if err := collectLog(ctx, cmd, rootPrivileges, currDir); err != nil {
		return err
	}

	if err := collectPprof(ctx, cmd, pid, amp, rootPrivileges, currDir, &wg); err != nil {
		return err
	}

	wg.Wait()
	return geneZipFile(currDir, filepath.Join(outDir, fmt.Sprintf("%s-%s.zip", prefix, timestamp)))
}
