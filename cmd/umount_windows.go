/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/gonutz/w32/v2"
)

func getProcessName(id uint32) string {
	snapshot := w32.CreateToolhelp32Snapshot(w32.TH32CS_SNAPMODULE, id)
	if snapshot == w32.ERROR_INVALID_HANDLE {
		return "<UNKNOWN>"
	}
	defer w32.CloseHandle(snapshot)

	var me w32.MODULEENTRY32
	me.Size = uint32(unsafe.Sizeof(me))
	if w32.Module32First(snapshot, &me) {
		return w32.UTF16PtrToString(&me.SzModule[0])
	}

	return "<UNKNOWN>"
}

func findProcessByName(name string) (*os.Process, error) {
	procs, _ := w32.EnumProcesses(make([]uint32, 10000))
	for _, pid := range procs {
		if pid == 0 || int(pid) == os.Getpid() {
			continue
		}
		n := getProcessName(pid)
		if strings.EqualFold(n, name) {
			return os.FindProcess(int(pid))
		}
	}
	return nil, fmt.Errorf("unknown process")
}

func killProcess(name string, mp string, force bool) error {
	p, err := findProcessByName(name)
	if err != nil {
		return fmt.Errorf("find %s: %s", name, err)
	}
	if force {
		logger.Infof("found process %s, kill it", name)
		return p.Kill()
	} else {
		logger.Infof("found process %s, stop it", name)
		return p.Signal(syscall.SIGTERM)
	}
}
