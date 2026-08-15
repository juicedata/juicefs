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
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	minio "github.com/minio/minio/cmd"
	miniohash "github.com/minio/minio/pkg/hash"
)

func TestGatewayLock(t *testing.T) {
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	var conf = vfs.Config{
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
	store := chunk.NewCachedStore(objStore, *conf.Chunk, nil)
	jfs, err := fs.NewFileSystem(&conf, m, store, nil)
	if err != nil {
		t.Fatalf("initialize  failed: %s", err)
	}
	jfsObj := &jfsObjects{fs: jfs, conf: &conf, listPool: minio.NewTreeWalkPool(time.Minute * 30), gConf: &Config{Umask: 022}, nsMutex: minio.NewNSLock(false)}
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	if err := jfs.Mkdir(mctx, minio.MinioMetaBucket, 0777, 022); err != 0 {
		t.Fatalf("mkdir failed: %s", err)
	}

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

func TestMkdirAllInBucket(t *testing.T) {
	jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
	ctx := context.Background()
	const bucket = "bucket"

	if eno := jfs.Mkdir(mctx, jfsObj.path(bucket), 0777, 022); eno != 0 {
		t.Fatalf("mkdir bucket: %s", eno)
	}
	if err := jfsObj.mkdirAllInBucket(ctx, bucket, jfsObj.path(bucket, "dir", "subdir")); err != nil {
		t.Fatalf("mkdir within bucket: %s", err)
	}
	if _, eno := jfs.Stat(mctx, jfsObj.path(bucket, "dir", "subdir")); eno != 0 {
		t.Fatalf("stat directory: %s", eno)
	}

	const missingBucket = "missing-bucket"
	err := jfsObj.mkdirAllInBucket(ctx, missingBucket, jfsObj.path(missingBucket, "dir", "subdir"))
	if err == nil || !fs.IsNotExist(err) {
		t.Fatalf("mkdir under missing bucket should fail with ENOENT, got %v", err)
	}
	if _, eno := jfs.Stat(mctx, jfsObj.path(missingBucket)); !fs.IsNotExist(eno) {
		t.Fatalf("missing bucket was recreated: %s", eno)
	}
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

func newPutObjectReader(t *testing.T, data []byte) *minio.PutObjReader {
	t.Helper()
	reader, err := miniohash.NewReader(bytes.NewReader(data), int64(len(data)), "", "", int64(len(data)))
	if err != nil {
		t.Fatalf("create put object reader: %s", err)
	}
	return minio.NewPutObjReader(reader)
}

func readGatewayObject(t *testing.T, gateway *jfsObjects, bucket, object string) []byte {
	t.Helper()
	reader, err := gateway.GetObjectNInfo(context.Background(), bucket, object, nil, nil, minio.LockType(0), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("get %s: %s", object, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %s", object, err)
	}
	return data
}

func assertPreconditionFailed(t *testing.T, err error) {
	t.Helper()
	var preconditionFailed minio.PreConditionFailed
	if !errors.As(err, &preconditionFailed) {
		t.Fatalf("expected PreConditionFailed, got %T: %v", err, err)
	}
}

func TestPutObjectIfNoneMatch(t *testing.T) {
	t.Run("existing object remains unchanged", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		original := []byte("original")
		if _, err := gateway.PutObject(ctx, bucket, "object", newPutObjectReader(t, original), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create original object: %s", err)
		}

		_, err := gateway.PutObject(ctx, bucket, "object", newPutObjectReader(t, []byte("replacement")), minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
		if got := readGatewayObject(t, gateway, bucket, "object"); !bytes.Equal(got, original) {
			t.Fatalf("existing object changed: got %q, want %q", got, original)
		}
	})

	t.Run("concurrent creates have one winner", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		const writers = 16
		start := make(chan struct{})
		results := make(chan error, writers)
		payloads := make(map[string]struct{}, writers)
		readers := make([]*minio.PutObjReader, writers)
		var waitGroup sync.WaitGroup
		for i := 0; i < writers; i++ {
			payload := []byte(fmt.Sprintf("writer-%02d", i))
			payloads[string(payload)] = struct{}{}
			readers[i] = newPutObjectReader(t, payload)
			waitGroup.Add(1)
			go func(reader *minio.PutObjReader) {
				defer waitGroup.Done()
				<-start
				_, err := gateway.PutObject(ctx, bucket, "race", reader, minio.ObjectOptions{IfNoneMatch: true})
				results <- err
			}(readers[i])
		}
		close(start)
		waitGroup.Wait()
		close(results)

		succeeded := 0
		failed := 0
		for err := range results {
			if err == nil {
				succeeded++
				continue
			}
			var preconditionFailed minio.PreConditionFailed
			if errors.As(err, &preconditionFailed) {
				failed++
				continue
			}
			t.Fatalf("unexpected concurrent PUT error: %T: %v", err, err)
		}
		if succeeded != 1 || failed != writers-1 {
			t.Fatalf("conditional create results: succeeded=%d failed=%d", succeeded, failed)
		}
		stored := string(readGatewayObject(t, gateway, bucket, "race"))
		if _, ok := payloads[stored]; !ok {
			t.Fatalf("stored unexpected payload %q", stored)
		}
	})

	t.Run("directory marker can only be created once", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		opts := minio.ObjectOptions{IfNoneMatch: true}
		if _, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), opts); err != nil {
			t.Fatalf("conditionally create directory marker: %s", err)
		}
		_, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), opts)
		assertPreconditionFailed(t, err)
	})
}

func TestCompleteMultipartUploadIfNoneMatch(t *testing.T) {
	gateway, _, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	original := []byte("original")
	if _, err := gateway.PutObject(ctx, bucket, "object", newPutObjectReader(t, original), minio.ObjectOptions{}); err != nil {
		t.Fatalf("create original object: %s", err)
	}

	uploadID, err := gateway.NewMultipartUpload(ctx, bucket, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	part, err := gateway.PutObjectPart(ctx, bucket, "object", uploadID, 1, newPutObjectReader(t, []byte("replacement")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put multipart part: %s", err)
	}
	_, err = gateway.CompleteMultipartUpload(ctx, bucket, "object", uploadID,
		[]minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}, minio.ObjectOptions{IfNoneMatch: true})
	assertPreconditionFailed(t, err)
	if got := readGatewayObject(t, gateway, bucket, "object"); !bytes.Equal(got, original) {
		t.Fatalf("existing object changed: got %q, want %q", got, original)
	}

	newObject := "new-object"
	uploadID, err = gateway.NewMultipartUpload(ctx, bucket, newObject, minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload for new object: %s", err)
	}
	part, err = gateway.PutObjectPart(ctx, bucket, newObject, uploadID, 1,
		newPutObjectReader(t, []byte("created")), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put multipart part for new object: %s", err)
	}
	_, err = gateway.CompleteMultipartUpload(ctx, bucket, newObject, uploadID,
		[]minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}, minio.ObjectOptions{IfNoneMatch: true})
	if err != nil {
		t.Fatalf("conditionally complete new object: %s", err)
	}
	if got := readGatewayObject(t, gateway, bucket, newObject); !bytes.Equal(got, []byte("created")) {
		t.Fatalf("new multipart object: got %q, want %q", got, "created")
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

func TestDeleteObjects(t *testing.T) {
	t.Run("keep sibling object named as bucket", func(t *testing.T) {
		jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
		bucket := "jmrq"
		if eno := jfs.Mkdir(mctx, "/"+bucket, 0777, 022); eno != 0 {
			t.Fatalf("mkdir bucket: %s", eno)
		}
		createTestFile(t, jfs, "/"+bucket+"/"+bucket) // key == bucket name -> /jmrq/jmrq
		createTestFile(t, jfs, "/"+bucket+"/rror")

		_, errs := jfsObj.DeleteObjects(context.Background(), bucket,
			[]minio.ObjectToDelete{{ObjectName: "rror"}}, minio.ObjectOptions{})
		for _, e := range errs {
			if e != nil {
				t.Fatalf("delete rror: %s", e)
			}
		}
		assertHeadObject(t, jfsObj, bucket, bucket, true)
		assertHeadObject(t, jfsObj, bucket, "rror", false)
	})

	t.Run("keep explicit directory object parent", func(t *testing.T) {
		jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
		bucket := "bkt1"
		if eno := jfs.Mkdir(mctx, "/"+bucket, 0777, 022); eno != 0 {
			t.Fatalf("mkdir bucket: %s", eno)
		}
		if eno := jfs.MkdirAll(mctx, "/"+bucket+"/a/b", 0777, 022); eno != 0 {
			t.Fatalf("mkdir a/b: %s", eno)
		}
		jfsObj.setFileAtime("/"+bucket+"/a/b", 0) // explicit directory object "a/b/"
		createTestFile(t, jfs, "/"+bucket+"/a/b/c")

		_, errs := jfsObj.DeleteObjects(context.Background(), bucket,
			[]minio.ObjectToDelete{{ObjectName: "a/b/c"}}, minio.ObjectOptions{})
		for _, e := range errs {
			if e != nil {
				t.Fatalf("delete a/b/c: %s", e)
			}
		}
		assertHeadObject(t, jfsObj, bucket, "a/b/", true)
		assertHeadObject(t, jfsObj, bucket, "a/b/c", false)
	})

	t.Run("prune empty implicit directories", func(t *testing.T) {
		jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
		bucket := "bkt2"
		if eno := jfs.Mkdir(mctx, "/"+bucket, 0777, 022); eno != 0 {
			t.Fatalf("mkdir bucket: %s", eno)
		}
		if eno := jfs.MkdirAll(mctx, "/"+bucket+"/x/y", 0777, 022); eno != 0 {
			t.Fatalf("mkdir x/y: %s", eno)
		}
		createTestFile(t, jfs, "/"+bucket+"/x/y/z")

		_, errs := jfsObj.DeleteObjects(context.Background(), bucket,
			[]minio.ObjectToDelete{{ObjectName: "x/y/z"}}, minio.ObjectOptions{})
		for _, e := range errs {
			if e != nil {
				t.Fatalf("delete x/y/z: %s", e)
			}
		}
		if fi, eno := jfs.Stat(mctx, "/"+bucket+"/x"); eno == 0 {
			t.Fatalf("implicit dir /x should be pruned, still exists: isDir=%v", fi.IsDir())
		}
	})

	t.Run("keep sibling bucket sharing name prefix", func(t *testing.T) {
		jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
		// two buckets whose names share a string prefix ("jmrq" vs "jmrqfoo")
		if eno := jfs.Mkdir(mctx, "/jmrq", 0777, 022); eno != 0 {
			t.Fatalf("mkdir jmrq: %s", eno)
		}
		if eno := jfs.Mkdir(mctx, "/jmrqfoo", 0777, 022); eno != 0 {
			t.Fatalf("mkdir jmrqfoo: %s", eno)
		}
		createTestFile(t, jfs, "/jmrqfoo/x")

		// a traversal key against bucket jmrq must never prune the sibling bucket dir
		_, _ = jfsObj.DeleteObjects(context.Background(), "jmrq",
			[]minio.ObjectToDelete{{ObjectName: "../jmrqfoo/x"}}, minio.ObjectOptions{})
		if _, eno := jfs.Stat(mctx, "/jmrqfoo"); eno != 0 {
			t.Fatalf("sibling bucket dir /jmrqfoo must not be pruned, got: %s", eno)
		}
	})
}
