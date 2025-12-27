package cmd

/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"golang.org/x/sys/windows"
)

func getprocessCommandLine(pid int) (string, error) {
	cmd := exec.Command("wmic", "process", "where", "ProcessID="+strconv.Itoa(pid), "get", "CommandLine")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run command line: %s, %v", cmd.String(), err)
	}

	lines := strings.Split(string(out), "\r\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("failed to find command line for pid: %d", pid)
	}

	for _, line := range lines[1:] {
		sline := strings.TrimSpace(line)
		if sline == "" {
			continue
		}
		return sline, nil
	}

	return "", fmt.Errorf("cannot find command line for pid %d. If the juicefs are mounted at background, Please rerun this with the admin permission.", pid)
}

func findMountProcess(mp string) (int, error) {
	processName := filepath.Base(os.Args[0])
	cmd := exec.Command("wmic", "process", "where", fmt.Sprintf("name='%s'", processName), "get", "CommandLine,ProcessId")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to exec command line: %s, %s", cmd.String(), err)
	}

	lines := strings.Split(string(out), "\r\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("failed to find mount process")
	}

	mp = strings.TrimRight(mp, "\\")
	for _, line := range lines[1:] {
		sline := strings.TrimSpace(line)

		if sline == "" {
			continue
		}

		// the first part of commandline contains 'xxx/mount.exe"'
		slines := strings.SplitN(sline, ".exe\" ", 2)
		if len(slines) < 2 {
			logger.Warnf("failed to split command line: %s", sline)
			continue
		}

		sline = slines[1]
		logger.Infof("sline: %s", sline)

		args := strings.Split(sline, " ")
		if len(args) < 3 {
			continue
		}
		mpFound := false
		mountFound := false
		for _, arg := range args {
			arg = strings.TrimSpace(arg)
			if arg == "" {
				continue
			}

			if arg == "mount" {
				mountFound = true
				continue
			}

			arg = strings.TrimRight(arg, "\\")

			if strings.EqualFold(arg, mp) {
				mpFound = true
			}
		}

		if mpFound && mountFound {
			// THE LAST PART IS PID
			pid, err := strconv.Atoi(args[len(args)-1])
			if err != nil {
				return 0, fmt.Errorf("failed to parse pid: %s", args[len(args)-1])
			}
			return pid, nil
		}
	}

	return 0, fmt.Errorf("cannot find the mount process for %s", mp)
}

func getProcessUserSid(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	var token windows.Token
	err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token)
	if err != nil {
		return "", err
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}

	return user.User.Sid.String(), nil

}

func getCmdMount(mp string) (uid, pid, cmd string, err error) {
	var tmpPid string
	_ = utils.WithTimeout(context.TODO(), func(context.Context) error {
		content, err := readConfig(mp)
		if err != nil {
			logger.Warnf("failed to read config file: %v", err)
		}
		cfg := vfs.Config{}
		if err := json.Unmarshal(content, &cfg); err != nil {
			logger.Warnf("failed to unmarshal config file: %v", err)
		}
		if cfg.Pid != 0 {
			tmpPid = strconv.Itoa(cfg.Pid)
		}
		return nil
	}, 3*time.Second)

	foundPid := 0
	if tmpPid != "" {
		pid = tmpPid
		foundPid, err = strconv.Atoi(pid)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to parse pid: %s", pid)
		}
	} else {
		foundPid, err = findMountProcess(mp)
		if err != nil {
			return "", "", "", err
		}

		pid = strconv.Itoa(foundPid)
	}

	cmd, err = getprocessCommandLine(foundPid)
	if err != nil {
		return "", "", "", err
	}

	uid, err = getProcessUserSid(foundPid)
	if err != nil {
		return "", "", "", err
	}

	return uid, pid, cmd, nil
}
