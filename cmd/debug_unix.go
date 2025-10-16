//go:build !windows
// +build !windows

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

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

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

	var psArgs []string
	if tmpPid != "" {
		pid = tmpPid
		psArgs = []string{"/bin/sh", "-c", fmt.Sprintf("ps -f -p %s", pid)}
	} else {
		psArgs = []string{"/bin/sh", "-c", fmt.Sprintf("ps -ef | grep -v grep | grep mount | grep %s", mp)}
	}
	ret, err := exec.Command(psArgs[0], psArgs[1:]...).CombinedOutput()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to execute command `%s`: %v", strings.Join(psArgs, " "), err)
	}
	var find bool
	var ppid string
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
				if find {
					newCmd := strings.Join(fields[7:], " ")
					newUid, newPid, newPpid := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])
					if newPid == ppid {
						return uid, pid, cmd, nil
					} else if pid == newPpid {
						return newUid, newPid, newCmd, nil
					} else {
						return "", "", "", fmt.Errorf("find more than one mount process for %s", mp)
					}
				}
				cmd = strings.Join(fields[7:], " ")
				uid, pid, ppid = strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])
				find = true
			}
		}
	}
	if cmd == "" {
		return "", "", "", fmt.Errorf("no mount command found for %s", mp)
	}
	return uid, pid, cmd, nil
}
