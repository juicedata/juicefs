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
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/oliverisaac/shellescape"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
)

type obj struct {
	key       string
	size      int64
	mtime     time.Time
	isDir     bool
	isSymlink bool
}

func (o *obj) Key() string          { return o.key }
func (o *obj) Size() int64          { return o.size }
func (o *obj) Mtime() time.Time     { return o.mtime }
func (o *obj) IsDir() bool          { return o.isDir }
func (o *obj) IsSymlink() bool      { return o.isSymlink }
func (o *obj) StorageClass() string { return "" }
func (o *obj) Status() string       { return "" }

type file struct {
	obj
}

type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) { return len(p), nil }

func (o *file) Owner() string     { return "" }
func (o *file) Group() string     { return "" }
func (o *file) Mode() os.FileMode { return 0 }

func TestCluster(t *testing.T) {
	// manager
	workerAddr := "127.0.0.1"
	if u, err := user.Current(); err != nil {
		logger.Warnf("Failed to get current user: %v", err)
	} else if u.Username != "" {
		workerAddr = u.Username + "@" + workerAddr
	}
	todo := make(chan object.Object, 100)
	var conf Config
	conf.Workers = []string{workerAddr}
	addr, err := startManager(&conf, todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	conf.Manager = addr
	client, err := getClusterHTTPClient(&conf)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Get("https://" + addr + "/debug/pprof/cmdline"); err != nil {
		t.Fatalf("get pprof: %s", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("manager accepted unauthenticated request: status %d", resp.StatusCode)
		}
		if strings.Contains(string(body), os.Args[0]) {
			t.Fatalf("manager leaked process command line via /debug/pprof/cmdline")
		}
	}
	req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/debug/pprof/cmdline", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+conf.Env[managerTokenEnv])
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("manager exposed authenticated pprof route: status %d", resp.StatusCode)
	}
	// worker
	mytodo := make(chan object.Object, 100)
	go fetchJobs(mytodo, &conf, nil)

	todo <- &obj{key: "test"}
	close(todo)

	obj := <-mytodo
	if obj.Key() != "test" {
		t.Fatalf("expect test but got %s", obj.Key())
	}
	if _, ok := <-mytodo; ok {
		t.Fatalf("should end")
	}
}

func TestClusterStatsRejectUnauthorizedState(t *testing.T) {
	srcDelayDelMu.Lock()
	savedDelayDel := srcDelayDel
	srcDelayDel = nil
	srcDelayDelMu.Unlock()
	t.Cleanup(func() {
		srcDelayDelMu.Lock()
		srcDelayDel = savedDelayDel
		srcDelayDelMu.Unlock()
	})

	tasks := make(chan object.Object, 1)
	config := &Config{Workers: []string{"127.0.0.1"}}
	addr, err := startManager(config, tasks, nil)
	if err != nil {
		t.Fatal(err)
	}
	config.Manager = addr
	tasks <- &obj{key: "issued", isDir: true}
	close(tasks)
	if _, err := httpRequest(config, "/fetch", nil); err != nil {
		t.Fatal(err)
	}
	client, err := getClusterHTTPClient(config)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated, err := http.NewRequest(http.MethodPost, "https://"+addr+"/stats", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(unauthenticated)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated stats returned %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	oversizedBody := io.LimitReader(endlessReader{}, maxClusterStatsSize+1)
	oversized, err := http.NewRequest(http.MethodPost, "https://"+addr+"/stats", oversizedBody)
	if err != nil {
		t.Fatal(err)
	}
	oversized.ContentLength = maxClusterStatsSize + 1
	oversized.Header.Set("Authorization", "Bearer "+config.Env[managerTokenEnv])
	oversized.Header.Set("Expect", "100-continue")
	resp, err = client.Do(oversized)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized stats returned %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if err := validateClusterStat(&Config{DeleteSrc: true}, &Stat{DelayDelDir: []string{"issued"}}, map[string]struct{}{"issued": {}}); err != nil {
		t.Fatalf("valid stats were rejected: %v", err)
	}

	for _, stat := range []Stat{
		{DelayDelDir: []string{"issued"}},
		{CompletedKeys: []string{"not-issued"}},
		{Copied: -1},
		{Copied: math.MaxInt64, Deleted: 1},
	} {
		body, err := json.Marshal(stat)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = httpRequest(config, "/stats", body); err == nil {
			t.Fatalf("manager accepted invalid stats: %+v", stat)
		}
	}

	srcDelayDelMu.Lock()
	defer srcDelayDelMu.Unlock()
	if len(srcDelayDel) != 0 {
		t.Fatalf("invalid stats scheduled source deletion: %v", srcDelayDel)
	}
}

func TestPrepareWorkerCommandRedactsSecretsInArgs(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	src := "oss://test-access-key:test-secret-key@test-bucket.oss.example.com/"
	dst := "/tmp/dst/"
	os.Args = []string{
		"/usr/local/bin/juicefs", "sync", src, dst,
		"--worker", "worker.example.com", "--debug", "--bwlimit", "10",
		"--start-time", "2026-07-13 07:25:05",
	}
	config := &Config{Env: map[string]string{
		"SECRET_KEY":     "env-secret-key",
		"SESSION_TOKEN":  "env-session-token",
		"JFS_PAGE_STACK": "1",
	}}
	config.SetClusterStorage(src, dst)

	args, payload, err := prepareWorkerCommand("worker.example.com", "10.0.0.1:12345", "/tmp/juicefs", config)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(args, " ")
	for _, secret := range []string{"test-secret-key", "env-secret-key", "env-session-token"} {
		if strings.Contains(command, secret) {
			t.Fatalf("worker command leaked %q: %s", secret, command)
		}
	}
	for _, storageURL := range []string{src, dst} {
		redacted := shellescape.Escape(utils.RemovePassword(storageURL))
		if !strings.Contains(command, redacted) {
			t.Fatalf("worker command should use redacted storage URL %q: %s", redacted, command)
		}
	}
	if strings.Contains(command, "cluster-worker-source") || strings.Contains(command, "cluster-worker-destination") {
		t.Fatalf("worker command should not use fixed storage placeholders: %s", command)
	}
	if strings.Contains(command, "--worker-config-stdin") {
		t.Fatalf("worker command should read stdin without an extra flag: %s", command)
	}
	if strings.Contains(command, "JFS_PAGE_STACK") {
		t.Fatalf("worker command should not pass JFS_PAGE_STACK separately: %s", command)
	}
	if !strings.Contains(command, `2026-07-13\ 07:25:05`) || strings.Contains(command, `2026-07-13\\\ 07:25:05`) {
		t.Fatalf("worker command should escape arguments exactly once: %s", command)
	}

	gotSrc, gotDst, env, err := ReadClusterWorkerConfig(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if gotSrc != src || gotDst != dst {
		t.Fatalf("unexpected worker storage config: %q -> %q", gotSrc, gotDst)
	}
	if env["SECRET_KEY"] != "env-secret-key" || env["SESSION_TOKEN"] != "env-session-token" {
		t.Fatalf("unexpected worker environment: %+v", env)
	}
}

func TestMultipartCheckpointFromManagerIsNotReportedAsDirty(t *testing.T) {
	uploads := newWorkerMultipartUploads()
	mtime := time.Now()
	state := &multipartUploadState{
		Upload: object.MultipartUpload{
			UploadID:    "upload-id",
			MinPartSize: 5 << 20,
			MaxCount:    10000,
		},
		Size:  maxBlock,
		Mtime: mtime,
		Parts: map[int]object.Part{
			1: {Num: 1, Size: 5 << 20, ETag: "part-1"},
		},
		Checksums: map[int]uint32{1: 123},
	}
	uploads.PutMultipartCheckpoint("large", state)

	if dirty := getMultipartUploads(uploads); dirty != nil {
		t.Fatalf("manager-provided multipart checkpoint should not be reported as dirty: %+v", dirty)
	}
	if part, chksum, ok := uploads.GetMultipartPart(uploads.uploads["large"], 1, true); !ok || part.ETag != "part-1" || chksum != 123 {
		t.Fatalf("manager-provided multipart checkpoint should remain available, part=%+v checksum=%d ok=%v", part, chksum, ok)
	}
}

func TestSentMultipartStatsClearOnlyDirtyMarks(t *testing.T) {
	uploads := newWorkerMultipartUploads()
	mtime := time.Now()
	upload := &object.MultipartUpload{UploadID: "upload-id", MinPartSize: 5 << 20, MaxCount: 10000}
	state := uploads.EnsureMultipartUploadState("large", maxBlock, mtime, 5<<20, upload)
	uploads.MarkMultipartPart("large", state, &object.Part{Num: 1, Size: 5 << 20, ETag: "part-1"}, 123, true)

	dirty := getMultipartUploads(uploads)
	state = dirty["large"]
	if len(dirty) != 1 || state == nil {
		t.Fatalf("expected dirty multipart part to be reported, got %+v", dirty)
	}
	if _, ok := state.Parts[1]; !ok {
		t.Fatalf("expected dirty multipart part to be reported, got %+v", dirty)
	}
	clearSentMultipartParts(uploads, dirty)
	if dirty := getMultipartUploads(uploads); dirty != nil {
		t.Fatalf("dirty multipart marks should be cleared after successful stats send: %+v", dirty)
	}
	if part, chksum, ok := uploads.GetMultipartPart(state, 1, true); !ok || part.ETag != "part-1" || chksum != 123 {
		t.Fatalf("clearing dirty marks should not remove local checkpoint part, part=%+v checksum=%d ok=%v", part, chksum, ok)
	}
}

func TestMarshal(t *testing.T) {
	mtime := time.Now()
	var objs = []object.Object{
		&obj{key: "test", mtime: mtime},
		withSize(&obj{key: "test1", size: 100}, -4),
		withSize(&file{obj{key: "test2", size: 200}}, -1),
		withSize(&file{obj{key: "test3", size: 200, isSymlink: true}}, -1),
	}
	d, err := marshalObjects(objs)
	if err != nil {
		t.Fatal(err)
	}
	objs2, e := unmarshalObjects(d)
	if e != nil {
		t.Fatal(e)
	}
	if objs2[0].Key() != "test" {
		t.Fatalf("expect test but got %s", objs2[0].Key())
	}
	if !objs2[0].Mtime().Equal(objs[0].Mtime()) {
		t.Fatalf("expect %s but got %s", mtime, objs2[0].Mtime())
	}
	if objs2[1].Key() != "test1" || objs2[1].Size() != -4 || withoutSize(objs2[1]).Size() != 100 {
		t.Fatalf("expect withSize but got %s", objs2[1].Key())
	}
	if objs2[2].Key() != "test2" || objs2[2].Size() != -1 || withoutSize(objs2[2]).Size() != 200 {
		t.Fatalf("expect withFSize but got %s", objs2[2].Key())
	}
	if objs2[3].Key() != "test3" || objs2[3].Size() != -1 || withoutSize(objs2[3]).Size() != 200 && objs2[3].IsSymlink() != true {
		t.Fatalf("expect withFSize but got %s", objs2[3].Key())
	}
}
