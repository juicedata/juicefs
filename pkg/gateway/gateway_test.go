/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package gateway

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	minio "github.com/minio/minio/cmd"
	xhttp "github.com/minio/minio/cmd/http"
	"github.com/minio/minio/pkg/hash"
)

func newTestGatewayObjects(t *testing.T, keepEtag bool) (*jfsObjects, string) {
	t.Helper()

	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test-bucket",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta failed: %v", err)
	}

	conf := vfs.Config{
		Meta: meta.DefaultConf(),
		Chunk: &chunk.Config{
			BlockSize:   format.BlockSize << 10,
			MaxUpload:   1,
			MaxDownload: 200,
			BufferSize:  100 << 20,
		},
		DirEntryTimeout: time.Millisecond * 100,
		EntryTimeout:    time.Millisecond * 100,
		AttrTimeout:     time.Millisecond * 100,
	}
	objStore, err := object.CreateStorage("mem", "", "", "", "")
	if err != nil {
		t.Fatalf("create storage failed: %v", err)
	}
	store := chunk.NewCachedStore(objStore, *conf.Chunk, nil)
	jfs, err := fs.NewFileSystem(&conf, m, store, nil)
	if err != nil {
		t.Fatalf("initialize filesystem failed: %s", err)
	}

	layer, err := NewJFSGateway(jfs, &conf, &Config{Bucket: format.Name, KeepEtag: keepEtag, Umask: 022})
	if err != nil {
		t.Fatalf("initialize gateway failed: %s", err)
	}
	jfsObj := layer.(*jfsObjects)
	if err := jfs.Mkdir(mctx, minio.MinioMetaBucket, 0777, 022); err != 0 {
		t.Fatalf("mkdir meta bucket failed: %s", err)
	}
	return jfsObj, format.Name
}

func mustPutObjReader(t *testing.T, data []byte) *minio.PutObjReader {
	t.Helper()
	sum := md5.Sum(data)
	hr, err := hash.NewReader(bytes.NewReader(data), int64(len(data)), hex.EncodeToString(sum[:]), "", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return minio.NewPutObjReader(hr)
}

func TestGatewayLock(t *testing.T) {
	jfsObj, _ := newTestGatewayObjects(t, false)

	rwLocker := jfsObj.NewNSLock(minio.MinioMetaBucket, minio.MinioMetaLockFile)

	if _, err := rwLocker.GetLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); err != nil {
		t.Fatalf("get lock failed: %s", err)
	}
	if _, err := rwLocker.GetLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); !errors.As(err, &minio.OperationTimedOut{}) {
		t.Fatalf("GetLock should return timeout error: %s", err)
	}
	rwLocker.Unlock()

	if _, err := rwLocker.GetRLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); err != nil {
		t.Fatalf("get lock failed: %s", err)
	}
	if _, err := rwLocker.GetRLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); err != nil {
		t.Fatalf("GetRLock should return nil: %s", err)
	}
	rwLocker.RUnlock()
	rwLocker.RUnlock()

	if _, err := rwLocker.GetLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); err != nil {
		t.Fatalf("get lock failed: %s", err)
	}
	if _, err := rwLocker.GetRLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); !errors.As(err, &minio.OperationTimedOut{}) {
		t.Fatalf("GetRLock should return timeout error: %s", err)
	}
	rwLocker.Unlock()

	if _, err := rwLocker.GetRLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); err != nil {
		t.Fatalf("GetRLock failed: %s", err)
	}
	if _, err := rwLocker.GetLock(context.Background(), minio.NewDynamicTimeout(2*time.Second, 1*time.Second)); !errors.As(err, &minio.OperationTimedOut{}) {
		t.Fatalf("GetRLock should return timeout error: %s", err)
	}
	rwLocker.RUnlock()
}

func TestConditionalPutObject(t *testing.T) {
	jfsObj, bucket := newTestGatewayObjects(t, true)
	ctx := context.Background()

	initial, err := jfsObj.PutObject(ctx, bucket, "existing.txt", mustPutObjReader(t, []byte("alpha")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put initial object: %v", err)
	}

	if _, err = jfsObj.PutObject(
		WithTargetConditions(ctx, Conditions{IfNoneMatch: []string{"*"}}),
		bucket,
		"create-only.txt",
		mustPutObjReader(t, []byte("new")),
		minio.ObjectOptions{},
	); err != nil {
		t.Fatalf("put new object with If-None-Match=* failed: %v", err)
	}

	if _, err = jfsObj.PutObject(
		WithTargetConditions(ctx, Conditions{IfNoneMatch: []string{"*"}}),
		bucket,
		"existing.txt",
		mustPutObjReader(t, []byte("beta")),
		minio.ObjectOptions{},
	); !errors.As(err, &minio.PreConditionFailed{}) {
		t.Fatalf("put existing object with If-None-Match=* should fail, got: %v", err)
	}

	if _, err = jfsObj.PutObject(
		WithTargetConditions(ctx, Conditions{IfMatch: []string{"does-not-match"}}),
		bucket,
		"existing.txt",
		mustPutObjReader(t, []byte("beta")),
		minio.ObjectOptions{},
	); !errors.As(err, &minio.PreConditionFailed{}) {
		t.Fatalf("put existing object with mismatched If-Match should fail, got: %v", err)
	}

	updated, err := jfsObj.PutObject(
		WithTargetConditions(ctx, Conditions{IfMatch: []string{initial.ETag}}),
		bucket,
		"existing.txt",
		mustPutObjReader(t, []byte("beta")),
		minio.ObjectOptions{},
	)
	if err != nil {
		t.Fatalf("put existing object with matching If-Match failed: %v", err)
	}
	if updated.ETag == initial.ETag {
		t.Fatalf("expected overwrite to produce a new ETag, got %q", updated.ETag)
	}
}

func TestConditionalDeleteObject(t *testing.T) {
	jfsObj, bucket := newTestGatewayObjects(t, true)
	ctx := context.Background()

	obj, err := jfsObj.PutObject(ctx, bucket, "delete-me.txt", mustPutObjReader(t, []byte("alpha")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put initial object: %v", err)
	}

	if _, err = jfsObj.DeleteObject(
		WithTargetConditions(ctx, Conditions{IfMatch: []string{"does-not-match"}}),
		bucket,
		"delete-me.txt",
		minio.ObjectOptions{},
	); !errors.As(err, &minio.PreConditionFailed{}) {
		t.Fatalf("delete with mismatched If-Match should fail, got: %v", err)
	}

	if _, err = jfsObj.GetObjectInfo(ctx, bucket, "delete-me.txt", minio.ObjectOptions{}); err != nil {
		t.Fatalf("object should still exist after failed delete: %v", err)
	}

	if _, err = jfsObj.DeleteObject(
		WithTargetConditions(ctx, Conditions{IfMatch: []string{obj.ETag}}),
		bucket,
		"delete-me.txt",
		minio.ObjectOptions{},
	); err != nil {
		t.Fatalf("delete with matching If-Match failed: %v", err)
	}

	if _, err = jfsObj.GetObjectInfo(ctx, bucket, "delete-me.txt", minio.ObjectOptions{}); !errors.As(err, &minio.ObjectNotFound{}) {
		t.Fatalf("expected object to be deleted, got: %v", err)
	}

	if _, err = jfsObj.DeleteObject(
		WithTargetConditions(ctx, Conditions{IfMatch: []string{obj.ETag}}),
		bucket,
		"delete-me.txt",
		minio.ObjectOptions{},
	); !errors.As(err, &minio.ObjectNotFound{}) {
		t.Fatalf("delete missing object with If-Match should report not found, got: %v", err)
	}
}

func TestConditionalCompleteMultipartUpload(t *testing.T) {
	jfsObj, bucket := newTestGatewayObjects(t, true)
	ctx := context.Background()

	existing, err := jfsObj.PutObject(ctx, bucket, "multipart.txt", mustPutObjReader(t, []byte("alpha")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put initial object: %v", err)
	}

	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, "multipart.txt", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("new multipart upload failed: %v", err)
	}
	part, err := jfsObj.PutObjectPart(ctx, bucket, "multipart.txt", uploadID, 1, mustPutObjReader(t, []byte("beta")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put multipart part failed: %v", err)
	}
	parts := []minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}

	if _, err = jfsObj.CompleteMultipartUpload(
		WithTargetConditions(ctx, Conditions{IfNoneMatch: []string{"*"}}),
		bucket,
		"multipart.txt",
		uploadID,
		parts,
		minio.ObjectOptions{},
	); !errors.As(err, &minio.PreConditionFailed{}) {
		t.Fatalf("complete multipart with If-None-Match=* should fail, got: %v", err)
	}

	info, err := jfsObj.GetObjectInfo(ctx, bucket, "multipart.txt", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("get existing object failed: %v", err)
	}
	if info.ETag != existing.ETag {
		t.Fatalf("failed complete should leave original object unchanged, got ETag %q want %q", info.ETag, existing.ETag)
	}

	uploadID, err = jfsObj.NewMultipartUpload(ctx, bucket, "multipart.txt", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("second multipart upload failed: %v", err)
	}
	part, err = jfsObj.PutObjectPart(ctx, bucket, "multipart.txt", uploadID, 1, mustPutObjReader(t, []byte("gamma")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("second put multipart part failed: %v", err)
	}

	result, err := jfsObj.CompleteMultipartUpload(
		WithTargetConditions(ctx, Conditions{IfMatch: []string{existing.ETag}}),
		bucket,
		"multipart.txt",
		uploadID,
		[]minio.CompletePart{{PartNumber: 1, ETag: part.ETag}},
		minio.ObjectOptions{},
	)
	if err != nil {
		t.Fatalf("complete multipart with matching If-Match failed: %v", err)
	}
	if result.ETag == existing.ETag {
		t.Fatalf("expected completed multipart upload to replace the original object")
	}
}

func TestConditionalRequestMiddlewareRewritesWriteFailures(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		downstream     int
		downstreamBody string
	}{
		{name: "internal-error", method: http.MethodPut, downstream: http.StatusInternalServerError, downstreamBody: `<Error><Code>InternalError</Code></Error>`},
		{name: "success-no-content", method: http.MethodDelete, downstream: http.StatusNoContent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := ConditionalRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, ok := TargetConditionsFromContext(r.Context()); !ok {
					t.Fatalf("expected conditional headers in request context")
				}
				markConditionalFailure(r.Context(), http.StatusPreconditionFailed, "PreconditionFailed", "At least one of the pre-conditions you specified did not hold")
				w.Header().Set(xhttp.AmzRequestID, "request-id")
				if tc.downstreamBody != "" {
					w.Header().Set("Content-Type", "application/xml")
				}
				w.WriteHeader(tc.downstream)
				if tc.downstreamBody != "" {
					_, _ = w.Write([]byte(tc.downstreamBody))
				}
			}))

			req := httptest.NewRequest(tc.method, "/bucket/object", nil)
			req.Header.Set(xhttp.IfNoneMatch, "*")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusPreconditionFailed {
				t.Fatalf("expected middleware to rewrite status to 412, got %d", rec.Code)
			}
			if got := rec.Header().Get(xhttp.AmzRequestID); got != "request-id" {
				t.Fatalf("expected request ID header to be preserved, got %q", got)
			}
			if body := rec.Body.String(); !strings.Contains(body, "<Code>PreconditionFailed</Code>") {
				t.Fatalf("expected rewritten S3 error body, got %q", body)
			}
		})
	}
}

func TestMain(m *testing.M) {
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	os.Exit(m.Run())
}

func newTestGateway(t *testing.T, conf Config) (*jfsObjects, *fs.FileSystem, string) {
	t.Helper()

	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %s", err)
	}
	vfsConf := vfs.Config{
		Meta: meta.DefaultConf(),
		Chunk: &chunk.Config{
			BlockSize:   format.BlockSize << 10,
			MaxUpload:   1,
			MaxDownload: 200,
			BufferSize:  100 << 20,
		},
		DirEntryTimeout: time.Millisecond * 100,
		EntryTimeout:    time.Millisecond * 100,
		AttrTimeout:     time.Millisecond * 100,
	}
	objStore, _ := object.CreateStorage("mem", "", "", "", "")
	store := chunk.NewCachedStore(objStore, *vfsConf.Chunk, nil)
	jfs, err := fs.NewFileSystem(&vfsConf, m, store, nil)
	if err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	conf.Bucket = format.Name
	if conf.Umask == 0 {
		conf.Umask = 022
	}
	jfsObj := &jfsObjects{
		fs:       jfs,
		conf:     &vfsConf,
		listPool: minio.NewTreeWalkPool(time.Minute * 30),
		gConf:    &conf,
		nsMutex:  minio.NewNSLock(false),
	}
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	return jfsObj, jfs, format.Name
}

func createTestFile(t *testing.T, jfs *fs.FileSystem, name string) {
	t.Helper()
	f, eno := jfs.Create(mctx, name, 0666, 022)
	if eno != 0 {
		t.Fatalf("create %s: %s", name, eno)
	}
	if eno = f.Close(mctx); eno != 0 {
		t.Fatalf("close %s: %s", name, eno)
	}
}

func assertHeadObject(t *testing.T, jfsObj *jfsObjects, bucket, object string, wantFound bool) {
	t.Helper()
	_, err := jfsObj.GetObjectInfo(context.Background(), bucket, object, minio.ObjectOptions{})
	if wantFound {
		if err != nil {
			t.Fatalf("head %s should succeed: %s", object, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("head %s should fail", object)
	}
	if !errors.As(err, &minio.ObjectNotFound{}) {
		t.Fatalf("head %s should return ObjectNotFound, got %T: %s", object, err, err)
	}
}

func TestGetObjectInfo(t *testing.T) {
	t.Run("head file slash fails with head dir", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{HeadDir: true})
		createTestFile(t, jfs, "/file")

		assertHeadObject(t, jfsObj, bucket, "file", true)
		assertHeadObject(t, jfsObj, bucket, "file/", false)
	})

	t.Run("put file under implicit directory", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		if eno := jfs.Mkdir(mctx, "/dir1", 0777, 022); eno != 0 {
			t.Fatalf("mkdir dir1: %s", eno)
		}
		createTestFile(t, jfs, "/dir1/key1")

		assertHeadObject(t, jfsObj, bucket, "dir1", false)
		assertHeadObject(t, jfsObj, bucket, "dir1/", false)
		assertHeadObject(t, jfsObj, bucket, "dir1/key1", true)
	})

	t.Run("put explicit directory object", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		if eno := jfs.MkdirAll(mctx, "/dir1/key1", 0777, 022); eno != 0 {
			t.Fatalf("mkdir dir1/key1: %s", eno)
		}
		jfsObj.setFileAtime("/dir1/key1", 0)

		assertHeadObject(t, jfsObj, bucket, "dir1/key1", false)
		assertHeadObject(t, jfsObj, bucket, "dir1/key1/", true)
	})

	t.Run("head dir allows implicit directories but not file slash", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{HeadDir: true})
		if eno := jfs.Mkdir(mctx, "/dir1", 0777, 022); eno != 0 {
			t.Fatalf("mkdir dir1: %s", eno)
		}
		createTestFile(t, jfs, "/dir1/key1")

		assertHeadObject(t, jfsObj, bucket, "dir1", true)
		assertHeadObject(t, jfsObj, bucket, "dir1/", true)
		assertHeadObject(t, jfsObj, bucket, "dir1/key1", true)
		assertHeadObject(t, jfsObj, bucket, "dir1/key1/", false)
	})
}
