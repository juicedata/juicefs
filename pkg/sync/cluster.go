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

package sync

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/oliverisaac/shellescape"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
)

// Stat has the counters to represent the progress.
type Stat struct {
	Copied       int64 // the number of copied files
	CopiedBytes  int64 // total amount of copied data in bytes
	Checked      int64 // the number of checked files
	CheckedBytes int64 // total amount of checked data in bytes
	Deleted      int64 // the number of deleted files
	Skipped      int64 // the number of files skipped
	SkippedBytes int64 // total amount of skipped data in bytes
	Failed       int64 // the number of files that fail to copy
}

func updateStats(r *Stat) {
	copied.IncrInt64(r.Copied)
	copiedBytes.IncrInt64(r.CopiedBytes)
	if checked != nil {
		checked.IncrInt64(r.Checked)
		checkedBytes.IncrInt64(r.CheckedBytes)
	}
	if deleted != nil {
		deleted.IncrInt64(r.Deleted)
	}
	skipped.IncrInt64(r.Skipped)
	skippedBytes.IncrInt64(r.SkippedBytes)
	if failed != nil {
		failed.IncrInt64(r.Failed)
	}
	handled.IncrInt64(r.Copied + r.Deleted + r.Skipped + r.Failed)
}

func httpRequest(url string, body []byte) (ans []byte, err error) {
	method := "GET"
	if body != nil {
		method = "POST"
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

var sendStatMu sync.Mutex

func sendStats(addr string) {
	sendStatMu.Lock()
	defer sendStatMu.Unlock()
	var r Stat
	r.Skipped = skipped.Current()
	r.SkippedBytes = skippedBytes.Current()
	r.Copied = copied.Current()
	r.CopiedBytes = copiedBytes.Current()
	if checked != nil {
		r.Checked = checked.Current()
		r.CheckedBytes = checkedBytes.Current()
	}
	if deleted != nil {
		r.Deleted = deleted.Current()
	}
	if failed != nil {
		r.Failed = failed.Current()
	}
	d, _ := json.Marshal(r)
	ans, err := httpRequest(fmt.Sprintf("http://%s/stats", addr), d)
	if err != nil || string(ans) != "OK" {
		if errors.Is(err, syscall.ECONNREFUSED) {
			logger.Errorf("the management process has been stopped, so the worker process now exits")
			os.Exit(1)
		}
		logger.Errorf("update stats: %s %s", string(ans), err)
	} else {
		skipped.IncrInt64(-r.Skipped)
		skippedBytes.IncrInt64(-r.SkippedBytes)
		copied.IncrInt64(-r.Copied)
		copiedBytes.IncrInt64(-r.CopiedBytes)
		if checked != nil {
			checked.IncrInt64(-r.Checked)
			checkedBytes.IncrInt64(-r.CheckedBytes)
		}
		if deleted != nil {
			deleted.IncrInt64(-r.Deleted)
		}
		if failed != nil {
			failed.IncrInt64(-r.Failed)
		}
	}
}

func startManager(config *Config, tasks <-chan object.Object) (string, error) {
	http.HandleFunc("/fetch", func(w http.ResponseWriter, req *http.Request) {
		var objs []object.Object
		var total int64
		obj, ok := <-tasks
		if !ok {
			_, _ = w.Write([]byte("[]"))
			return
		}
		objs = append(objs, obj)
		total += obj.Size()
	LOOP:
		for len(objs) < 100 && total < 400<<20 {
			select {
			case obj = <-tasks:
				if obj == nil {
					break LOOP
				}
				objs = append(objs, obj)
				total += obj.Size()
			default:
				break LOOP
			}
		}
		d, err := marshalObjects(objs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logger.Debugf("send %d objects(%s) to %s", len(objs), humanize.IBytes(uint64(total)), req.RemoteAddr)
		_, _ = w.Write(d)
	})
	http.HandleFunc("/stats", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" {
			http.Error(w, "POST required", http.StatusBadRequest)
			return
		}
		d, err := io.ReadAll(req.Body)
		if err != nil {
			logger.Errorf("read: %s", err)
			return
		}
		var r Stat
		err = json.Unmarshal(d, &r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updateStats(&r)
		logger.Debugf("receive stats %+v from %s", r, req.RemoteAddr)
		_, _ = w.Write([]byte("OK"))
	})
	var addr string
	if config.ManagerAddr != "" {
		addr = config.ManagerAddr
	} else {
		ip, err := utils.GetLocalIp(net.JoinHostPort(config.Workers[0], "22"))
		if err != nil {
			return "", fmt.Errorf("not found local ip: %s", err)
		}
		addr = ip
	}

	if !strings.Contains(addr, ":") {
		addr += ":"
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen: %s", err)
	}
	logger.Infof("Listen at %s", l.Addr())
	go func() { _ = http.Serve(l, nil) }()
	return l.Addr().String(), nil
}

func findSelfPath() (string, error) {
	program := os.Args[0]
	if strings.Contains(program, "/") {
		path, err := filepath.Abs(program)
		if err != nil {
			return "", fmt.Errorf("resolve path %s: %s", program, err)
		}
		return path, nil
	}
	for _, searchPath := range strings.Split(os.Getenv("PATH"), ":") {
		if searchPath != "" {
			p := filepath.Join(searchPath, program)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("can't find path for %s", program)
}

func launchWorker(address string, config *Config, wg *sync.WaitGroup) {
	workers := strings.Split(strings.Join(config.Workers, ","), ",")
	for _, host := range workers {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			// copy
			path, err := findSelfPath()
			if err != nil {
				logger.Errorf("find self path: %s", err)
				return
			}
			rpath := filepath.Join("/tmp", filepath.Base(path))
			cmd := exec.Command("rsync", "-au", path, host+":"+rpath)
			output, err := cmd.CombinedOutput()
			logger.Debugf("exec: %s,err: %s", cmd.String(), string(output))
			if err != nil {
				// fallback to scp
				cmd = exec.Command("scp", "-o", "StrictHostKeyChecking=no", path, host+":"+rpath)
				output, err = cmd.CombinedOutput()
				logger.Debugf("exec: %s,err: %s", cmd.String(), string(output))
			}
			if err != nil {
				logger.Errorf("copy itself to %s: %s", host, err)
				return
			}
			// launch itself
			var args = []string{host}
			// set env
			var printEnv []string
			for k, v := range config.Env {
				args = append(args, fmt.Sprintf("%s=%s", k, v))
				if strings.Contains(k, "SECRET") ||
					strings.Contains(k, "TOKEN") ||
					strings.Contains(k, "PASSWORD") ||
					strings.Contains(k, "AZURE_STORAGE_CONNECTION_STRING") ||
					strings.Contains(k, "JFS_RSA_PASSPHRASE") {
					v = "******"
				}
				printEnv = append(printEnv, fmt.Sprintf("%s=%s", k, v))
			}

			args = append(args, rpath)
			args = append(args, os.Args[1:]...)
			args = append(args, "--manager", address)
			if !config.Verbose && !config.Quiet {
				args = append(args, "-q")
			}
			var argsBk = make([]string, len(args))
			copy(argsBk, args)
			for i, s := range printEnv {
				argsBk[i+1] = s
			}
			logger.Debugf("launch worker command args: [ssh, %s]", strings.Join(shellescape.EscapeArgs(argsBk), ", "))
			cmd = exec.Command("ssh", shellescape.EscapeArgs(args)...)
			cmd.Stdin = os.Stdin
			stderr, err := cmd.StderrPipe()
			if err != nil {
				logger.Errorf("redirect stderr: %s", err)
			}
			err = cmd.Start()
			if err != nil {
				logger.Errorf("start itself at %s: %s", host, err)
				return
			}
			logger.Infof("launch a worker on %s", host)
			var finished = make(chan struct{})
			go func() {
				r := bufio.NewReader(stderr)
				for {
					line, err := r.ReadString('\n')
					if err != nil || len(line) == 0 {
						finished <- struct{}{}
						return
					}
					println(host, line[:len(line)-1])
				}
			}()
			err = cmd.Wait()
			<-finished
			if err != nil {
				logger.Errorf("%s: %s", host, err)
			}
		}(host)
	}
}

func marshalObjects(objs []object.Object) ([]byte, error) {
	var arr []map[string]interface{}
	for _, o := range objs {
		obj := object.MarshalObject(o)
		switch oo := o.(type) {
		case *withSize:
			obj["nsize"] = oo.nsize
			obj["size"] = oo.Object.Size()
		case *withFSize:
			obj["fnsize"] = oo.nsize
			obj["size"] = oo.File.Size()
		}
		arr = append(arr, obj)
	}
	return json.MarshalIndent(arr, "", " ")
}

func unmarshalObjects(d []byte) ([]object.Object, error) {
	var arr []map[string]interface{}
	err := json.Unmarshal(d, &arr)
	if err != nil {
		return nil, err
	}
	var objs []object.Object
	for _, m := range arr {
		obj := object.UnmarshalObject(m)
		if nsize, ok := m["nsize"]; ok {
			obj = &withSize{obj, int64(nsize.(float64))}
		} else if fnsize, ok := m["fnsize"]; ok {
			obj = &withFSize{obj.(object.File), int64(fnsize.(float64))}
		}
		objs = append(objs, obj)
	}
	return objs, nil
}

func fetchJobs(tasks chan<- object.Object, config *Config) {
	for {
		url := fmt.Sprintf("http://%s/fetch", config.Manager)
		ans, err := httpRequest(url, nil)
		if err != nil {
			logger.Errorf("fetch jobs: %s", err)
			time.Sleep(time.Second)
			continue
		}
		var jobs []object.Object
		jobs, err = unmarshalObjects(ans)
		if err != nil {
			logger.Errorf("Unmarshal %s: %s", string(ans), err)
			time.Sleep(time.Second)
			continue
		}
		logger.Debugf("got %d jobs", len(jobs))
		if len(jobs) == 0 {
			break
		}
		for _, obj := range jobs {
			tasks <- obj
		}
	}
	close(tasks)
}
