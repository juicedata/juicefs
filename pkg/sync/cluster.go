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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	Copied           int64                            // the number of copied files
	CopiedBytes      int64                            // total amount of copied data in bytes
	Checked          int64                            // the number of checked files
	CheckedBytes     int64                            // total amount of checked data in bytes
	Deleted          int64                            // the number of deleted files
	Skipped          int64                            // the number of files skipped
	SkippedBytes     int64                            // total amount of skipped data in bytes
	Failed           int64                            // the number of files that fail to copy
	DelayDelDir      []string                         // the directories that need to be deleted
	CompletedKeys    []string                         `json:"completed_keys,omitempty"` // checkpoint: keys completed by this worker
	FailedKeys       []string                         `json:"failed_keys,omitempty"`    // checkpoint: keys failed by this worker
	MultipartUploads map[string]*multipartUploadState `json:"multipart_uploads,omitempty"`
}

const maxClusterWorkerConfigSize = 16 << 20

type clusterWorkerConfig struct {
	Source      string            `json:"source"`
	Destination string            `json:"destination"`
	Env         map[string]string `json:"env,omitempty"`
}

var completionMu sync.Mutex
var completedKeysBuf []string
var failedKeysBuf []string

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

func httpRequest(ctx context.Context, url string, body []byte) (ans []byte, err error) {
	method := "GET"
	if body != nil {
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
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

func getMultipartUploads(uploads *workerMultipartUploads) map[string]*multipartUploadState {
	if uploads == nil {
		return nil
	}
	uploads.RLock()
	defer uploads.RUnlock()
	if len(uploads.dirtyParts) == 0 {
		return nil
	}
	dirtyUploads := make(map[string]*multipartUploadState, len(uploads.dirtyParts))
	for key, dirtyParts := range uploads.dirtyParts {
		state := uploads.uploads[key]
		if !state.isValid() || len(dirtyParts) == 0 {
			continue
		}
		parts := make(map[int]object.Part, len(dirtyParts))
		var checksums map[int]uint32
		for num := range dirtyParts {
			part, ok := state.Parts[num]
			if !ok {
				continue
			}
			parts[num] = part
			if chksum, ok := state.Checksums[num]; ok {
				if checksums == nil {
					checksums = make(map[int]uint32)
				}
				checksums[num] = chksum
			}
		}
		if len(parts) == 0 {
			continue
		}
		dirtyUploads[key] = &multipartUploadState{
			Upload:    state.Upload,
			Size:      state.Size,
			Mtime:     state.Mtime,
			Parts:     parts,
			Checksums: checksums,
		}
	}
	if len(dirtyUploads) == 0 {
		return nil
	}
	return dirtyUploads
}

func clearSentMultipartParts(workerUploads *workerMultipartUploads, uploads map[string]*multipartUploadState) {
	if workerUploads == nil || len(uploads) == 0 {
		return
	}
	workerUploads.Lock()
	defer workerUploads.Unlock()
	for key, sent := range uploads {
		dirtyParts := workerUploads.dirtyParts[key]
		if dirtyParts == nil {
			continue
		}
		for num := range sent.Parts {
			delete(dirtyParts, num)
		}
		if len(dirtyParts) == 0 {
			delete(workerUploads.dirtyParts, key)
		}
	}
}

func sendStats(ctx context.Context, addr string, multipartUploads *workerMultipartUploads) bool {
	sendStatMu.Lock()
	defer sendStatMu.Unlock()
	var r Stat
	r.Skipped = skipped.Current()
	r.SkippedBytes = skippedBytes.Current()
	r.Copied = copied.Current()
	r.CopiedBytes = copiedBytes.Current()
	srcDelayDelMu.Lock()
	r.DelayDelDir = srcDelayDel
	srcDelayDel = make([]string, 0)
	srcDelayDelMu.Unlock()
	completionMu.Lock()
	r.CompletedKeys = completedKeysBuf
	r.FailedKeys = failedKeysBuf
	completedKeysBuf = make([]string, 0)
	failedKeysBuf = make([]string, 0)
	completionMu.Unlock()
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
	r.MultipartUploads = getMultipartUploads(multipartUploads)
	d, _ := json.Marshal(r)
	ans, err := httpRequest(ctx, fmt.Sprintf("http://%s/stats", addr), d)
	if err != nil || string(ans) != "OK" {
		srcDelayDelMu.Lock()
		srcDelayDel = append(srcDelayDel, r.DelayDelDir...)
		srcDelayDelMu.Unlock()
		completionMu.Lock()
		completedKeysBuf = append(r.CompletedKeys, completedKeysBuf...)
		failedKeysBuf = append(r.FailedKeys, failedKeysBuf...)
		completionMu.Unlock()
		if errors.Is(err, syscall.ECONNREFUSED) {
			logger.Errorf("the management process has been stopped, so the worker process now exits")
			return false
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				logger.Errorf("update stats timed out: %s", err)
			}
			return false
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
		clearSentMultipartParts(multipartUploads, r.MultipartUploads)
	}
	return true
}

func startManager(ctx context.Context, config *Config, tasks <-chan object.Object, checkpointMgr *CheckpointManager) (string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, req *http.Request) {
		var objs []object.Object
		var total int64
		var obj object.Object
		var ok bool
		select {
		case obj, ok = <-tasks:
			if !ok {
				_, _ = w.Write([]byte("[]"))
				return
			}
		case <-ctx.Done():
			_, _ = w.Write([]byte("[]"))
			return
		case <-req.Context().Done():
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
			case <-ctx.Done():
				break LOOP
			default:
				break LOOP
			}
		}
		if checkpointMgr != nil {
			for i, o := range objs {
				nsize := o.Size()
				base := withoutSize(o)
				if base.Size() > multipartCheckpointThreshold && (nsize == base.Size() || nsize == markChecksum) {
					if cp := checkpointMgr.GetMultipartCheckpoint(base.Key(), base.Size(), base.Mtime()); cp != nil {
						objs[i] = withMultipart(o, cp)
					}
				}
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
	mux.HandleFunc("/stats", func(w http.ResponseWriter, req *http.Request) {
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
		srcDelayDelMu.Lock()
		srcDelayDel = append(srcDelayDel, r.DelayDelDir...)
		srcDelayDelMu.Unlock()
		if checkpointMgr != nil {
			for key, state := range r.MultipartUploads {
				checkpointMgr.PutMultipartCheckpoint(key, state)
			}
			for _, key := range r.CompletedKeys {
				checkpointMgr.MarkCompleted(key)
			}
			for _, key := range r.FailedKeys {
				checkpointMgr.MarkFailed(key)
			}
		}
		logger.Debugf("receive stats %+v from %s", r, req.RemoteAddr)
		_, _ = w.Write([]byte("OK"))
	})
	var addr string
	u, err := url.Parse("ssh://" + config.Workers[0])
	if err != nil {
		return "", fmt.Errorf("invalid worker address %s: %s", config.Workers[0], err)
	}
	if config.ManagerAddr != "" {
		addr = config.ManagerAddr
		if strings.HasPrefix(addr, ":") || strings.Contains(addr, "0.0.0.0") {
			ip, err := utils.GetLocalIp(net.JoinHostPort(u.Host, "22"))
			if err != nil {
				return "", fmt.Errorf("get local ip: %s", err)
			}
			addr = ip + addr
		}
	} else {
		ip, err := utils.GetLocalIp(net.JoinHostPort(u.Host, "22"))
		if err != nil {
			return "", fmt.Errorf("not found local ip: %s", err)
		}
		logger.Debugf("Use local ip %s", ip)
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
	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := cleanupCtx(ctx)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	go func() { _ = srv.Serve(l) }()
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

func prepareWorkerCommand(host, address, path string, config *Config) ([]string, []byte, error) {
	workerArgs := append([]string(nil), os.Args[1:]...)
	var foundSource, foundDestination bool
	for i, arg := range workerArgs {
		if arg == config.clusterSource {
			workerArgs[i] = utils.RemovePassword(config.clusterSource)
			foundSource = true
		}
		if arg == config.clusterDestination {
			workerArgs[i] = utils.RemovePassword(config.clusterDestination)
			foundDestination = true
		}
	}
	if !foundSource || !foundDestination {
		return nil, nil, fmt.Errorf("can't locate source or destination in command arguments")
	}

	payload, err := json.Marshal(clusterWorkerConfig{
		Source:      config.clusterSource,
		Destination: config.clusterDestination,
		Env:         config.Env,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal worker config: %s", err)
	}

	args := []string{host, path}
	args = append(args, workerArgs...)
	args = append(args, "--manager", address)
	if !config.Verbose && !config.Quiet {
		args = append(args, "-q")
	}
	return shellescape.EscapeArgs(args), payload, nil
}

// ReadClusterWorkerConfig reads storage URLs and environment variables from worker stdin.
func ReadClusterWorkerConfig(r io.Reader) (string, string, map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxClusterWorkerConfigSize+1))
	if err != nil {
		return "", "", nil, fmt.Errorf("read worker config: %s", err)
	}
	if len(data) > maxClusterWorkerConfigSize {
		return "", "", nil, fmt.Errorf("worker config is too large")
	}
	var config clusterWorkerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", "", nil, fmt.Errorf("unmarshal worker config: %s", err)
	}
	if config.Source == "" || config.Destination == "" {
		return "", "", nil, fmt.Errorf("worker config is missing source or destination")
	}
	return config.Source, config.Destination, config.Env, nil
}

func launchWorker(ctx context.Context, address string, config *Config, wg *sync.WaitGroup) {
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
			cmd := exec.CommandContext(ctx, "rsync", "-a", "-e", "ssh -o StrictHostKeyChecking=no -o PasswordAuthentication=no", path, host+":"+rpath)
			output, err := cmd.CombinedOutput()
			logger.Debugf("exec: %q,err: %s", cmd.String(), string(output))
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// fallback to scp
				cmd = exec.CommandContext(ctx, "scp", "-o", "StrictHostKeyChecking=no", "-o", "PasswordAuthentication=no", path, host+":"+rpath)
				output, err = cmd.CombinedOutput()
				logger.Debugf("exec: %q,err: %s", cmd.String(), string(output))
			}
			if err != nil {
				logger.Errorf("copy itself to %q: %s", host, err)
				return
			}
			// launch itself
			args, payload, err := prepareWorkerCommand(host, address, rpath, config)
			if err != nil {
				logger.Errorf("prepare worker command for %q: %s", host, err)
				return
			}
			defer clear(payload)
			logger.Debugf("launch worker command args: [ssh, %q]", strings.Join(args, ", "))
			cmd = exec.CommandContext(ctx, "ssh", args...)
			cmd.Stdin = bytes.NewReader(payload)
			stderr, err := cmd.StderrPipe()
			if err != nil {
				logger.Errorf("redirect stderr: %s", err)
			}
			err = cmd.Start()
			if err != nil {
				logger.Errorf("start itself at %q: %s", host, err)
				return
			}
			logger.Infof("launch a worker on %q", host)
			var finished = make(chan struct{})
			var logRe = regexp.MustCompile(`^.*<([A-Z]+)>: (.*)`)
			go func() {
				r := bufio.NewReader(stderr)
				for {
					line, err := r.ReadString('\n')
					if err != nil || len(line) == 0 {
						finished <- struct{}{}
						return
					}
					line = strings.TrimSuffix(line, "\n")

					var level, content string
					if matches := logRe.FindStringSubmatch(line); len(matches) >= 3 {
						level = matches[1]
						content = matches[2]
					} else {
						level = "INFO"
						content = line
					}

					switch level {
					case "ERROR":
						logger.Errorf("[%q] %s", host, content)
					case "WARNING":
						logger.Warnf("[%q] %s", host, content)
					case "DEBUG":
						logger.Debugf("[%q] %s", host, content)
					default:
						logger.Infof("[%q] %s", host, content)
					}
				}
			}()
			err = cmd.Wait()
			<-finished
			if err != nil {
				logger.Errorf("%q: %s", host, err)
			}
		}(host)
	}
}

func marshalObjects(objs []object.Object) ([]byte, error) {
	var arr []map[string]interface{}
	for _, o := range objs {
		cp := multipartCheckpoint(o)
		o = withoutMultipart(o)
		nsize := o.Size()
		o = withoutSize(o)
		obj := object.MarshalObject(o)
		if nsize != o.Size() {
			obj["nsize"] = nsize
		}
		if cp != nil {
			obj["multipart_checkpoint"] = cp
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
			obj = withSize(obj, int64(nsize.(float64)))
		}
		if cpRaw, ok := m["multipart_checkpoint"]; ok {
			cpBytes, err := json.Marshal(cpRaw)
			if err == nil {
				var cp multipartUploadState
				if err := json.Unmarshal(cpBytes, &cp); err == nil {
					obj = withMultipart(obj, &cp)
				}
			}
		}
		objs = append(objs, obj)
	}
	return objs, nil
}

func fetchJobs(ctx context.Context, tasks chan<- object.Object, config *Config, uploads multipartUploads) {
	defer close(tasks)
	for {
		if ctx.Err() != nil {
			return
		}
		url := fmt.Sprintf("http://%s/fetch", config.Manager)
		ans, err := httpRequest(ctx, url, nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Errorf("fetch jobs: %s", err)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		var jobs []object.Object
		jobs, err = unmarshalObjects(ans)
		if err != nil {
			logger.Errorf("Unmarshal %s: %s", string(ans), err)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		logger.Debugf("got %d jobs", len(jobs))
		if len(jobs) == 0 {
			logger.Infof("no more jobs")
			break
		}
		for _, obj := range jobs {
			if cp := multipartCheckpoint(obj); cp != nil {
				if uploads != nil {
					uploads.PutMultipartCheckpoint(obj.Key(), cp)
				}
				obj = withoutMultipart(obj)
			}
			select {
			case tasks <- obj:
			case <-ctx.Done():
				return
			}
		}
	}
}
