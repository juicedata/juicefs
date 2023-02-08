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
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

// Stat has the counters to represent the progress.
type Stat struct {
	Copied       int64 // the number of copied files
	CopiedBytes  int64 // total amount of copied data in bytes
	CheckedBytes int64 // total amount of checked data in bytes
	Deleted      int64 // the number of deleted files
	Skipped      int64 // the number of files skipped
	Failed       int64 // the number of files that fail to copy
}

func updateStats(r *Stat) {
	copied.IncrInt64(r.Copied)
	copiedBytes.IncrInt64(r.CopiedBytes)
	checkedBytes.IncrInt64(r.CheckedBytes)
	deleted.IncrInt64(r.Deleted)
	skipped.IncrInt64(r.Skipped)
	failed.IncrInt64(r.Failed)
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
	return ioutil.ReadAll(resp.Body)
}

func sendStats(addr string) {
	var r Stat
	r.Copied = copied.Current()
	r.CopiedBytes = copiedBytes.Current()
	r.CheckedBytes = checkedBytes.Current()
	r.Deleted = deleted.Current()
	r.Skipped = skipped.Current()
	r.Failed = failed.Current()
	d, _ := json.Marshal(r)
	ans, err := httpRequest(fmt.Sprintf("http://%s/stats", addr), d)
	if err != nil || string(ans) != "OK" {
		logger.Errorf("update stats: %s %s", string(ans), err)
	} else {
		copied.IncrInt64(-r.Copied)
		copiedBytes.IncrInt64(-r.CopiedBytes)
		checkedBytes.IncrInt64(-r.CheckedBytes)
		deleted.IncrInt64(-r.Deleted)
		skipped.IncrInt64(-r.Skipped)
		failed.IncrInt64(-r.Failed)
	}
}

func findLocalIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", err
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue // not an ipv4 address
			}
			return ip.String(), nil
		}
	}
	return "", errors.New("are you connected to the network?")
}

func startManager(tasks <-chan object.Object) (string, error) {
	http.HandleFunc("/fetch", func(w http.ResponseWriter, req *http.Request) {
		var objs []object.Object
		obj, ok := <-tasks
		if !ok {
			_, _ = w.Write([]byte("[]"))
			return
		}
		objs = append(objs, obj)
	LOOP:
		for {
			select {
			case obj = <-tasks:
				if obj == nil {
					break LOOP
				}
				objs = append(objs, obj)
				if len(objs) > 100 {
					break LOOP
				}
			default:
				break LOOP
			}
		}
		d, err := marshalObjects(objs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logger.Debugf("send %d objects to %s", len(objs), req.RemoteAddr)
		_, _ = w.Write(d)
	})
	http.HandleFunc("/stats", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" {
			http.Error(w, "POST required", http.StatusBadRequest)
			return
		}
		d, err := ioutil.ReadAll(req.Body)
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
	ip, err := findLocalIP()
	if err != nil {
		return "", fmt.Errorf("find local ip: %s", err)
	}
	l, err := net.Listen("tcp", ip+":")
	if err != nil {
		return "", fmt.Errorf("listen: %s", err)
	}
	logger.Infof("Listen at %s", l.Addr())
	go func() { _ = http.Serve(l, nil) }()
	ps := strings.Split(l.Addr().String(), ":")
	port := ps[len(ps)-1]
	return fmt.Sprintf("%s:%s", ip, port), nil
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
			err = cmd.Run()
			if err != nil {
				// fallback to scp
				cmd = exec.Command("scp", path, host+":"+rpath)
				err = cmd.Run()
			}
			if err != nil {
				logger.Errorf("copy itself to %s: %s", host, err)
				return
			}
			// launch itself
			var args = []string{host, rpath}
			if strings.HasSuffix(path, "juicefs") {
				args = append(args, os.Args[1:]...)
				args = append(args, "--manager", address)
			} else {
				args = append(args, "--manager", address)
				args = append(args, os.Args[1:]...)
			}
			if !config.Verbose && !config.Quiet {
				args = append(args, "-q")
			}

			logger.Debugf("launch worker command args: [ssh, %s]", strings.Join(args, ", "))
			cmd = exec.Command("ssh", args...)
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
			go func() {
				r := bufio.NewReader(stderr)
				for {
					line, err := r.ReadString('\n')
					if err != nil || len(line) == 0 {
						return
					}
					println(host, line[:len(line)-1])
				}
			}()
			err = cmd.Wait()
			if err != nil {
				logger.Errorf("%s: %s", host, err)
			}
		}(host)
	}
}

func marshalObjects(objs []object.Object) ([]byte, error) {
	var arr []map[string]interface{}
	for _, o := range objs {
		arr = append(arr, object.MarshalObject(o))
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
		objs = append(objs, object.UnmarshalObject(m))
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
