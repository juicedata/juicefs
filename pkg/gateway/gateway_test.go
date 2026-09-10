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
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	minio "github.com/minio/minio/cmd"
	miniohash "github.com/minio/minio/pkg/hash"
)

type lockResult struct {
	lk  *jfsFLock
	err error
}

type createErrMeta struct {
	meta.Meta
	onCreate func()
}

func (m *createErrMeta) Create(ctx meta.Context, parent meta.Ino, name string, mode, cumask uint16, flags uint32, inode *meta.Ino, attr *meta.Attr) syscall.Errno {
	if m.onCreate != nil {
		onCreate := m.onCreate
		m.onCreate = nil
		onCreate()
		return syscall.ENOENT
	}
	return m.Meta.Create(ctx, parent, name, mode, cumask, flags, inode, attr)
}

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

func TestBucketLifecycleLockCancellation(t *testing.T) {
	jfsObj, _, _ := newTestGateway(t, Config{MultiBucket: true})
	if err := jfsObj.MakeBucketWithLocation(context.Background(), minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}

	lk, err := jfsObj.lockBucket(context.Background())
	if err != nil {
		t.Fatalf("lock bucket: %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan lockResult, 1)
	go func() {
		lk, err := jfsObj.lockBucket(ctx)
		result <- lockResult{lk, err}
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case r := <-result:
		if r.lk != nil {
			r.lk.Unlock()
		}
		if !errors.Is(r.err, context.Canceled) {
			lk.Unlock()
			t.Fatalf("lock should stop after context cancellation, got %v", r.err)
		}
	case <-time.After(2 * time.Second):
		lk.Unlock()
		t.Fatal("lock did not stop after context cancellation")
	}
	lk.Unlock()

	next, err := jfsObj.lockBucket(context.Background())
	if err != nil {
		t.Fatalf("lock after canceled waiter: %s", err)
	}
	next.Unlock()
}

func TestBucketLifecycleLockAcrossGateways(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "gateway-lock.db")
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	m1 := meta.NewClient(metaURL, nil)
	if err := m1.Init(format, true); err != nil {
		t.Fatalf("init metadata: %s", err)
	}
	if err := m1.NewSession(true); err != nil {
		t.Fatalf("create first metadata session: %s", err)
	}
	defer m1.CloseSession()
	m2 := meta.NewClient(metaURL, nil)
	if _, err := m2.Load(true); err != nil {
		t.Fatalf("load metadata: %s", err)
	}
	if err := m2.NewSession(true); err != nil {
		t.Fatalf("create second metadata session: %s", err)
	}
	defer m2.CloseSession()

	g1, _ := newTestGatewayWithMeta(t, m1, format, Config{MultiBucket: true})
	g2, _ := newTestGatewayWithMeta(t, m2, format, Config{MultiBucket: true})
	if err := g1.MakeBucketWithLocation(context.Background(), minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}

	lk, err := g1.lockBucket(context.Background())
	if err != nil {
		t.Fatalf("lock bucket from first gateway: %s", err)
	}
	result := make(chan lockResult, 1)
	go func() {
		lk, err := g2.lockBucket(context.Background())
		result <- lockResult{lk, err}
	}()
	select {
	case r := <-result:
		if r.err == nil {
			r.lk.Unlock()
		}
		lk.Unlock()
		t.Fatalf("lock from second gateway should block, got %v", r.err)
	case <-time.After(100 * time.Millisecond):
	}
	lk.Unlock()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("second gateway should acquire lock after unlock: %s", r.err)
		}
		r.lk.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("second gateway did not acquire lock after unlock")
	}
}

func TestObjectCommitAfterBucketDeleted(t *testing.T) {
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	m := meta.NewClient("memkv://", nil)
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %s", err)
	}
	g1, _ := newTestGatewayWithMeta(t, m, format, Config{MultiBucket: true})
	g2, _ := newTestGatewayWithMeta(t, m, format, Config{MultiBucket: true})
	ctx := context.Background()
	const bucket = "gone-bucket"
	if err := g1.MakeBucketWithLocation(ctx, minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}
	if err := g1.MakeBucketWithLocation(ctx, "src-bucket", minio.BucketOptions{}); err != nil {
		t.Fatalf("create src bucket: %s", err)
	}
	if err := g1.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create bucket: %s", err)
	}
	srcData := []byte("src")
	if _, err := g1.PutObject(ctx, "src-bucket", "src-obj", newTestPutObjReader(t, bytes.NewReader(srcData), srcData), minio.ObjectOptions{}); err != nil {
		t.Fatalf("put src object: %s", err)
	}
	uploadID, err := g1.NewMultipartUpload(ctx, bucket, "obj", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	partData := []byte("part")
	part, err := g1.PutObjectPart(ctx, bucket, "obj", uploadID, 1, newTestPutObjReader(t, bytes.NewReader(partData), partData), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put part: %s", err)
	}
	// warm g1's dentry cache so checkBucket may still see the bucket
	if _, err := g1.GetBucketInfo(ctx, bucket); err != nil {
		t.Fatalf("get bucket info: %s", err)
	}
	// the second gateway deletes the bucket; g1's cache is not invalidated
	if err := g2.DeleteBucket(ctx, bucket, false); err != nil {
		t.Fatalf("delete bucket: %s", err)
	}

	for _, object := range []string{"obj", "dir/obj", "dir/"} {
		data := []byte("data")
		if _, err := g1.PutObject(ctx, bucket, object, newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}); !errors.As(err, &minio.BucketNotFound{}) {
			t.Fatalf("PutObject %s after bucket deleted should return BucketNotFound, got %v", object, err)
		}
	}
	if _, err := g1.CopyObject(ctx, "src-bucket", "src-obj", bucket, "copy-obj", minio.ObjectInfo{}, minio.ObjectOptions{}, minio.ObjectOptions{}); !errors.As(err, &minio.BucketNotFound{}) {
		t.Fatalf("CopyObject after bucket deleted should return BucketNotFound, got %v", err)
	}
	if _, err := g1.CompleteMultipartUpload(ctx, bucket, "obj", uploadID, []minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}, minio.ObjectOptions{}); !errors.As(err, &minio.BucketNotFound{}) {
		t.Fatalf("CompleteMultipartUpload after bucket deleted should return BucketNotFound, got %v", err)
	}
	// exercise the commit path deterministically, bypassing checkBucket
	data := []byte("data")
	if _, err := g1.putObject(ctx, bucket, g1.path(bucket, "obj"),
		newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}, func(string) {}, false); !errors.As(err, &minio.BucketNotFound{}) {
		t.Fatalf("putObject after bucket deleted should return BucketNotFound, got %v", err)
	}
	if _, errno := g2.fs.Stat(mctx, g2.path(bucket)); !fs.IsNotExist(errno) {
		t.Fatalf("deleted bucket was recreated: %s", errno)
	}
}

func TestCopyObjectTempCreateAfterBucketDeleted(t *testing.T) {
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	baseMeta := meta.NewClient("memkv://", nil)
	if err := baseMeta.Init(format, true); err != nil {
		t.Fatalf("init meta: %s", err)
	}
	createMeta := &createErrMeta{Meta: baseMeta}
	g1, _ := newTestGatewayWithMeta(t, createMeta, format, Config{MultiBucket: true})
	g2, _ := newTestGatewayWithMeta(t, baseMeta, format, Config{MultiBucket: true})
	ctx := context.Background()
	for _, bucket := range []string{minio.MinioMetaBucket, "src-bucket", "dst-bucket"} {
		if err := g1.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err != nil {
			t.Fatalf("create bucket %s: %s", bucket, err)
		}
	}
	data := []byte("data")
	if _, err := g1.PutObject(ctx, "src-bucket", "src-obj", newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}); err != nil {
		t.Fatalf("put source object: %s", err)
	}

	var deleteErr error
	createMeta.onCreate = func() {
		deleteErr = g2.DeleteBucket(ctx, "dst-bucket", false)
	}
	if _, err := g1.CopyObject(ctx, "src-bucket", "src-obj", "dst-bucket", "dst-obj", minio.ObjectInfo{}, minio.ObjectOptions{}, minio.ObjectOptions{}); !errors.As(err, &minio.BucketNotFound{}) {
		t.Fatalf("CopyObject after destination bucket deletion should return BucketNotFound, got %v", err)
	}
	if deleteErr != nil {
		t.Fatalf("delete destination bucket: %s", deleteErr)
	}
}

func TestObjectCommitErrBucketAlive(t *testing.T) {
	jfsObj, _, _ := newTestGateway(t, Config{MultiBucket: true})
	ctx := context.Background()
	if err := jfsObj.MakeBucketWithLocation(ctx, minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}
	if err := jfsObj.MakeBucketWithLocation(ctx, "alive-bucket", minio.BucketOptions{}); err != nil {
		t.Fatalf("create bucket: %s", err)
	}
	// a pruned parent or a removed temporary source surfaces as ENOENT while
	// the bucket still exists: the object/uploadID mapping must be kept
	if err := jfsObj.objectCommitErr(ctx, syscall.ENOENT, "alive-bucket", "obj"); !errors.As(err, &minio.ObjectNotFound{}) {
		t.Fatalf("commit ENOENT with alive bucket should return ObjectNotFound, got %v", err)
	}
	if err := jfsObj.objectCommitErr(ctx, syscall.ENOENT, "alive-bucket", "obj", "upload-id"); !errors.As(err, &minio.InvalidUploadID{}) {
		t.Fatalf("commit ENOENT with alive bucket and uploadID should return InvalidUploadID, got %v", err)
	}
	if err := jfsObj.DeleteBucket(ctx, "alive-bucket", false); err != nil {
		t.Fatalf("delete bucket: %s", err)
	}
	if err := jfsObj.objectCommitErr(ctx, syscall.ENOENT, "alive-bucket", "obj"); !errors.As(err, &minio.BucketNotFound{}) {
		t.Fatalf("commit ENOENT after bucket deleted should return BucketNotFound, got %v", err)
	}
}

type lookupErrMeta struct {
	meta.Meta
	name string
}

func (m *lookupErrMeta) Lookup(ctx meta.Context, parent meta.Ino, name string, inode *meta.Ino, attr *meta.Attr, checkPerm bool) syscall.Errno {
	if name == m.name {
		return syscall.EIO
	}
	return m.Meta.Lookup(ctx, parent, name, inode, attr, checkPerm)
}

func TestObjectCommitErrLookupFailure(t *testing.T) {
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	m := meta.NewClient("memkv://", nil)
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %s", err)
	}
	jfsObj, _ := newTestGatewayWithMeta(t, &lookupErrMeta{Meta: m, name: "err-bucket"}, format, Config{MultiBucket: true})
	// a metadata failure while verifying the bucket root must be surfaced,
	// not masked as ObjectNotFound
	if err := jfsObj.objectCommitErr(context.Background(), syscall.ENOENT, "err-bucket", "obj"); !errors.Is(err, syscall.EIO) {
		t.Fatalf("commit ENOENT with failing metadata lookup should return EIO, got %v", err)
	}
}

func TestBucketLifecycleConsistency(t *testing.T) {
	jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
	ctx := context.Background()
	if err := jfsObj.MakeBucketWithLocation(ctx, minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}

	t.Run("make and delete use lifecycle lock", func(t *testing.T) {
		lk, err := jfsObj.lockBucket(ctx)
		if err != nil {
			t.Fatalf("lock bucket: %s", err)
		}
		made := make(chan error, 1)
		go func() {
			made <- jfsObj.MakeBucketWithLocation(ctx, "locked-bucket", minio.BucketOptions{})
		}()
		select {
		case err := <-made:
			lk.Unlock()
			t.Fatalf("MakeBucket should wait for lifecycle lock, got %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		lk.Unlock()
		select {
		case err := <-made:
			if err != nil {
				t.Fatalf("MakeBucket after unlock: %s", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("MakeBucket did not continue after unlock")
		}

		lk, err = jfsObj.lockBucket(ctx)
		if err != nil {
			t.Fatalf("lock bucket before delete: %s", err)
		}
		deleted := make(chan error, 1)
		go func() {
			deleted <- jfsObj.DeleteBucket(ctx, "locked-bucket", false)
		}()
		select {
		case err := <-deleted:
			lk.Unlock()
			t.Fatalf("DeleteBucket should wait for lifecycle lock, got %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		lk.Unlock()
		select {
		case err := <-deleted:
			if err != nil {
				t.Fatalf("DeleteBucket after unlock: %s", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("DeleteBucket did not continue after unlock")
		}
	})

	t.Run("failed delete keeps metadata", func(t *testing.T) {
		const bucket = "nonempty-bucket"
		if err := jfsObj.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err != nil {
			t.Fatalf("create bucket: %s", err)
		}
		createTestFile(t, jfs, jfsObj.path(bucket, "object"))
		if err := jfsObj.DeleteBucket(ctx, bucket, false); !errors.As(err, &minio.BucketNotEmpty{}) {
			t.Fatalf("delete non-empty bucket should fail with BucketNotEmpty, got %v", err)
		}
		metadataPath := jfsObj.path(minio.MinioMetaBucket, minio.BucketMetaPrefix, bucket, minio.BucketMetadataFile)
		if _, errno := jfs.Stat(mctx, metadataPath); errno != 0 {
			t.Fatalf("bucket metadata should remain after failed delete: %s", errno)
		}
	})

	t.Run("delete moves temporary files to trash", func(t *testing.T) {
		const bucket = "temporary-files-bucket"
		if err := jfsObj.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err != nil {
			t.Fatalf("create bucket: %s", err)
		}
		uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, "multipart", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("create multipart upload: %s", err)
		}
		partData := []byte("part")
		if _, err := jfsObj.PutObjectPart(ctx, bucket, "multipart", uploadID, 1, newTestPutObjReader(t, bytes.NewReader(partData), partData), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put object part: %s", err)
		}
		if err := jfsObj.DeleteBucket(ctx, bucket, false); err != nil {
			t.Fatalf("delete bucket: %s", err)
		}
		if _, errno := jfs.Stat(mctx, jfsObj.tpath(bucket)); !fs.IsNotExist(errno) {
			t.Fatalf("bucket temporary files should be removed, got %s", errno)
		}
		trashDir := jfsObj.tpath(bucketTrashDir)
		f, errno := jfs.Open(mctx, trashDir, 0)
		if errno != 0 {
			t.Fatalf("open bucket trash directory: %s", errno)
		}
		entries, errno := f.Readdir(mctx, 0)
		_ = f.Close(mctx)
		if errno != 0 {
			t.Fatalf("read bucket trash directory: %s", errno)
		}
		found := false
		for _, entry := range entries {
			movedUpload := path.Join(trashDir, entry.Name(), "uploads", uploadID[:subDirPrefix], uploadID)
			if _, errno = jfs.Stat(mctx, movedUpload); errno == 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("bucket temporary files were not moved to trash")
		}
	})

	t.Run("missing bucket cleans stale metadata", func(t *testing.T) {
		const bucket = "stale-metadata-bucket"
		if err := jfsObj.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err != nil {
			t.Fatalf("create bucket: %s", err)
		}
		if errno := jfs.Delete(mctx, jfsObj.path(bucket)); errno != 0 {
			t.Fatalf("remove bucket directory: %s", errno)
		}
		if err := jfsObj.DeleteBucket(ctx, bucket, false); !errors.As(err, &minio.BucketNotFound{}) {
			t.Fatalf("delete missing bucket should return BucketNotFound, got %v", err)
		}
		metadataPath := jfsObj.path(minio.MinioMetaBucket, minio.BucketMetaPrefix, bucket, minio.BucketMetadataFile)
		if _, errno := jfs.Stat(mctx, metadataPath); !errors.Is(errno, os.ErrNotExist) {
			t.Fatalf("stale bucket metadata should be removed, got %s", errno)
		}
	})

	t.Run("metadata failure rolls back bucket", func(t *testing.T) {
		const bucket = "rollback-bucket"
		metadataDir := jfsObj.path(minio.MinioMetaBucket, minio.BucketMetaPrefix)
		if errno := jfs.MkdirAll(mctx, metadataDir, 0777, 022); errno != 0 {
			t.Fatalf("create metadata directory: %s", errno)
		}
		createTestFile(t, jfs, jfsObj.path(minio.MinioMetaBucket, minio.BucketMetaPrefix, bucket))
		if err := jfsObj.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err == nil {
			t.Fatal("MakeBucket should fail when metadata cannot be saved")
		}
		if _, errno := jfs.Stat(mctx, jfsObj.path(bucket)); !errors.Is(errno, os.ErrNotExist) {
			t.Fatalf("bucket should be removed after metadata failure, got %s", errno)
		}
	})
}

func TestPutObjectDoesNotRecreateDeletedBucket(t *testing.T) {
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	m := meta.NewClient("memkv://", nil)
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init metadata: %s", err)
	}
	g1, _ := newTestGatewayWithMeta(t, m, format, Config{MultiBucket: true})
	g2, _ := newTestGatewayWithMeta(t, m, format, Config{MultiBucket: true})
	ctx := context.Background()
	const bucket = "deleted-bucket"
	if err := g1.MakeBucketWithLocation(ctx, minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}
	if err := g1.MakeBucketWithLocation(ctx, bucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create bucket: %s", err)
	}

	data := []byte("object data")
	reader := &gatedReader{reader: bytes.NewReader(data), started: make(chan struct{}), release: make(chan struct{})}
	putReader := newTestPutObjReader(t, reader, data)
	putDone := make(chan error, 1)
	go func() {
		_, err := g1.PutObject(ctx, bucket, "dir/object", putReader, minio.ObjectOptions{})
		putDone <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(2 * time.Second):
		t.Fatal("PutObject did not start reading")
	}

	if err := g2.DeleteBucket(ctx, bucket, false); err != nil {
		close(reader.release)
		t.Fatalf("delete bucket: %s", err)
	}
	// Simulate the first gateway observing the deletion after its dentry cache
	// expires. The object commit must still not create the bucket root.
	g1.fs.InvalidateEntry(meta.RootInode, bucket)
	close(reader.release)
	select {
	case err := <-putDone:
		if !errors.As(err, &minio.BucketNotFound{}) {
			t.Fatalf("PutObject after bucket deleted should return BucketNotFound, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PutObject did not finish")
	}

	if _, errno := g2.fs.Stat(mctx, g2.path(bucket)); !fs.IsNotExist(errno) {
		t.Fatalf("deleted bucket was recreated: %s", errno)
	}
	metadataPath := g2.path(minio.MinioMetaBucket, minio.BucketMetaPrefix, bucket, minio.BucketMetadataFile)
	if _, errno := g2.fs.Stat(mctx, metadataPath); !fs.IsNotExist(errno) {
		t.Fatalf("deleted bucket metadata was recreated: %s", errno)
	}
}

type gatedReader struct {
	reader  io.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *gatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.reader.Read(p)
}

func newTestPutObjReader(t *testing.T, r io.Reader, data []byte) *minio.PutObjReader {
	t.Helper()
	sum := md5.Sum(data)
	hashReader, err := miniohash.NewReader(r, int64(len(data)), hex.EncodeToString(sum[:]), "", int64(len(data)))
	if err != nil {
		t.Fatalf("create put object reader: %s", err)
	}
	return minio.NewPutObjReader(hashReader)
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
	jfsObj, jfs := newTestGatewayWithMeta(t, m, format, conf)
	return jfsObj, jfs, format.Name
}

func newTestGatewayWithMeta(t *testing.T, m meta.Meta, format *meta.Format, conf Config) (*jfsObjects, *fs.FileSystem) {
	t.Helper()
	vfsConf := &vfs.Config{
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
	jfs, err := fs.NewFileSystem(vfsConf, m, store, nil)
	if err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	conf.Bucket = format.Name
	if conf.Umask == 0 {
		conf.Umask = 022
	}
	jfsObj := &jfsObjects{
		fs:       jfs,
		conf:     vfsConf,
		listPool: minio.NewTreeWalkPool(time.Minute * 30),
		gConf:    &conf,
		nsMutex:  minio.NewNSLock(false),
	}
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	if conf.MultiBucket && !conf.ReadOnly {
		if errno := jfs.MkdirAll(mctx, jfsObj.tpath(bucketTrashDir), 0777, conf.Umask); errno != 0 {
			t.Fatalf("create bucket trash directory: %s", errno)
		}
	}
	return jfsObj, jfs
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

func setupMetadataInheritanceTarget(t *testing.T, jfs *fs.FileSystem, defaultRule *acl.Rule) (string, *acl.Rule) {
	t.Helper()
	format := jfs.Meta().GetFormat()
	format.EnableACL = true
	if err := jfs.Meta().Init(&format, false); err != nil {
		t.Fatalf("enable ACL support: %s", err)
	}

	const target = "/acl-target"
	if eno := jfs.Mkdir(mctx, target, 0777, 0); eno != 0 {
		t.Fatalf("mkdir target: %s", eno)
	}

	// Configure the target through the metadata API with an explicit root
	// context.  The test must also run as an unprivileged local macOS process;
	// going through vfs.File.Chown would depend on the host's OS permissions.
	rootCtx := meta.Background()
	var targetIno meta.Ino
	eno := jfs.Meta().Lookup(rootCtx, meta.RootInode, "acl-target", &targetIno, new(meta.Attr), false)
	if eno != 0 {
		t.Fatalf("lookup target: %s", eno)
	}
	if eno = jfs.Meta().SetAttr(rootCtx, targetIno, meta.SetAttrGID, 0, &meta.Attr{Gid: 2468}); eno != 0 {
		t.Fatalf("set target gid: %s", eno)
	}
	// Chown may clear setgid, so set it afterwards.
	if eno = jfs.Meta().SetAttr(rootCtx, targetIno, meta.SetAttrMode, 0, &meta.Attr{Mode: 02770}); eno != 0 {
		t.Fatalf("set target mode: %s", eno)
	}

	if defaultRule == nil {
		// Keep the resulting mode at 0600 while making the ACL extended, so the
		// test checks both the mode derived from the default ACL and ACL storage.
		defaultRule = &acl.Rule{
			Owner: 6,
			Group: 0,
			Mask:  0,
			Other: 0,
			NamedUsers: []acl.Entry{{
				Id:   1001,
				Perm: 0,
			}},
		}
	}
	if eno = jfs.Meta().SetFacl(rootCtx, targetIno, acl.TypeDefault, defaultRule); eno != 0 {
		t.Fatalf("set default ACL: %s", eno)
	}

	return target, defaultRule
}

func setupNestedMetadataInheritanceTarget(t *testing.T, jfs *fs.FileSystem) (string, *acl.Rule) {
	// Nested directory creation needs execute permission.  Keep this setup
	// separate from the regular file case, whose ACL intentionally produces 0600.
	nestedRule := &acl.Rule{
		Owner: 7,
		Group: 0,
		Mask:  7,
		Other: 0,
		NamedUsers: []acl.Entry{{
			Id:   1001,
			Perm: 0,
		}},
	}
	target, _ := setupMetadataInheritanceTarget(t, jfs, nestedRule)
	return target, nestedRule
}

func assertInheritedMetadata(t *testing.T, jfs *fs.FileSystem, name string, wantACL *acl.Rule) {
	t.Helper()
	fi, eno := jfs.Stat(mctx, name)
	if eno != 0 {
		t.Fatalf("stat %s: %s", name, eno)
	}
	gotACL := &acl.Rule{}
	aclErr := jfs.GetFacl(mctx, name, acl.TypeAccess, gotACL)
	t.Logf("%s: mode=%#o gid=%d access_acl_err=%v access_acl=%s", name, uint32(fi.Mode().Perm()), fi.Gid(), aclErr, gotACL)

	if fi.Gid() != 2468 || uint32(fi.Mode().Perm()) != uint32(wantACL.GetMode()) || aclErr != 0 || !gotACL.IsEqual(wantACL) {
		t.Errorf("metadata mismatch for %s: got mode=%#o gid=%d acl_err=%v acl=%s, want mode=%#o gid=%d acl=%s",
			name, uint32(fi.Mode().Perm()), fi.Gid(), aclErr, gotACL, uint32(wantACL.GetMode()), 2468, wantACL)
	}
}

// TestGatewayObjectOperationsInheritDestinationMetadata verifies that every
// Gateway upload completion path applies the destination directory's POSIX
// GID and default ACL to the final object inode.
func TestGatewayObjectOperationsInheritDestinationMetadata(t *testing.T) {
	// A regular PUT creates a staged inode first and commits it with rename.
	t.Run("PUT", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		target, defaultRule := setupMetadataInheritanceTarget(t, jfs, nil)
		wantACL := defaultRule.ChildAccessACL(0666)
		data := []byte("metadata-inheritance")

		if _, err := jfsObj.PutObject(context.Background(), bucket, "acl-target/put",
			newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put object: %s", err)
		}
		assertInheritedMetadata(t, jfs, target+"/put", wantACL)
	})

	// A nested object path exercises the ENOENT retry: the first rename sees
	// missing parent directories, mkdirAllInBucket creates them, and the second
	// rename commits the staged file with inherited metadata.
	t.Run("PUT with missing parent directories", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		target, nestedRule := setupNestedMetadataInheritanceTarget(t, jfs)
		wantACL := nestedRule.ChildAccessACL(0666)
		data := []byte("metadata-inheritance")
		object := "acl-target/nested/deep/put"

		if _, err := jfsObj.PutObject(context.Background(), bucket, object,
			newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put nested object: %s", err)
		}
		assertInheritedMetadata(t, jfs, target+"/nested/deep/put", wantACL)
	})

	// COPY follows the same staged-file commit path as PUT and must preserve the
	// destination directory's inherited metadata.
	t.Run("COPY", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		target, defaultRule := setupMetadataInheritanceTarget(t, jfs, nil)
		wantACL := defaultRule.ChildAccessACL(0666)
		data := []byte("metadata-inheritance")
		if _, err := jfsObj.PutObject(context.Background(), bucket, "source",
			newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put source object: %s", err)
		}
		srcInfo, err := jfsObj.GetObjectInfo(context.Background(), bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source object info: %s", err)
		}
		if _, err = jfsObj.CopyObject(context.Background(), bucket, "source", bucket,
			"acl-target/copy", srcInfo, minio.ObjectOptions{}, minio.ObjectOptions{}); err != nil {
			t.Fatalf("copy object: %s", err)
		}
		assertInheritedMetadata(t, jfs, target+"/copy", wantACL)
	})

	// COPY must also retry after the destination's missing parent directories
	// are created, then apply the destination directory's inherited metadata.
	t.Run("COPY with missing parent directories", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		target, nestedRule := setupNestedMetadataInheritanceTarget(t, jfs)
		wantACL := nestedRule.ChildAccessACL(0666)
		data := []byte("metadata-inheritance")
		if _, err := jfsObj.PutObject(context.Background(), bucket, "source",
			newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{}); err != nil {
			t.Fatalf("put source object: %s", err)
		}
		srcInfo, err := jfsObj.GetObjectInfo(context.Background(), bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source object info: %s", err)
		}
		object := "acl-target/nested/deep/copy"
		if _, err = jfsObj.CopyObject(context.Background(), bucket, "source", bucket,
			object, srcInfo, minio.ObjectOptions{}, minio.ObjectOptions{}); err != nil {
			t.Fatalf("copy nested object: %s", err)
		}
		assertInheritedMetadata(t, jfs, target+"/nested/deep/copy", wantACL)
	})

	// Completing a multipart upload commits a previously staged inode and must
	// apply inheritance at completion time, not only when the upload starts.
	t.Run("multipart completion", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		target, defaultRule := setupMetadataInheritanceTarget(t, jfs, nil)
		wantACL := defaultRule.ChildAccessACL(0666)
		data := []byte("metadata-inheritance")
		object := "acl-target/multipart"
		uploadID, err := jfsObj.NewMultipartUpload(context.Background(), bucket, object, minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("new multipart upload: %s", err)
		}
		part, err := jfsObj.PutObjectPart(context.Background(), bucket, object, uploadID, 1,
			newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("put multipart part: %s", err)
		}
		if _, err = jfsObj.CompleteMultipartUpload(context.Background(), bucket, object, uploadID,
			[]minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}, minio.ObjectOptions{}); err != nil {
			t.Fatalf("complete multipart upload: %s", err)
		}
		assertInheritedMetadata(t, jfs, target+"/multipart", wantACL)
	})

	// Multipart completion must create missing destination parents before the
	// retrying rename and preserve the inherited metadata on the final object.
	t.Run("multipart completion with missing parent directories", func(t *testing.T) {
		jfsObj, jfs, bucket := newTestGateway(t, Config{})
		target, nestedRule := setupNestedMetadataInheritanceTarget(t, jfs)
		wantACL := nestedRule.ChildAccessACL(0666)
		data := []byte("metadata-inheritance")
		object := "acl-target/nested/deep/multipart"
		uploadID, err := jfsObj.NewMultipartUpload(context.Background(), bucket, object, minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("new nested multipart upload: %s", err)
		}
		part, err := jfsObj.PutObjectPart(context.Background(), bucket, object, uploadID, 1,
			newTestPutObjReader(t, bytes.NewReader(data), data), minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("put nested multipart part: %s", err)
		}
		if _, err = jfsObj.CompleteMultipartUpload(context.Background(), bucket, object, uploadID,
			[]minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}, minio.ObjectOptions{}); err != nil {
			t.Fatalf("complete nested multipart upload: %s", err)
		}
		assertInheritedMetadata(t, jfs, target+"/nested/deep/multipart", wantACL)
	})
}
