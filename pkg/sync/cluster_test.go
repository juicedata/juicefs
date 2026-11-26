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
	"context"
	"errors"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/user"
	"strings"
	"syscall"
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
	addr, stop, err := startManager(&conf, todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := http.Get("http://" + addr + "/debug/pprof/cmdline"); err != nil {
		t.Fatalf("get pprof: %s", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("manager exposed /debug/pprof: status %d", resp.StatusCode)
		}
		if strings.Contains(string(body), os.Args[0]) {
			t.Fatalf("manager leaked process command line via /debug/pprof/cmdline")
		}
	}
	// sendStats(addr)
	// worker
	conf.Manager = addr
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

	// a lost worker that comes back after the manager stops must not be able to report stats
	stop()
	// drop the keep-alive connection closed by the manager, then ECONNREFUSED makes the worker exit (see sendStats)
	http.DefaultClient.CloseIdleConnections()
	if _, err := httpRequest("http://"+addr+"/stats", nil); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("manager should refuse stats after stopped, got %v", err)
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
	if !strings.HasPrefix(command, "-o ServerAliveInterval=15 -o ServerAliveCountMax=4 worker.example.com ") {
		t.Fatalf("worker command should enable ssh keepalive: %s", command)
	}
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

type failPutStore struct{ object.ObjectStorage }

func (s failPutStore) Put(ctx context.Context, key string, in io.Reader, getters ...object.AttrGetter) error {
	return errors.New("put failed")
}

type inplaceStore struct{ object.ObjectStorage }

func (s inplaceStore) Limits() object.Limits { return object.Limits{PutInplace: true} }

func TestRetryUnfinished(t *testing.T) {
	// resumed objects were handled by previous runs and restored from the checkpoint
	setup := func(name string, total, resumed int64) (src, dst object.ObjectStorage, mgr *CheckpointManager, config *Config) {
		p := utils.NewProgress(true)
		handled = p.AddCountBar("Scanned objects", 0)
		copied = p.AddCountSpinner("Copied objects")
		copiedBytes = p.AddByteSpinner("Copied bytes")
		skipped = p.AddCountSpinner("Skipped objects")
		skippedBytes = p.AddByteSpinner("Skipped bytes")
		failed = p.AddCountSpinner("Failed objects")
		deleted = p.AddCountSpinner("Deleted objects")
		checked, checkedBytes = nil, nil
		totalHandled.Store(0)
		handled.SetTotal(resumed + total)
		handled.SetCurrent(resumed)

		config = newTestConfig()
		concurrent = make(chan int, config.Threads)
		src, _ = object.CreateStorage("mem", name+"-src", "", "", "")
		dst, _ = object.CreateStorage("mem", name+"-dst", "", "", "")
		mgr = NewCheckpointManager(src, dst, config)
		return
	}
	add := func(t *testing.T, src object.ObjectStorage, mgr *CheckpointManager, keys ...string) {
		putObjects(t, src, keys...)
		for _, key := range keys {
			o, err := src.Head(ctx, key)
			if err != nil {
				t.Fatalf("head %s: %v", key, err)
			}
			mgr.AddPendingKey("", o)
		}
	}

	t.Run("retry lost and failed keys", func(t *testing.T) {
		// 2 of 10 unfinished, at the 20% limit
		src, dst, mgr, config := setup("retry-unfinished", 10, 0)
		add(t, src, mgr, "done", "done2", "lost", "fail")
		mgr.MarkCompleted("done")
		mgr.MarkCompleted("done2")
		mgr.MarkFailed("fail")
		handled.IncrInt64(9) // all but lost were handled
		failed.Increment()

		retryUnfinished(src, dst, config, mgr, 0)

		assertDstHasKeys(t, dst, "lost", "fail")
		if objs, n := mgr.unfinishedObjects(); len(objs) != 0 || n != 0 {
			t.Fatalf("expect no unfinished objects, got %d (%d failed)", len(objs), n)
		}
		if n := failed.Current(); n != 0 {
			t.Fatalf("expect 0 failed, got %d", n)
		}
		incrHandled(0)
		if handled.Current() != handled.GetTotal() {
			t.Fatalf("handled %d != total %d", handled.Current(), handled.GetTotal())
		}
	})

	t.Run("give up when more than 20% unfinished", func(t *testing.T) {
		// 2 of 9 unfinished
		src, dst, mgr, config := setup("retry-unfinished-giveup", 9, 0)
		add(t, src, mgr, "done", "lost", "fail")
		mgr.MarkCompleted("done")
		mgr.MarkFailed("fail")

		retryUnfinished(src, dst, config, mgr, 0)

		for _, key := range []string{"lost", "fail"} {
			if _, err := dst.Head(ctx, key); err == nil {
				t.Fatalf("%s should not be retried", key)
			}
		}
		if objs, n := mgr.unfinishedObjects(); len(objs) != 2 || n != 1 {
			t.Fatalf("expect 2 unfinished objects (1 failed), got %d (%d failed)", len(objs), n)
		}
	})

	t.Run("failed again", func(t *testing.T) {
		src, dst, mgr, config := setup("retry-unfinished-again", 10, 0)
		add(t, src, mgr, "done", "done2", "lost", "fail")
		mgr.MarkCompleted("done")
		mgr.MarkCompleted("done2")
		mgr.MarkFailed("fail")
		handled.IncrInt64(9)
		failed.Increment()

		retryUnfinished(src, failPutStore{dst}, config, mgr, 0)

		if n := failed.Current(); n != 2 {
			t.Fatalf("expect 2 failed, got %d", n)
		}
		incrHandled(0)
		if handled.Current() != handled.GetTotal() {
			t.Fatalf("handled %d != total %d", handled.Current(), handled.GetTotal())
		}
		state := mgr.GetOrCreatePrefixState("")
		if len(state.PendingKeys) != 0 || len(state.FailedKeys) != 2 || state.FailedKeys["lost"] == nil || state.FailedKeys["fail"] == nil {
			t.Fatalf("expect lost and fail in failed keys, got pending %v, failed %v", state.PendingKeys, state.FailedKeys)
		}
	})

	t.Run("give up based on objects in this run when resumed", func(t *testing.T) {
		// 2 of 5 unfinished in this run, but only 2 of 11 including the resumed ones
		src, dst, mgr, config := setup("retry-unfinished-resumed", 5, 6)
		add(t, src, mgr, "done", "lost", "fail")
		mgr.MarkCompleted("done")
		mgr.MarkFailed("fail")
		handled.IncrInt64(4)
		failed.Increment()

		retryUnfinished(src, dst, config, mgr, 6)

		if _, err := dst.Head(ctx, "lost"); err == nil {
			t.Fatalf("lost should not be retried")
		}
	})

	t.Run("skip when writing in place", func(t *testing.T) {
		for name, inplace := range map[string]bool{"inplace": true, "backend": false} {
			src, dst, mgr, config := setup("retry-unfinished-skip-"+name, 5, 0)
			config.Inplace = inplace
			if name == "backend" {
				dst = object.WithPrefix(inplaceStore{dst}, "sub/")
			}
			add(t, src, mgr, "done", "lost")
			mgr.MarkCompleted("done")
			handled.IncrInt64(4)

			retryUnfinished(src, dst, config, mgr, 0)

			if _, err := dst.Head(ctx, "lost"); err == nil {
				t.Fatalf("lost should not be retried with %s", name)
			}
		}
	})

	t.Run("retry with delete-src-after", func(t *testing.T) {
		src, dst, mgr, config := setup("retry-unfinished-delete-src-after", 5, 0)
		config.DeleteSrcAfter = true
		add(t, src, mgr, "done", "lost")
		mgr.MarkCompleted("done")
		handled.IncrInt64(4)

		retryUnfinished(src, dst, config, mgr, 0)

		assertDstHasKeys(t, dst, "lost")
		if _, err := src.Head(ctx, "lost"); err == nil {
			t.Fatalf("lost should be deleted from src")
		}
	})
}
