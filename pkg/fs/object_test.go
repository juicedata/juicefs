/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/stretchr/testify/require"
)

type reloadTestMeta struct {
	meta.Meta
	reload func(*meta.Format) error
}

func (m *reloadTestMeta) OnReload(fn func(*meta.Format) error) {
	m.reload = fn
}

func TestReloadableStorage(t *testing.T) {
	const storage = "reload-test"
	var created int
	var createErr error
	object.Register(storage, func(bucket, accessKey, secretKey, token string) (object.ObjectStorage, error) {
		if secretKey != "test-secret" {
			t.Errorf("storage constructor did not receive the decrypted secret key")
		}
		created++
		if createErr != nil {
			return nil, createErr
		}
		return object.CreateStorage("mem", bucket, accessKey, secretKey, token)
	})
	plain := meta.Format{Name: "vol", Storage: storage, Bucket: "original", SecretKey: "test-secret"}
	format := plain
	if err := format.Encrypt(); err != nil {
		t.Fatal(err)
	}
	bucket := "first"
	m := &reloadTestMeta{}
	blob, err := NewReloadableStorage(&format, m, func(f *meta.Format) {
		f.Bucket = bucket
		f.UploadLimit = 42
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { object.Shutdown(blob) })
	if blob.String() != "mem://first/vol/" || format.Bucket != "original" {
		t.Fatalf("initial storage %s, format bucket %q", blob, format.Bucket)
	}

	updated := plain
	if err := updated.Encrypt(); err != nil {
		t.Fatal(err)
	}
	if err := m.reload(updated.Clone()); err != nil || created != 1 {
		t.Fatalf("reloading the same credentials: %v, %d create calls", err, created)
	}

	bucket = "second"
	createErr = errors.New("temporary storage error")
	if err := m.reload(updated.Clone()); !errors.Is(err, createErr) || blob.String() != "mem://first/vol/" {
		t.Fatalf("failed reload: %v, storage %s", err, blob)
	}
	createErr = nil
	if err := m.reload(&updated); err != nil || blob.String() != "mem://second/vol/" {
		t.Fatalf("retried reload: %v, storage %s", err, blob)
	}
	if updated.UploadLimit != 42 || !updated.KeyEncrypted {
		t.Fatalf("reload callback did not preserve patched limits and encrypted credentials")
	}
}

func TestReloadableStorageConcurrentReload(t *testing.T) {
	m := &reloadTestMeta{}
	format := &meta.Format{Name: "reload-race", Storage: "mem", Bucket: "initial"}
	store, err := NewReloadableStorage(format, m, nil)
	require.NoError(t, err)
	require.NotNil(t, m.reload)

	start := make(chan struct{})
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			for range 1000 {
				_ = store.String()
				if err := store.Put(context.Background(), "key", strings.NewReader("payload")); err != nil {
					errors <- err
					return
				}
			}
		})
	}
	close(start)
	for i := range 100 {
		next := format.Clone()
		next.Bucket = fmt.Sprintf("reload-%d", i)
		require.NoError(t, m.reload(next))
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Contains(t, store.String(), "reload-99")
}

func TestReloadableStorageReloadFailure(t *testing.T) {
	m := &reloadTestMeta{}
	format := &meta.Format{Name: "reload-failure", Storage: "mem", Bucket: "initial"}
	store, err := NewReloadableStorage(format, m, nil)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, "key", strings.NewReader("original")))

	failed := format.Clone()
	failed.Storage = "unsupported-reload-storage"
	require.Error(t, m.reload(failed))
	require.Contains(t, store.String(), "initial")
	reader, err := store.Get(ctx, "key", 1, 3)
	require.NoError(t, err)
	defer reader.Close()

	next := format.Clone()
	next.Storage = "file"
	next.Bucket = t.TempDir() + "/"
	require.NoError(t, m.reload(next))
	require.NotContains(t, store.String(), "mem://")
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "rig", string(data))
	require.NoError(t, store.Put(ctx, "new", strings.NewReader("replacement")))
	info, err := store.Head(ctx, "new")
	require.NoError(t, err)
	require.EqualValues(t, len("replacement"), info.Size())
	require.NoError(t, store.Delete(ctx, "new"))
	_, err = store.Head(ctx, "new")
	require.Error(t, err)
}

func TestReloadableStorageConcurrentS3UserAgent(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	var requests, invalid atomic.Int64
	expected := "JuiceFS-" + version.Version()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodHead || !strings.Contains(r.Header.Get("User-Agent"), expected) {
			invalid.Add(1)
		}
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	format := &meta.Format{Name: "reload-agent", Storage: "s3", Bucket: server.URL + "/bucket",
		AccessKey: "test-access-key", SecretKey: "test-secret-key"}
	m := &reloadTestMeta{}
	store, err := NewReloadableStorage(format, m, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			for range 200 {
				if _, err := store.Head(ctx, "key"); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	close(start)
	for i := range 100 {
		next := format.Clone()
		next.AccessKey = fmt.Sprintf("test-access-%d", i)
		require.NoError(t, m.reload(next))
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 400, requests.Load())
	require.Zero(t, invalid.Load())
}
