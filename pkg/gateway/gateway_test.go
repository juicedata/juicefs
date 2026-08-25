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
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/hash"
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

func newTestMultiBucketGateway(t *testing.T) (*jfsObjects, *fs.FileSystem) {
	t.Helper()
	jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
	if eno := jfs.Mkdir(mctx, minio.MinioMetaBucket, 0777, 022); eno != 0 {
		t.Fatalf("mkdir %s: %s", minio.MinioMetaBucket, eno)
	}
	return jfsObj, jfs
}

func makeTestBucket(t *testing.T, jfsObj *jfsObjects, bucket string) {
	t.Helper()
	if err := jfsObj.MakeBucketWithLocation(context.Background(), bucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("make bucket %s: %s", bucket, err)
	}
}

func assertExists(t *testing.T, jfs *fs.FileSystem, p string, want bool) {
	t.Helper()
	_, eno := jfs.Stat(mctx, p)
	if want && eno != 0 {
		t.Fatalf("%s should exist, got %s", p, eno)
	}
	if !want && eno == 0 {
		t.Fatalf("%s should not exist", p)
	}
}

func bucketMetaPath(bucket string) string {
	return "/" + minio.MinioMetaBucket + "/" + minio.BucketMetaPrefix + "/" + bucket + "/" + minio.BucketMetadataFile
}

func TestDeleteBucket(t *testing.T) {
	ctx := context.Background()

	t.Run("non-empty bucket fails without side effects", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		createTestFile(t, jfs, "/bucket1/key")
		assertExists(t, jfs, bucketMetaPath("bucket1"), true)

		err := jfsObj.DeleteBucket(ctx, "bucket1", false)
		if err == nil {
			t.Fatal("DeleteBucket on a non-empty bucket should fail")
		}
		if !errors.As(err, &minio.BucketNotEmpty{}) {
			t.Fatalf("expected BucketNotEmpty, got %T: %s", err, err)
		}
		assertExists(t, jfs, "/bucket1", true)
		assertExists(t, jfs, "/bucket1/key", true)
		assertExists(t, jfs, bucketMetaPath("bucket1"), true)
	})

	t.Run("missing bucket fails without side effects", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		if err := jfsObj.DeleteBucket(ctx, "bucket2", false); err == nil {
			t.Fatal("DeleteBucket on a missing bucket should fail")
		} else if !errors.As(err, &minio.BucketNotFound{}) {
			t.Fatalf("expected BucketNotFound, got %T: %s", err, err)
		}
		assertExists(t, jfs, bucketMetaPath("bucket1"), true)
	})

	t.Run("empty bucket is removed with its staging tree", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		uploadID, err := jfsObj.NewMultipartUpload(ctx, "bucket1", "obj", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("new multipart upload: %s", err)
		}
		assertExists(t, jfs, jfsObj.upath("bucket1", uploadID), true)

		if err := jfsObj.DeleteBucket(ctx, "bucket1", false); err != nil {
			t.Fatalf("DeleteBucket should succeed: %s", err)
		}
		assertExists(t, jfs, "/bucket1", false)
		assertExists(t, jfs, bucketMetaPath("bucket1"), false)
		assertExists(t, jfs, "/"+metaBucket+"/bucket1", false)
	})

	t.Run("gc reclaims staging dir of a deleted bucket", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		orphan := "/" + metaBucket + "/ghost/uploads/abc"
		if eno := jfs.MkdirAll(mctx, orphan, 0777, 022); eno != 0 {
			t.Fatalf("mkdir %s: %s", orphan, eno)
		}
		old := time.Now().Add(-8 * 24 * time.Hour).Unix()
		for _, p := range []string{orphan, "/" + metaBucket + "/ghost/uploads"} {
			f, eno := jfs.Open(mctx, p, 0)
			if eno != 0 {
				t.Fatalf("open %s: %s", p, eno)
			}
			if eno := f.Utime(mctx, old*1000, old*1000); eno != 0 {
				t.Fatalf("utime %s: %s", p, eno)
			}
			f.Close(mctx)
		}

		jfsObj.cleanupOnce()

		assertExists(t, jfs, orphan, false)
		assertExists(t, jfs, "/"+metaBucket+"/ghost", false)
	})
}

type hookReader struct {
	io.Reader
	once sync.Once
	fn   func()
}

func (h *hookReader) Read(p []byte) (int, error) {
	h.once.Do(h.fn)
	return h.Reader.Read(p)
}

func newPutReader(t *testing.T, data []byte, fn func()) *minio.PutObjReader {
	t.Helper()
	var r io.Reader = bytes.NewReader(data)
	if fn != nil {
		r = &hookReader{Reader: r, fn: fn}
	}
	hr, err := hash.NewReader(r, int64(len(data)), "", "", int64(len(data)))
	if err != nil {
		t.Fatalf("hash reader: %s", err)
	}
	return minio.NewPutObjReader(hr)
}

func TestMkdirAllUnder(t *testing.T) {
	ctx := context.Background()

	t.Run("creates nested dirs below root", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		if err := jfsObj.mkdirAllUnder(ctx, "/bucket1", "/bucket1/a/b/c"); err != nil {
			t.Fatalf("mkdirAllUnder: %s", err)
		}
		assertExists(t, jfs, "/bucket1/a/b/c", true)
	})

	t.Run("never creates the root itself", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		err := jfsObj.mkdirAllUnder(ctx, "/gone", "/gone/a/b")
		if !errors.Is(err, errRootGone) {
			t.Fatalf("expected errRootGone, got %v", err)
		}
		assertExists(t, jfs, "/gone", false)
		assertExists(t, jfs, "/gone/a/b", false)
	})

	t.Run("never creates the root spelled with a trailing slash", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		root := jfsObj.path("bucket1")
		p := jfsObj.path("bucket1", "/") // what PutObject builds for the key "/"
		if p != root+sep {
			t.Fatalf("expected %q, got %q", root+sep, p)
		}
		if err := jfsObj.mkdirAllUnder(ctx, root, p); !errors.Is(err, errRootGone) {
			t.Fatalf("expected errRootGone, got %v", err)
		}
		assertExists(t, jfs, "/bucket1", false)
	})

	t.Run("root itself is only checked", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		if err := jfsObj.mkdirAllUnder(ctx, "/bucket1", "/bucket1"); err != nil {
			t.Fatalf("mkdirAllUnder on root: %s", err)
		}
		assertExists(t, jfs, "/bucket1", true)
	})
}

func TestPutObjectDoesNotResurrectBucket(t *testing.T) {
	ctx := context.Background()

	t.Run("put concurrent with delete must not double-succeed", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")

		var delErr error
		r := newPutReader(t, []byte("hello world"), func() {
			delErr = jfsObj.DeleteBucket(ctx, "bucket1", false)
		})

		_, err := jfsObj.PutObject(ctx, "bucket1", "key", r, minio.ObjectOptions{})
		if delErr != nil {
			t.Fatalf("DeleteBucket should have succeeded: %s", delErr)
		}
		if err == nil {
			t.Fatal("PutObject and DeleteBucket both succeeded: the bucket was resurrected")
		}
		if !errors.As(err, &minio.BucketNotFound{}) {
			t.Fatalf("expected BucketNotFound, got %T: %s", err, err)
		}
		assertExists(t, jfs, "/bucket1", false)
		assertExists(t, jfs, "/bucket1/key", false)
	})

	t.Run("nested key can be rewritten after its prefix was removed", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")

		if _, err := jfsObj.PutObject(ctx, "bucket1", "a/b/key1",
			newPutReader(t, []byte("one"), nil), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put a/b/key1: %s", err)
		}
		if _, err := jfsObj.DeleteObject(ctx, "bucket1", "a/b/key1", minio.ObjectOptions{}); err != nil {
			t.Fatalf("delete a/b/key1: %s", err)
		}
		assertExists(t, jfs, "/bucket1/a/b", false) // the empty prefix is gone
		assertExists(t, jfs, "/bucket1", true)      // the bucket is not

		if _, err := jfsObj.PutObject(ctx, "bucket1", "a/b/key2",
			newPutReader(t, []byte("two"), nil), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put a/b/key2 into an existing bucket must succeed, got %T: %s", err, err)
		}
		assertExists(t, jfs, "/bucket1/a/b/key2", true)
	})

	t.Run("upload part after bucket deletion must not recreate upload dir", func(t *testing.T) {
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		uploadID, err := jfsObj.NewMultipartUpload(ctx, "bucket1", "obj", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("new multipart upload: %s", err)
		}
		if err := jfsObj.DeleteBucket(ctx, "bucket1", false); err != nil {
			t.Fatalf("DeleteBucket: %s", err)
		}

		_, err = jfsObj.PutObjectPart(ctx, "bucket1", "obj", uploadID, 1,
			newPutReader(t, []byte("part"), nil), minio.ObjectOptions{})
		if err == nil {
			t.Fatal("PutObjectPart should fail after the bucket was deleted")
		}
		assertExists(t, jfs, "/"+metaBucket+"/bucket1", false)
		assertExists(t, jfs, "/bucket1", false)
	})
}

func TestBucketLock(t *testing.T) {
	ctx := context.Background()

	t.Run("shared locks do not exclude each other", func(t *testing.T) {
		jfsObj, _ := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")

		var releases []func()
		for i := 0; i < 5; i++ {
			release, err := jfsObj.rLockBucket(ctx, "bucket1")
			if err != nil {
				t.Fatalf("rLockBucket %d: %s", i, err)
			}
			releases = append(releases, release)
		}
		bl := jfsObj.bucketLocks.get("bucket1")
		if bl.readers != 5 {
			t.Fatalf("expected 5 readers, got %d", bl.readers)
		}
		for _, release := range releases {
			release()
		}
		if bl.readers != 0 {
			t.Fatalf("expected 0 readers after release, got %d", bl.readers)
		}
	})

	t.Run("exclusive lock waits for a publisher", func(t *testing.T) {
		jfsObj, _ := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")

		release, err := jfsObj.rLockBucket(ctx, "bucket1")
		if err != nil {
			t.Fatalf("rLockBucket: %s", err)
		}
		var once sync.Once
		releaseOnce := func() { once.Do(release) }

		locked := make(chan struct{})
		var wg sync.WaitGroup
		defer wg.Wait()
		defer releaseOnce()

		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := jfsObj.lockBucket(ctx, "bucket1")
			if err != nil {
				return
			}
			close(locked)
			unlock()
		}()

		select {
		case <-locked:
			t.Fatal("lockBucket must not be granted while a publisher holds the bucket")
		case <-time.After(200 * time.Millisecond):
		}

		releaseOnce()
		select {
		case <-locked:
		case <-time.After(5 * time.Second):
			t.Fatal("lockBucket was not granted after the publisher released")
		}
	})

	t.Run("locks are per bucket", func(t *testing.T) {
		jfsObj, _ := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		makeTestBucket(t, jfsObj, "bucket2")

		release, err := jfsObj.rLockBucket(ctx, "bucket1")
		if err != nil {
			t.Fatalf("rLockBucket: %s", err)
		}
		defer release()

		done := make(chan error, 1)
		var wg sync.WaitGroup
		defer wg.Wait()
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := jfsObj.lockBucket(ctx, "bucket2")
			if err == nil {
				unlock()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("lockBucket on another bucket: %s", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("lockBucket on another bucket must not block")
		}
	})

	t.Run("no-op in single bucket mode", func(t *testing.T) {
		jfsObj, _, bucket := newTestGateway(t, Config{})
		release, err := jfsObj.rLockBucket(ctx, bucket)
		if err != nil {
			t.Fatalf("rLockBucket: %s", err)
		}
		release()
		unlock, err := jfsObj.lockBucket(ctx, bucket)
		if err != nil {
			t.Fatalf("lockBucket: %s", err)
		}
		unlock()
		if _, eno := jfsObj.fs.Stat(mctx, bucketLockDir); eno == 0 {
			t.Fatal("single bucket mode must not create lock files")
		}
	})

	// NOTE: memkv, Redis and SQLite serialize Rmdir against Rename on their own,
	// so this only discriminates on MySQL, PostgreSQL or TiKV.
	t.Run("put and delete never both succeed", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			jfsObj, jfs := newTestMultiBucketGateway(t)
			makeTestBucket(t, jfsObj, "bucket1")

			var putErr, delErr error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, putErr = jfsObj.PutObject(ctx, "bucket1", "key",
					newPutReader(t, []byte("data"), nil), minio.ObjectOptions{})
			}()
			go func() {
				defer wg.Done()
				<-start
				delErr = jfsObj.DeleteBucket(ctx, "bucket1", false)
			}()
			close(start)
			wg.Wait()

			if putErr == nil && delErr == nil {
				t.Fatalf("iter %d: PutObject and DeleteBucket both succeeded", i)
			}
			if delErr == nil {
				if _, eno := jfs.Stat(mctx, "/bucket1/key"); eno == 0 {
					t.Fatalf("iter %d: bucket deleted but object survived", i)
				}
				if putErr == nil {
					t.Fatalf("iter %d: object published into a deleted bucket", i)
				}
				if !errors.As(putErr, &minio.BucketNotFound{}) {
					t.Fatalf("iter %d: expected BucketNotFound, got %T: %s", i, putErr, putErr)
				}
			}
		}
	})
}

func completeTestUpload(t *testing.T, jfsObj *jfsObjects, bucket, object, uploadID string, etag string) error {
	t.Helper()
	_, err := jfsObj.CompleteMultipartUpload(context.Background(), bucket, object, uploadID,
		[]minio.CompletePart{{PartNumber: 1, ETag: etag}}, minio.ObjectOptions{})
	return err
}

func TestMultipartCompleteVsAbort(t *testing.T) {
	ctx := context.Background()

	newUpload := func(t *testing.T) (*jfsObjects, *fs.FileSystem, string, string) {
		t.Helper()
		jfsObj, jfs := newTestMultiBucketGateway(t)
		makeTestBucket(t, jfsObj, "bucket1")
		uploadID, err := jfsObj.NewMultipartUpload(ctx, "bucket1", "obj", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("new multipart upload: %s", err)
		}
		info, err := jfsObj.PutObjectPart(ctx, "bucket1", "obj", uploadID, 1,
			newPutReader(t, []byte("hello"), nil), minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("put part: %s", err)
		}
		return jfsObj, jfs, uploadID, info.ETag
	}

	t.Run("abort after complete reports NoSuchUpload", func(t *testing.T) {
		jfsObj, jfs, uploadID, etag := newUpload(t)
		if err := completeTestUpload(t, jfsObj, "bucket1", "obj", uploadID, etag); err != nil {
			t.Fatalf("complete: %s", err)
		}
		assertExists(t, jfs, "/bucket1/obj", true)

		err := jfsObj.AbortMultipartUpload(ctx, "bucket1", "obj", uploadID, minio.ObjectOptions{})
		if err == nil {
			t.Fatal("abort must not succeed once the upload was completed")
		}
		assertExists(t, jfs, "/bucket1/obj", true) // the published object survives
	})

	t.Run("complete after abort reports NoSuchUpload", func(t *testing.T) {
		jfsObj, jfs, uploadID, etag := newUpload(t)
		if err := jfsObj.AbortMultipartUpload(ctx, "bucket1", "obj", uploadID, minio.ObjectOptions{}); err != nil {
			t.Fatalf("abort: %s", err)
		}
		if err := completeTestUpload(t, jfsObj, "bucket1", "obj", uploadID, etag); err == nil {
			t.Fatal("complete must not succeed once the upload was aborted")
		}
		assertExists(t, jfs, "/bucket1/obj", false)
	})

	t.Run("concurrent complete and abort never both succeed", func(t *testing.T) {
		for i := 0; i < 30; i++ {
			jfsObj, jfs, uploadID, etag := newUpload(t)

			var completeErr, abortErr error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				completeErr = completeTestUpload(t, jfsObj, "bucket1", "obj", uploadID, etag)
			}()
			go func() {
				defer wg.Done()
				<-start
				abortErr = jfsObj.AbortMultipartUpload(ctx, "bucket1", "obj", uploadID, minio.ObjectOptions{})
			}()
			close(start)
			wg.Wait()

			if completeErr == nil && abortErr == nil {
				t.Fatalf("iter %d: complete and abort both succeeded", i)
			}
			if abortErr == nil {
				if _, eno := jfs.Stat(mctx, "/bucket1/obj"); eno == 0 {
					t.Fatalf("iter %d: abort succeeded but the object was published", i)
				}
			}
		}
	})
}

func TestBucketLifecycleConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("create waits while a delete holds the bucket", func(t *testing.T) {
		jfsObj, _ := newTestMultiBucketGateway(t)

		unlock, err := jfsObj.lockBucket(ctx, "bucket1")
		if err != nil {
			t.Fatalf("lockBucket: %s", err)
		}
		var once sync.Once
		unlockOnce := func() { once.Do(unlock) }

		done := make(chan error, 1)
		var wg sync.WaitGroup
		defer wg.Wait()
		defer unlockOnce()
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- jfsObj.MakeBucketWithLocation(ctx, "bucket1", minio.BucketOptions{})
		}()

		select {
		case <-done:
			t.Fatal("MakeBucket must not run while a delete holds the bucket")
		case <-time.After(200 * time.Millisecond):
		}

		unlockOnce()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("MakeBucket after release: %s", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("MakeBucket did not proceed after the delete released the bucket")
		}
	})

	t.Run("upload registered around delete leaves nothing behind", func(t *testing.T) {
		for i := 0; i < 30; i++ {
			jfsObj, jfs := newTestMultiBucketGateway(t)
			makeTestBucket(t, jfsObj, "bucket1")

			var delErr, mpuErr error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				delErr = jfsObj.DeleteBucket(ctx, "bucket1", false)
			}()
			go func() {
				defer wg.Done()
				<-start
				_, mpuErr = jfsObj.NewMultipartUpload(ctx, "bucket1", "obj", minio.ObjectOptions{})
			}()
			close(start)
			wg.Wait()

			if delErr != nil {
				continue // the bucket survived, the upload is legitimate
			}
			if _, eno := jfs.Stat(mctx, "/"+metaBucket+"/bucket1"); eno == 0 {
				t.Fatalf("iter %d: bucket deleted (mpu=%v) but its staging tree survived", i, mpuErr)
			}
		}
	})
}

func TestBucketLockOwnerIsPerProcess(t *testing.T) {
	if bucketLockOwner <= 1 {
		t.Fatalf("bucket lock owner must be generated per process, got %d", bucketLockOwner)
	}

	jfsObj, _ := newTestMultiBucketGateway(t)
	makeTestBucket(t, jfsObj, "bucket1")

	release, err := jfsObj.rLockBucket(context.Background(), "bucket1")
	if err != nil {
		t.Fatalf("rLockBucket: %s", err)
	}
	defer release()
	bl := jfsObj.bucketLocks.get("bucket1")

	other := bucketLockOwner ^ 0xdeadbeef
	if eno := jfsObj.fs.Meta().Flock(mctx, bl.inode, other, meta.F_WRLCK, false); eno == 0 {
		_ = jfsObj.fs.Meta().Flock(mctx, bl.inode, other, meta.F_UNLCK, false)
		t.Fatal("a different owner must not get an exclusive lock while we hold a shared one")
	} else if !errors.Is(eno, syscall.EAGAIN) {
		t.Fatalf("expected EAGAIN, got %s", eno)
	}

	if eno := jfsObj.fs.Meta().Flock(mctx, bl.inode, bucketLockOwner, meta.F_WRLCK, false); eno != 0 {
		t.Fatalf("same owner should have been granted the lock, got %s", eno)
	}
	if eno := jfsObj.fs.Meta().Flock(mctx, bl.inode, bucketLockOwner, meta.F_RDLCK, false); eno != 0 {
		t.Fatalf("restore shared lock: %s", eno)
	}
}

func TestBucketLockFilesAreInternal(t *testing.T) {
	ctx := context.Background()
	jfsObj, jfs := newTestMultiBucketGateway(t)
	makeTestBucket(t, jfsObj, "bucket1")

	release, err := jfsObj.rLockBucket(ctx, "bucket1")
	if err != nil {
		t.Fatalf("rLockBucket: %s", err)
	}
	release()
	lockFile := bucketLockDir + sep + "bucket1"
	assertExists(t, jfs, lockFile, true)

	for _, name := range jfsObj.stagingBuckets() {
		if name == ".locks" {
			t.Error("the lock directory was mistaken for a bucket staging dir")
		}
	}
	buckets, err := jfsObj.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("list buckets: %s", err)
	}
	for _, b := range buckets {
		if b.Name == ".locks" || b.Name == metaBucket {
			t.Errorf("internal directory %q exposed as a bucket", b.Name)
		}
	}

	jfsObj.cleanupOnce()
	assertExists(t, jfs, lockFile, true)

	if err := jfsObj.DeleteBucket(ctx, "bucket1", false); err != nil {
		t.Fatalf("DeleteBucket: %s", err)
	}
	assertExists(t, jfs, lockFile, true)
}
