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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
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

const (
	managerTokenEnv        = "JFS_MANAGER_TOKEN"
	managerCAEnv           = "JFS_MANAGER_CA"
	maxClusterStatsSize    = 16 << 20
	maxClusterResponseSize = 64 << 20
)

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

var clusterHTTPClients sync.Map

func getClusterHTTPClient(config *Config) (*http.Client, error) {
	ca := config.Env[managerCAEnv]
	if ca == "" {
		return nil, errors.New("missing cluster manager CA")
	}
	if cached, ok := clusterHTTPClients.Load(ca); ok {
		return cached.(*http.Client), nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca)) {
		return nil, errors.New("invalid cluster manager CA")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		IdleConnTimeout:       time.Minute,
	}}
	actual, _ := clusterHTTPClients.LoadOrStore(ca, client)
	return actual.(*http.Client), nil
}

func httpRequest(config *Config, endpoint string, body []byte) (ans []byte, err error) {
	method := "GET"
	if body != nil {
		method = "POST"
	}
	req, err := http.NewRequest(method, fmt.Sprintf("https://%s%s", config.Manager, endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	token := config.Env[managerTokenEnv]
	if token == "" {
		return nil, errors.New("missing cluster manager token")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client, err := getClusterHTTPClient(config)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ans, err = io.ReadAll(io.LimitReader(resp.Body, maxClusterResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(ans) > maxClusterResponseSize {
		return nil, errors.New("cluster manager response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cluster manager returned %s: %s", resp.Status, strings.TrimSpace(string(ans)))
	}
	return ans, nil
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

func sendStats(config *Config, multipartUploads *workerMultipartUploads) {
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
	ans, err := httpRequest(config, "/stats", d)
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
		clearSentMultipartParts(multipartUploads, r.MultipartUploads)
	}
}

func generateClusterCredentials(addr string) (string, string, tls.Certificate, error) {
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return "", "", tls.Certificate{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "juicefs-sync-manager"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	return token, string(certPEM), cert, nil
}

func authenticateClusterRequest(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got := []byte(req.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func validateClusterStat(config *Config, r *Stat, issued map[string]struct{}) error {
	counters := []int64{r.Copied, r.CopiedBytes, r.Checked, r.CheckedBytes, r.Deleted, r.Skipped, r.SkippedBytes, r.Failed}
	for _, value := range counters {
		if value < 0 {
			return errors.New("negative cluster statistic")
		}
	}
	handledCounters := []int64{r.Copied, r.Deleted, r.Skipped, r.Failed}
	var handledTotal int64
	for _, value := range handledCounters {
		if value > math.MaxInt64-handledTotal {
			return errors.New("cluster statistic overflow")
		}
		handledTotal += value
	}
	if len(r.DelayDelDir) > 0 && !config.DeleteSrc && !config.DeleteSrcAfter {
		return errors.New("source deletion was not enabled")
	}
	check := func(key string) error {
		if _, ok := issued[key]; !ok {
			return fmt.Errorf("key %q was not issued to a worker", key)
		}
		return nil
	}
	for _, keys := range [][]string{r.DelayDelDir, r.CompletedKeys, r.FailedKeys} {
		for _, key := range keys {
			if err := check(key); err != nil {
				return err
			}
		}
	}
	for key := range r.MultipartUploads {
		if err := check(key); err != nil {
			return err
		}
	}
	return nil
}

func startManager(config *Config, tasks <-chan object.Object, checkpointMgr *CheckpointManager) (string, error) {
	mux := http.NewServeMux()
	var issuedMu sync.Mutex
	issued := make(map[string]struct{})
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
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
		issuedMu.Lock()
		for _, o := range objs {
			issued[o.Key()] = struct{}{}
		}
		issuedMu.Unlock()
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
		if req.ContentLength > maxClusterStatsSize {
			http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxClusterStatsSize)
		d, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		var r Stat
		err = json.Unmarshal(d, &r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		issuedMu.Lock()
		err = validateClusterStat(config, &r, issued)
		issuedMu.Unlock()
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
	token, ca, cert, err := generateClusterCredentials(l.Addr().String())
	if err != nil {
		_ = l.Close()
		return "", fmt.Errorf("generate cluster credentials: %s", err)
	}
	if config.Env == nil {
		config.Env = make(map[string]string)
	}
	config.Env[managerTokenEnv] = token
	config.Env[managerCAEnv] = ca
	logger.Infof("Listen at %s", l.Addr())
	server := &http.Server{
		Handler:           authenticateClusterRequest(token, mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	tlsListener := tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	go func() { _ = server.Serve(tlsListener) }()
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
			cmd := exec.Command("rsync", "-a", "-e", "ssh -o StrictHostKeyChecking=yes -o PasswordAuthentication=no", path, host+":"+rpath)
			output, err := cmd.CombinedOutput()
			logger.Debugf("exec: %q,err: %s", cmd.String(), string(output))
			if err != nil {
				// fallback to scp
				cmd = exec.Command("scp", "-o", "StrictHostKeyChecking=yes", "-o", "PasswordAuthentication=no", path, host+":"+rpath)
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
			cmd = exec.Command("ssh", append([]string{"-o", "StrictHostKeyChecking=yes"}, args...)...)
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

func fetchJobs(tasks chan<- object.Object, config *Config, uploads multipartUploads) {
	for {
		ans, err := httpRequest(config, "/fetch", nil)
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
			tasks <- obj
		}
	}
	close(tasks)
}
