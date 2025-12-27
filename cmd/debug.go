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
	"context"
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
				Value: 5000,
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

func copyFile(srcPath, destPath string, requireRootPrivileges bool) error {
	if runtime.GOOS == "windows" {
		return utils.WithTimeout(context.TODO(), func(context.Context) error {
			return copyFileOnWindows(srcPath, destPath)
		}, 3*time.Second)
	}

	var copyArgs []string
	if requireRootPrivileges {
		copyArgs = append(copyArgs, "sudo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	copyArgs = append(copyArgs, "/bin/sh", "-c", fmt.Sprintf("cat %s > %s", srcPath, destPath))
	return exec.CommandContext(ctx, copyArgs[0], copyArgs[1:]...).Run()
}

var logArg = regexp.MustCompile(`--log(\s*=?\s*)(\S+)`)

func getLogPath(cmd string) (string, error) {
	var logPath string
	tmp := logArg.FindStringSubmatch(cmd)
	if len(tmp) == 3 {
		logPath = tmp[2]
	} else {
		logPath = filepath.Join(getDefaultLogDir(), "juicefs.log")
	}

	return logPath, nil
}

func closeFile(file *os.File) {
	if err := file.Close(); err != nil {
		logger.Fatalf("failed to close file %s: %v", file.Name(), err)
	}
}

func getPprofPort(pid, amp string, requireRootPrivileges bool) (int, error) {
	cfg := vfs.Config{}
	_ = utils.WithTimeout(context.TODO(), func(context.Context) error {
		content, err := readConfig(amp)
		if err != nil {
			logger.Warnf("failed to read config file: %v", err)
		}
		if err := json.Unmarshal(content, &cfg); err != nil {
			logger.Warnf("failed to unmarshal config file: %v", err)
		}
		return nil
	}, 3*time.Second)

	if cfg.Port != nil {
		if len(strings.Split(cfg.Port.DebugAgent, ":")) >= 2 {
			if port, err := strconv.Atoi(strings.Split(cfg.Port.DebugAgent, ":")[1]); err != nil {
				logger.Warnf("failed to parse debug agent port: %v", err)
			} else {
				return port, nil
			}
		}
	}

	var lsofArgs []string
	if requireRootPrivileges {
		lsofArgs = append(lsofArgs, "sudo")
	}
	lsofArgs = append(lsofArgs, "/bin/sh", "-c", "lsof -i -nP | grep -v grep | grep LISTEN | grep "+pid)
	ret, err := exec.Command(lsofArgs[0], lsofArgs[1:]...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to execute command `%s`: %v", strings.Join(lsofArgs, " "), err)
	}
	logger.Debugf("lsof output: \n%s", string(ret))
	lines := strings.Split(string(ret), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("pprof will be collected, but no listen port")
	}

	var listenPort = -1
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 0 {
			port, err := func() (port int, err error) {
				defer func() {
					e := recover()
					if e != nil {
						err = fmt.Errorf("failed to parse listen port: %v", e)
					}
				}()
				port, err = strconv.Atoi(strings.Split(fields[len(fields)-2], ":")[1])
				if err != nil {
					logger.Errorf("failed to parse port %v: %v", port, err)
				}
				return
			}()
			if err != nil {
				continue
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

func getRequest(url string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating GET request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
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
	resp, err := getRequest(url, 3*time.Second)
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

func reqAndSaveMetric(name string, metric metricItem, outDir string, timeout time.Duration) error {
	resp, err := getRequest(metric.url, timeout)
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

func collectPprof(ctx *cli.Context, cmd string, pid string, amp string, requireRootPrivileges bool, currDir string, wg *sync.WaitGroup) error {
	if !checkAgent(cmd) {
		logger.Warnf("No agent found, the pprof metrics will not be collected")
		return nil
	}

	port, err := getPprofPort(pid, amp, requireRootPrivileges)
	if err != nil {
		return fmt.Errorf("failed to get pprof port: %v", err)
	}
	baseUrl := fmt.Sprintf("http://localhost:%d/debug/pprof/", port)
	logger.Infof("The pprof base url: %s", baseUrl)
	trace := ctx.Uint64("trace-sec")
	profile := ctx.Uint64("profile-sec")
	metrics := map[string]metricItem{
		"allocs":       {name: "allocs.pb.gz", url: baseUrl + "allocs"},
		"blocks":       {name: "block.pb.gz", url: baseUrl + "block"},
		"cmdline":      {name: "cmdline.txt", url: baseUrl + "cmdline"},
		"goroutine":    {name: "goroutine.pb.gz", url: baseUrl + "goroutine"},
		"stack":        {name: "goroutine.stack.txt", url: baseUrl + "goroutine?debug=1"},
		"stack-detail": {name: "goroutine.stack.detail.txt", url: baseUrl + "goroutine?debug=2"},
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
			timeout := 3 * time.Second
			defer wg.Done()
			if name == "profile" {
				logger.Infof("Profile metrics are being sampled, sampling duration: %ds", profile)
				timeout = time.Duration(profile+5) * time.Second
			}
			if name == "trace" {
				logger.Infof("Trace metrics are being sampled, sampling duration: %ds", trace)
				timeout = time.Duration(trace+5) * time.Second
			}
			if err := reqAndSaveMetric(name, metric, pprofOutDir, timeout); err != nil {
				logger.Errorf("Failed to get and save metric %s: %v", name, err)
			}
		}(name, metric)
	}
	return nil
}

func collectLog(ctx *cli.Context, cmd string, requireRootPrivileges bool, currDir string, uid string) error {
	mountdByWinSystem := runtime.GOOS == "windows" && uid == "S-1-5-18" // https://learn.microsoft.com/en-us/windows/win32/secauthz/well-known-sids
	if !(strings.Contains(cmd, "-d") || strings.Contains(cmd, "--background")) && !mountdByWinSystem {
		logger.Warnf("The juicefs mount by foreground, the log will not be collected")
		return nil
	}
	logPath, err := getLogPath(cmd)
	if err != nil {
		return fmt.Errorf("failed to get log path: %v", err)
	}
	limit := ctx.Uint64("limit")
	retLogPath := filepath.Join(currDir, "juicefs.log")

	if runtime.GOOS == "windows" {
		// check powershell is installed
		_, err = exec.LookPath("powershell")
		if err != nil {
			logger.Warnf("Powershell is not installed, the log will not be collected")
			return nil
		}

		copyArgs := []string{"powershell", "-Command", fmt.Sprintf("Get-Content -Tail %d %s > %s", limit, logPath, retLogPath)}
		logger.Infof("The last %d lines of %s will be collected", limit, logPath)
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(timeoutCtx, copyArgs[0], copyArgs[1:]...).Run()
	} else {
		var copyArgs []string
		if requireRootPrivileges {
			copyArgs = append(copyArgs, "sudo")
		}
		copyArgs = append(copyArgs, "/bin/sh", "-c", fmt.Sprintf("tail -n %d %s > %s", limit, logPath, retLogPath))
		logger.Infof("The last %d lines of %s will be collected", limit, logPath)
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(timeoutCtx, copyArgs[0], copyArgs[1:]...).Run()
	}
}

func collectSysInfo(ctx *cli.Context, currDir string) error {
	sysInfo := utils.GetSysInfo()
	result := fmt.Sprintf(`Platform: 
%s %s
%s`, runtime.GOOS, runtime.GOARCH, sysInfo)

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

func collectSpecialFile(ctx *cli.Context, amp string, currDir string, requireRootPrivileges bool, wg *sync.WaitGroup) error {
	prefixed := true
	configName := ".jfs.config"
	_ = utils.WithTimeout(context.TODO(), func(context.Context) error {
		if !utils.Exists(filepath.Join(amp, configName)) {
			configName = ".config"
			prefixed = false
		}
		return nil
	}, 3*time.Second)
	if err := copyFile(filepath.Join(amp, configName), filepath.Join(currDir, "config.txt"), requireRootPrivileges); err != nil {
		return fmt.Errorf("failed to get volume config %s: %v", configName, err)
	}

	statsName := ".jfs.stats"
	if !prefixed {
		statsName = statsName[4:]
	}
	stats := ctx.Uint64("stats-sec")
	wg.Add(1)
	go func() {
		defer wg.Done()
		srcPath := filepath.Join(amp, statsName)
		destPath := filepath.Join(currDir, "stats.txt")
		if err := copyFile(srcPath, destPath, requireRootPrivileges); err != nil {
			logger.Errorf("Failed to get volume config %s: %v", statsName, err)
		}

		logger.Infof("Stats metrics are being sampled, sampling duration: %ds", stats)
		time.Sleep(time.Second * time.Duration(stats))
		destPath = filepath.Join(currDir, fmt.Sprintf("stats.%ds.txt", stats))
		if err := copyFile(srcPath, destPath, requireRootPrivileges); err != nil {
			logger.Errorf("Failed to get volume config %s: %v", statsName, err)
		}
	}()
	return nil
}

func debug(ctx *cli.Context) error {
	setup(ctx, 1)
	mp := ctx.Args().First()
	var inode uint64
	if err := utils.WithTimeout(context.TODO(), func(context.Context) error {
		var err error
		if inode, err = utils.GetFileInode(mp); err != nil {
			return fmt.Errorf("failed to lookup inode for %s: %s", mp, err)
		}
		return nil
	}, 3*time.Second); err != nil {
		logger.Warnf(err.Error())
		logger.Warnf("assuming the mount point is JuiceFS mount point")
	} else {
		if inode != uint64(meta.RootInode) {
			return fmt.Errorf("path %s is not a mount point", mp)
		}
	}

	amp, err := filepath.Abs(mp)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}
	timestamp := time.Now().Format("20060102150405")
	prefix := strings.Trim(strings.Join(strings.Split(amp, "/"), "-"), "-")
	if runtime.GOOS == "windows" {
		prefix = strings.ReplaceAll(prefix, ":", "")
	}
	outDir := ctx.String("out-dir")
	currDir := filepath.Join(outDir, fmt.Sprintf("%s-%s", prefix, timestamp))
	if err := os.MkdirAll(currDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create current out dir %s: %v", currDir, err)
	}

	if err := collectSysInfo(ctx, currDir); err != nil {
		logger.Errorf("Failed to collect system info: %v", err)
	}

	uid, pid, cmd, err := getCmdMount(amp)
	logger.Infof("mount point:%s pid:%s uid:%s", amp, pid, uid)
	if err != nil {
		return fmt.Errorf("failed to get mount command: %v", err)
	}
	fmt.Printf("\nMount Command:\n%s\n\n", cmd)

	requireRootPrivileges := false
	if (uid == "0" || uid == "root") && os.Getuid() != 0 {
		fmt.Println("Mount point is mounted by the root user, may ask for root privilege...")
		requireRootPrivileges = true
	}

	var wg sync.WaitGroup
	if err := collectSpecialFile(ctx, amp, currDir, requireRootPrivileges, &wg); err != nil {
		logger.Errorf("Failed to collect special file: %v", err)
	}

	if err := collectLog(ctx, cmd, requireRootPrivileges, currDir, uid); err != nil {
		logger.Errorf("Failed to collect log: %v", err)
	}

	if err := collectPprof(ctx, cmd, pid, amp, requireRootPrivileges, currDir, &wg); err != nil {
		logger.Errorf("Failed to collect pprof: %v", err)
	}

	wg.Wait()
	abs, _ := filepath.Abs(currDir)
	logger.Infof("All files are collected to %s", abs)
	return geneZipFile(currDir, filepath.Join(outDir, fmt.Sprintf("%s-%s.zip", prefix, timestamp)))
}
