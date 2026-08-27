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
	"path/filepath"
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
	miniohash "github.com/minio/minio/pkg/hash"
)

type lockResult struct {
	lk  *jfsFLock
	err error
}

type renameGateMeta struct {
	meta.Meta
	dstName string
	renamed chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *renameGateMeta) Rename(ctx meta.Context, parentSrc meta.Ino, nameSrc string, parentDst meta.Ino, nameDst string, flags uint32, inode *meta.Ino, attr *meta.Attr) syscall.Errno {
	errno := m.Meta.Rename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, inode, attr)
	if errno == 0 && nameDst == m.dstName {
		m.once.Do(func() {
			close(m.renamed)
			<-m.release
		})
	}
	return errno
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

func TestBucketLifecycleLock(t *testing.T) {
	jfsObj, _, _ := newTestGateway(t, Config{MultiBucket: true})
	if err := jfsObj.MakeBucketWithLocation(context.Background(), minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}

	lk, err := jfsObj.lockBucket(context.Background(), "bucket-a")
	if err != nil {
		t.Fatalf("lock bucket-a: %s", err)
	}

	result := make(chan lockResult, 1)
	go func() {
		lk, err := jfsObj.lockBucket(context.Background(), "bucket-a")
		result <- lockResult{lk, err}
	}()
	select {
	case r := <-result:
		if r.err == nil {
			r.lk.Unlock()
		}
		lk.Unlock()
		t.Fatalf("second lock on the same bucket should block, got %v", r.err)
	case <-time.After(100 * time.Millisecond):
	}

	other, err := jfsObj.lockBucket(context.Background(), "bucket-b")
	if err != nil {
		lk.Unlock()
		t.Fatalf("lock on a different bucket should succeed: %s", err)
	}
	other.Unlock()
	lk.Unlock()

	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("second lock should succeed after unlock: %s", r.err)
		}
		r.lk.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("second lock did not succeed after unlock")
	}
}

func TestBucketLifecycleLockCancellation(t *testing.T) {
	jfsObj, _, _ := newTestGateway(t, Config{MultiBucket: true})
	if err := jfsObj.MakeBucketWithLocation(context.Background(), minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}

	lk, err := jfsObj.lockBucket(context.Background(), "canceled-bucket")
	if err != nil {
		t.Fatalf("lock bucket: %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan lockResult, 1)
	go func() {
		lk, err := jfsObj.lockBucket(ctx, "canceled-bucket")
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

	next, err := jfsObj.lockBucket(context.Background(), "canceled-bucket")
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

	lk, err := g1.lockBucket(context.Background(), "shared-bucket")
	if err != nil {
		t.Fatalf("lock bucket from first gateway: %s", err)
	}
	result := make(chan lockResult, 1)
	go func() {
		lk, err := g2.lockBucket(context.Background(), "shared-bucket")
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

func TestBucketLifecycleConsistency(t *testing.T) {
	jfsObj, jfs, _ := newTestGateway(t, Config{MultiBucket: true})
	ctx := context.Background()
	if err := jfsObj.MakeBucketWithLocation(ctx, minio.MinioMetaBucket, minio.BucketOptions{}); err != nil {
		t.Fatalf("create metadata bucket: %s", err)
	}

	t.Run("make and delete use lifecycle lock", func(t *testing.T) {
		lk, err := jfsObj.lockBucket(ctx, "locked-bucket")
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

		lk, err = jfsObj.lockBucket(ctx, "locked-bucket")
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
		if err == nil {
			t.Fatal("PutObject should fail after its bucket is deleted")
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

func TestMultipartUploadLock(t *testing.T) {
	jfsObj, _, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}

	readLock, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_RDLCK)
	if err != nil {
		t.Fatalf("get upload read lock: %s", err)
	}
	otherRead, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_RDLCK)
	if err != nil {
		readLock.RUnlock()
		t.Fatalf("second upload read lock should succeed: %s", err)
	}
	otherRead.RUnlock()

	result := make(chan lockResult, 1)
	go func() {
		lk, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_WRLCK)
		result <- lockResult{lk, err}
	}()
	select {
	case r := <-result:
		if r.err == nil {
			r.lk.Unlock()
		}
		readLock.RUnlock()
		t.Fatalf("upload write lock should wait for read lock, got %v", r.err)
	case <-time.After(100 * time.Millisecond):
	}
	readLock.RUnlock()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("upload write lock after read unlock: %s", r.err)
		}
		r.lk.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("upload write lock did not continue after read unlock")
	}
}

func TestMultipartUploadLockAcrossGateways(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "upload-lock.db")
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

	g1, _ := newTestGatewayWithMeta(t, m1, format, Config{})
	g2, _ := newTestGatewayWithMeta(t, m2, format, Config{})
	ctx := context.Background()
	uploadID, err := g1.NewMultipartUpload(ctx, format.Name, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	readLock, err := g1.lockUpload(ctx, format.Name, "object", uploadID, meta.F_RDLCK)
	if err != nil {
		t.Fatalf("get upload read lock from first gateway: %s", err)
	}
	result := make(chan lockResult, 1)
	go func() {
		lk, err := g2.lockUpload(ctx, format.Name, "object", uploadID, meta.F_WRLCK)
		result <- lockResult{lk, err}
	}()
	select {
	case r := <-result:
		if r.err == nil {
			r.lk.Unlock()
		}
		readLock.RUnlock()
		t.Fatalf("second gateway write lock should block, got %v", r.err)
	case <-time.After(100 * time.Millisecond):
	}
	readLock.RUnlock()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("second gateway write lock after read unlock: %s", r.err)
		}
		r.lk.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("second gateway write lock did not continue after read unlock")
	}
}

func TestMultipartUploadLockRejectsDeletedPath(t *testing.T) {
	jfsObj, _, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	writeLock, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_WRLCK)
	if err != nil {
		t.Fatalf("get upload write lock: %s", err)
	}
	result := make(chan lockResult, 1)
	go func() {
		lk, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_RDLCK)
		result <- lockResult{lk, err}
	}()
	select {
	case r := <-result:
		if r.err == nil {
			r.lk.RUnlock()
		}
		writeLock.Unlock()
		t.Fatalf("upload read lock should wait for write lock, got %v", r.err)
	case <-time.After(100 * time.Millisecond):
	}
	if eno := jfsObj.fs.Rmr(mctx, jfsObj.upath(bucket, uploadID), true, meta.RmrDefaultThreads); eno != 0 {
		writeLock.Unlock()
		t.Fatalf("remove locked upload path: %s", eno)
	}
	writeLock.Unlock()
	select {
	case r := <-result:
		if r.lk != nil {
			r.lk.RUnlock()
		}
		if !errors.As(r.err, &minio.InvalidUploadID{}) {
			t.Fatalf("waiter on a deleted upload should return InvalidUploadID, got %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upload lock waiter did not detect deleted path")
	}
}

func TestMultipartUploadLockRejectsDeletedPathAcrossFileSystems(t *testing.T) {
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	m := meta.NewClient("memkv://", nil)
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init metadata: %s", err)
	}
	g1, _ := newTestGatewayWithMeta(t, m, format, Config{})
	g2, _ := newTestGatewayWithMeta(t, m, format, Config{})
	ctx := context.Background()
	uploadID, err := g1.NewMultipartUpload(ctx, format.Name, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	uploadPath := g1.upath(format.Name, uploadID)
	if _, eno := g2.fs.Stat(mctx, uploadPath); eno != 0 {
		t.Fatalf("cache upload path in second filesystem: %s", eno)
	}
	writeLock, err := g1.lockUpload(ctx, format.Name, "object", uploadID, meta.F_WRLCK)
	if err != nil {
		t.Fatalf("get upload write lock: %s", err)
	}
	result := make(chan lockResult, 1)
	go func() {
		lk, err := g2.lockUpload(ctx, format.Name, "object", uploadID, meta.F_RDLCK)
		result <- lockResult{lk, err}
	}()
	select {
	case r := <-result:
		if r.err == nil {
			r.lk.RUnlock()
		}
		writeLock.Unlock()
		t.Fatalf("upload read lock should wait for write lock, got %v", r.err)
	case <-time.After(40 * time.Millisecond):
	}
	if eno := g1.fs.Rmr(mctx, uploadPath, true, meta.RmrDefaultThreads); eno != 0 {
		writeLock.Unlock()
		t.Fatalf("remove locked upload path: %s", eno)
	}
	writeLock.Unlock()
	select {
	case r := <-result:
		if r.lk != nil {
			r.lk.RUnlock()
		}
		if !errors.As(r.err, &minio.InvalidUploadID{}) {
			t.Fatalf("waiter with a positive dentry cache should return InvalidUploadID, got %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upload lock waiter did not detect deleted path")
	}
}

func TestMultipartCleanupUsesUploadLock(t *testing.T) {
	jfsObj, jfs, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	f, eno := jfs.Open(mctx, jfsObj.upath(bucket, uploadID), 0)
	if eno != 0 {
		t.Fatalf("open upload path: %s", eno)
	}
	if eno = f.Utime(mctx, -1, time.Now().Add(-8*24*time.Hour).UnixMilli()); eno != 0 {
		_ = f.Close(mctx)
		t.Fatalf("age upload path: %s", eno)
	}
	_ = f.Close(mctx)

	readLock, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_RDLCK)
	if err != nil {
		t.Fatalf("get upload read lock: %s", err)
	}
	done := make(chan struct{}, 1)
	go func() {
		jfsObj.cleanupDir(".sys/uploads/", true)
		done <- struct{}{}
	}()
	select {
	case <-done:
		readLock.RUnlock()
		t.Fatal("multipart cleanup should wait for upload read lock")
	case <-time.After(100 * time.Millisecond):
	}
	readLock.RUnlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("multipart cleanup did not continue after read unlock")
	}
	if _, eno = jfs.Stat(mctx, jfsObj.upath(bucket, uploadID)); !errors.Is(eno, os.ErrNotExist) {
		t.Fatalf("expired multipart upload should be deleted, got %s", eno)
	}
}

func TestMultipartCleanupKeepsRefreshedUpload(t *testing.T) {
	jfsObj, jfs, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, "object", minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	uploadPath := jfsObj.upath(bucket, uploadID)
	f, eno := jfs.Open(mctx, uploadPath, 0)
	if eno != 0 {
		t.Fatalf("open upload path: %s", eno)
	}
	if eno = f.Utime(mctx, -1, time.Now().Add(-8*24*time.Hour).UnixMilli()); eno != 0 {
		_ = f.Close(mctx)
		t.Fatalf("age upload path: %s", eno)
	}
	_ = f.Close(mctx)

	readLock, err := jfsObj.lockUpload(ctx, bucket, "object", uploadID, meta.F_RDLCK)
	if err != nil {
		t.Fatalf("get upload read lock: %s", err)
	}
	done := make(chan struct{}, 1)
	go func() {
		jfsObj.cleanupDir(".sys/uploads/", true)
		done <- struct{}{}
	}()
	select {
	case <-done:
		readLock.RUnlock()
		t.Fatal("multipart cleanup should wait for upload read lock")
	case <-time.After(40 * time.Millisecond):
	}
	createTestFile(t, jfs, jfsObj.ppath(bucket, uploadID, "1"))
	readLock.RUnlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("multipart cleanup did not continue after read unlock")
	}
	if _, eno = jfs.Stat(mctx, uploadPath); eno != 0 {
		t.Fatalf("refreshed multipart upload should be kept, got %s", eno)
	}
}

func TestPutObjectPartAfterAbort(t *testing.T) {
	jfsObj, jfs, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	const object = "object"
	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, object, minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}

	data := []byte("part written concurrently with abort")
	reader := &gatedReader{reader: bytes.NewReader(data), started: make(chan struct{}), release: make(chan struct{})}
	putReader := newTestPutObjReader(t, reader, data)
	putDone := make(chan error, 1)
	go func() {
		_, err := jfsObj.PutObjectPart(ctx, bucket, object, uploadID, 1, putReader, minio.ObjectOptions{})
		putDone <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(2 * time.Second):
		t.Fatal("PutObjectPart did not start reading")
	}

	abortDone := make(chan error, 1)
	go func() {
		abortDone <- jfsObj.AbortMultipartUpload(ctx, bucket, object, uploadID, minio.ObjectOptions{})
	}()
	select {
	case err := <-abortDone:
		if err != nil {
			close(reader.release)
			t.Fatalf("abort multipart upload: %s", err)
		}
	case <-time.After(2 * time.Second):
		close(reader.release)
		t.Fatal("AbortMultipartUpload waited for an uncommitted part body")
	}
	close(reader.release)
	select {
	case err := <-putDone:
		if !errors.As(err, &minio.InvalidUploadID{}) {
			t.Fatalf("part commit after abort should return InvalidUploadID, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PutObjectPart did not finish after abort")
	}
	if _, eno := jfs.Stat(mctx, jfsObj.upath(bucket, uploadID)); !errors.Is(eno, os.ErrNotExist) {
		t.Fatalf("aborted upload should not be recreated, got %s", eno)
	}
}

func TestCompleteMultipartUploadExcludesPartCommit(t *testing.T) {
	jfsObj, jfs, bucket := newTestGateway(t, Config{})
	ctx := context.Background()
	const object = "object"
	uploadID, err := jfsObj.NewMultipartUpload(ctx, bucket, object, minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	oldData := []byte("old part")
	oldPart, err := jfsObj.PutObjectPart(ctx, bucket, object, uploadID, 1, newTestPutObjReader(t, bytes.NewReader(oldData), oldData), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put initial part: %s", err)
	}

	newData := []byte("replacement part")
	reader := &gatedReader{reader: bytes.NewReader(newData), started: make(chan struct{}), release: make(chan struct{})}
	putReader := newTestPutObjReader(t, reader, newData)
	putDone := make(chan error, 1)
	go func() {
		_, err := jfsObj.PutObjectPart(ctx, bucket, object, uploadID, 1, putReader, minio.ObjectOptions{})
		putDone <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement PutObjectPart did not start reading")
	}

	_, err = jfsObj.CompleteMultipartUpload(ctx, bucket, object, uploadID, []minio.CompletePart{{PartNumber: 1, ETag: oldPart.ETag}}, minio.ObjectOptions{})
	if err != nil {
		close(reader.release)
		t.Fatalf("complete multipart upload: %s", err)
	}
	close(reader.release)
	select {
	case err := <-putDone:
		if !errors.As(err, &minio.InvalidUploadID{}) {
			t.Fatalf("part commit after complete should return InvalidUploadID, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement PutObjectPart did not finish after complete")
	}

	f, eno := jfs.Open(mctx, jfsObj.path(bucket, object), 0)
	if eno != 0 {
		t.Fatalf("open completed object: %s", eno)
	}
	defer f.Close(mctx)
	got := make([]byte, len(oldData))
	n, err := f.Read(mctx, got)
	if err != nil {
		t.Fatalf("read completed object: %s", err)
	}
	if n != len(oldData) || !bytes.Equal(got, oldData) {
		t.Fatalf("completed object = %q, want %q", got[:n], oldData)
	}
}

func TestCompleteMultipartUploadConcurrentOverwriteInfo(t *testing.T) {
	format := &meta.Format{Name: "test", BlockSize: 4096, Capacity: 1 << 30, DirStats: true}
	baseMeta := meta.NewClient("memkv://", nil)
	if err := baseMeta.Init(format, true); err != nil {
		t.Fatalf("init metadata: %s", err)
	}
	gate := &renameGateMeta{
		Meta:    baseMeta,
		dstName: "object",
		renamed: make(chan struct{}),
		release: make(chan struct{}),
	}
	g1, _ := newTestGatewayWithMeta(t, gate, format, Config{})
	g2, _ := newTestGatewayWithMeta(t, baseMeta, format, Config{})
	ctx := context.Background()
	const object = "object"

	uploadID, err := g1.NewMultipartUpload(ctx, format.Name, object, minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("create multipart upload: %s", err)
	}
	partData := []byte("multipart result")
	part, err := g1.PutObjectPart(ctx, format.Name, object, uploadID, 1, newTestPutObjReader(t, bytes.NewReader(partData), partData), minio.ObjectOptions{})
	if err != nil {
		t.Fatalf("put part: %s", err)
	}

	type completeResult struct {
		info minio.ObjectInfo
		err  error
	}
	completeDone := make(chan completeResult, 1)
	go func() {
		info, err := g1.CompleteMultipartUpload(ctx, format.Name, object, uploadID, []minio.CompletePart{{PartNumber: 1, ETag: part.ETag}}, minio.ObjectOptions{})
		completeDone <- completeResult{info: info, err: err}
	}()
	released := false
	release := func() {
		if !released {
			close(gate.release)
			released = true
		}
	}
	defer release()
	select {
	case <-gate.renamed:
	case <-time.After(2 * time.Second):
		t.Fatal("complete multipart upload did not rename the destination")
	}

	replacement := []byte("new")
	if _, err = g2.PutObject(ctx, format.Name, object, newTestPutObjReader(t, bytes.NewReader(replacement), replacement), minio.ObjectOptions{}); err != nil {
		t.Fatalf("overwrite object from second gateway: %s", err)
	}
	release()

	select {
	case result := <-completeDone:
		if result.err != nil {
			t.Fatalf("complete multipart upload: %s", result.err)
		}
		if result.info.Size != int64(len(partData)) {
			t.Fatalf("completed object info size = %d, want %d", result.info.Size, len(partData))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("complete multipart upload did not finish")
	}
	fi, eno := g2.fs.Stat(mctx, g2.path(format.Name, object))
	if eno != 0 {
		t.Fatalf("stat overwritten object: %s", eno)
	}
	if fi.Size() != int64(len(replacement)) {
		t.Fatalf("final object size = %d, want %d", fi.Size(), len(replacement))
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
	return jfsObj, jfs
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
