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
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	minio "github.com/minio/minio/cmd"
	xhttp "github.com/minio/minio/cmd/http"
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
		type result struct {
			payload []byte
			err     error
		}
		results := make(chan result, writers)
		readers := make([]*minio.PutObjReader, writers)
		var waitGroup sync.WaitGroup
		for i := 0; i < writers; i++ {
			payload := []byte(fmt.Sprintf("writer-%02d", i))
			readers[i] = newPutObjectReader(t, payload)
			waitGroup.Add(1)
			go func(payload []byte, reader *minio.PutObjReader) {
				defer waitGroup.Done()
				<-start
				_, err := gateway.PutObject(ctx, bucket, "race", reader, minio.ObjectOptions{IfNoneMatch: true})
				results <- result{payload: payload, err: err}
			}(payload, readers[i])
		}
		close(start)
		waitGroup.Wait()
		close(results)

		succeeded := 0
		failed := 0
		var successfulPayload []byte
		for result := range results {
			if result.err == nil {
				succeeded++
				successfulPayload = result.payload
				continue
			}
			var preconditionFailed minio.PreConditionFailed
			if errors.As(result.err, &preconditionFailed) {
				failed++
				continue
			}
			t.Fatalf("unexpected concurrent PUT error: %T: %v", result.err, result.err)
		}
		if succeeded != 1 || failed != writers-1 {
			t.Fatalf("conditional create results: succeeded=%d failed=%d", succeeded, failed)
		}
		if stored := readGatewayObject(t, gateway, bucket, "race"); !bytes.Equal(stored, successfulPayload) {
			t.Fatalf("stored payload %q does not match successful writer %q", stored, successfulPayload)
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

	t.Run("directory marker writes can proceed while the parent is locked", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		parent, eno := jfs.Stat(mctx, gateway.path(bucket, ""))
		if eno != 0 {
			t.Fatalf("stat parent directory: %s", eno)
		}
		const owner = ^uint64(0)
		if eno = jfs.Meta().Flock(mctx, parent.Inode(), owner, meta.F_WRLCK, false); eno != 0 {
			t.Fatalf("lock parent directory: %s", eno)
		}
		defer func() {
			if eno := jfs.Meta().Flock(mctx, parent.Inode(), owner, meta.F_UNLCK, false); eno != 0 {
				t.Errorf("unlock parent directory: %s", eno)
			}
		}()

		for _, conditional := range []bool{true, false} {
			if _, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), minio.ObjectOptions{IfNoneMatch: conditional}); err != nil {
				t.Fatalf("write marker with IfNoneMatch=%t while parent is locked: %s", conditional, err)
			}
		}
		assertHeadObject(t, gateway, bucket, "prefix/", true)
	})

	t.Run("head dir concurrent marker creates have one winner", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{HeadDir: true})
		ctx := context.Background()
		const writers = 8
		start := make(chan struct{})
		results := make(chan error, writers)
		var waitGroup sync.WaitGroup
		for i := 0; i < writers; i++ {
			reader := newPutObjectReader(t, nil)
			waitGroup.Add(1)
			go func(reader *minio.PutObjReader) {
				defer waitGroup.Done()
				<-start
				_, err := gateway.PutObject(ctx, bucket, "prefix/", reader, minio.ObjectOptions{IfNoneMatch: true})
				results <- err
			}(reader)
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
			t.Fatalf("unexpected concurrent directory PUT error: %T: %v", err, err)
		}
		if succeeded != 1 || failed != writers-1 {
			t.Fatalf("conditional directory results: succeeded=%d failed=%d", succeeded, failed)
		}
		assertHeadObject(t, gateway, bucket, "prefix/", true)
	})

	t.Run("implicit directory is promoted without replacing children", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{KeepEtag: true, ObjTag: true, ObjMeta: true})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		directoryPath := gateway.path(bucket, "prefix")
		fi, eno := jfs.Stat(mctx, directoryPath)
		if eno != 0 {
			t.Fatalf("stat implicit directory: %s", eno)
		}
		attr := meta.Attr{Atime: 123, Atimensec: 456000000}
		if eno = jfs.Meta().SetAttr(mctx, fi.Inode(), meta.SetAttrAtime, 0, &attr); eno != 0 {
			t.Fatalf("set implicit directory atime: %s", eno)
		}
		jfs.InvalidateAttr(fi.Inode())
		originalXattrs := map[string][]byte{
			s3Etag: []byte("original-etag"),
			s3Tags: []byte("owner=posix"),
			s3Meta: []byte(`{"x-amz-meta-owner":"posix"}`),
		}
		for name, value := range originalXattrs {
			if eno = jfs.SetXattr(mctx, directoryPath, name, value, 0); eno != 0 {
				t.Fatalf("set original xattr %s: %s", name, eno)
			}
		}
		assertHeadObject(t, gateway, bucket, "prefix/", false)

		_, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), minio.ObjectOptions{IfNoneMatch: true})
		if err != nil {
			t.Fatalf("promote implicit directory: %v", err)
		}
		after, eno := jfs.Stat(mctx, directoryPath)
		if eno != 0 {
			t.Fatalf("stat rejected implicit directory: %s", eno)
		}
		if after.Inode() != fi.Inode() || !isExplicitDirectoryMarker(after.Attr()) {
			t.Fatalf("promotion did not preserve inode and publish marker: %+v", after)
		}
		for name := range originalXattrs {
			if _, eno := jfs.GetXattr(mctx, directoryPath, name); eno != meta.ENOATTR {
				t.Fatalf("old xattr %s remains: %s", name, eno)
			}
		}
		assertHeadObject(t, gateway, bucket, "prefix/", true)
		_, err = gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
		if got := readGatewayObject(t, gateway, bucket, "prefix/child"); !bytes.Equal(got, []byte("child")) {
			t.Fatalf("child object changed: got %q, want %q", got, "child")
		}
	})

	t.Run("non-empty directory PUT does not create a marker", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		assertHeadObject(t, gateway, bucket, "prefix/", false)

		_, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, []byte("not-empty")), minio.ObjectOptions{IfNoneMatch: true})
		var existsAsDirectory minio.ObjectExistsAsDirectory
		if !errors.As(err, &existsAsDirectory) {
			t.Fatalf("expected ObjectExistsAsDirectory, got %T: %v", err, err)
		}
		assertHeadObject(t, gateway, bucket, "prefix/", false)
	})

	t.Run("sub-millisecond atime uses the same marker semantics", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if eno := jfs.MkdirAll(mctx, "/prefix", 0777, 022); eno != 0 {
			t.Fatalf("create directory: %s", eno)
		}
		fi, eno := jfs.Stat(mctx, "/prefix")
		if eno != 0 {
			t.Fatalf("stat directory: %s", eno)
		}
		attr := meta.Attr{Atime: 0, Atimensec: 1}
		if eno = jfs.Meta().SetAttr(mctx, fi.Inode(), meta.SetAttrAtime, 0, &attr); eno != 0 {
			t.Fatalf("set directory atime: %s", eno)
		}
		jfs.InvalidateAttr(fi.Inode())

		assertHeadObject(t, gateway, bucket, "prefix/", true)
		_, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
	})

	t.Run("unconditional PUT still promotes an implicit directory", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		if _, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), minio.ObjectOptions{}); err != nil {
			t.Fatalf("unconditionally create marker for implicit directory: %s", err)
		}
		assertHeadObject(t, gateway, bucket, "prefix/", true)
		if got := readGatewayObject(t, gateway, bucket, "prefix/child"); !bytes.Equal(got, []byte("child")) {
			t.Fatalf("child object changed: got %q, want %q", got, "child")
		}
	})

	t.Run("concurrent POSIX directory creation has a safe outcome", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		const iterations = 128
		for i := 0; i < iterations; i++ {
			object := fmt.Sprintf("prefix-%03d/", i)
			directoryPath := gateway.path(bucket, object)
			reader := newPutObjectReader(t, nil)
			start := make(chan struct{})
			markerResult := make(chan error, 1)
			directoryResult := make(chan error, 1)

			go func() {
				<-start
				_, err := gateway.PutObject(ctx, bucket, object, reader, minio.ObjectOptions{IfNoneMatch: true})
				markerResult <- err
			}()
			go func() {
				<-start
				directoryResult <- gateway.mkdirAll(ctx, directoryPath)
			}()
			close(start)

			if err := <-directoryResult; err != nil {
				t.Fatalf("create POSIX directory %s: %s", directoryPath, err)
			}
			markerErr := <-markerResult
			if markerErr == nil {
				assertHeadObject(t, gateway, bucket, object, true)
				continue
			}
			t.Fatalf("conditional marker creation: %v", markerErr)
		}
	})

	t.Run("head dir treats an implicit directory as existing", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{HeadDir: true})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		_, err := gateway.PutObject(ctx, bucket, "prefix/", newPutObjectReader(t, nil), minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
	})
}

func TestCopyObjectIfNoneMatch(t *testing.T) {
	t.Run("existing destination remains unchanged", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, []byte("source")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create source: %s", err)
		}
		if _, err := gateway.PutObject(ctx, bucket, "destination", newPutObjectReader(t, []byte("original")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create destination: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		_, err = gateway.CopyObject(ctx, bucket, "source", bucket, "destination", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
		if got := readGatewayObject(t, gateway, bucket, "destination"); !bytes.Equal(got, []byte("original")) {
			t.Fatalf("destination changed: got %q, want %q", got, "original")
		}
	})

	t.Run("new destination is created", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, []byte("source")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create source: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		if _, err = gateway.CopyObject(ctx, bucket, "source", bucket, "destination", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true}); err != nil {
			t.Fatalf("conditionally copy to new destination: %s", err)
		}
		if got := readGatewayObject(t, gateway, bucket, "destination"); !bytes.Equal(got, []byte("source")) {
			t.Fatalf("copied data: got %q, want %q", got, "source")
		}
	})

	t.Run("copying an object onto itself fails", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, []byte("source")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create source: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		_, err = gateway.CopyObject(ctx, bucket, "source", bucket, "source", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
	})

	t.Run("zero-byte source creates a new marker with complete attributes", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{KeepEtag: true, ObjTag: true, ObjMeta: true})
		ctx := context.Background()
		sourceOpts := minio.ObjectOptions{UserDefined: map[string]string{"x-amz-meta-owner": "source"}}
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, nil), sourceOpts); err != nil {
			t.Fatalf("create source: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}
		srcInfo.UserDefined["x-amz-meta-owner"] = "final"
		srcInfo.UserDefined[xhttp.AmzObjectTagging] = "project=juicefs"

		copyInfo, err := gateway.CopyObject(ctx, bucket, "source", bucket, "prefix/", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		if err != nil {
			t.Fatalf("conditionally copy to new marker: %s", err)
		}
		assertHeadObject(t, gateway, bucket, "prefix/", true)
		if copyInfo.ETag != srcInfo.ETag {
			t.Fatalf("copy ETag: got %q, want %q", copyInfo.ETag, srcInfo.ETag)
		}
		dstInfo, err := gateway.GetObjectInfo(ctx, bucket, "prefix/", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get destination info: %s", err)
		}
		if dstInfo.ETag != copyInfo.ETag {
			t.Fatalf("destination HEAD ETag: got %q, want %q", dstInfo.ETag, copyInfo.ETag)
		}
		listInfo, err := gateway.ListObjects(ctx, bucket, "prefix/", "", "", 100)
		if err != nil {
			t.Fatalf("list destination marker: %s", err)
		}
		var listedMarker *minio.ObjectInfo
		for i := range listInfo.Objects {
			if listInfo.Objects[i].Name == "prefix/" {
				listedMarker = &listInfo.Objects[i]
				break
			}
		}
		if listedMarker == nil {
			t.Fatalf("destination marker missing from LIST: %#v", listInfo.Objects)
		}
		if listedMarker.ETag != copyInfo.ETag {
			t.Fatalf("destination LIST ETag: got %q, want %q", listedMarker.ETag, copyInfo.ETag)
		}
		if got := dstInfo.UserDefined["x-amz-meta-owner"]; got != "final" {
			t.Fatalf("destination metadata: got %q, want %q", got, "final")
		}
		if dstInfo.UserTags != "project=juicefs" {
			t.Fatalf("destination tags: got %q, want %q", dstInfo.UserTags, "project=juicefs")
		}
		storedEtag, eno := jfs.GetXattr(mctx, "/prefix", s3Etag)
		if eno != 0 {
			t.Fatalf("get destination ETag: %s", eno)
		}
		if string(storedEtag) != srcInfo.ETag {
			t.Fatalf("stored ETag: got %q, want %q", storedEtag, srcInfo.ETag)
		}
		fi, eno := jfs.Stat(mctx, "/prefix")
		if eno != 0 {
			t.Fatalf("stat destination marker: %s", eno)
		}
		if !isExplicitDirectoryMarker(fi.Attr()) {
			t.Fatalf("destination was not published as an explicit marker: atime=%d.%09d", fi.Attr().Atime, fi.Attr().Atimensec)
		}
	})

	t.Run("zero-byte source promotes an implicit directory atomically", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{KeepEtag: true, ObjTag: true, ObjMeta: true})
		ctx := context.Background()
		sourceOpts := minio.ObjectOptions{UserDefined: map[string]string{
			"x-amz-meta-owner":     "source",
			xhttp.AmzObjectTagging: "owner=source",
		}}
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, nil), sourceOpts); err != nil {
			t.Fatalf("create source: %s", err)
		}
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		directoryPath := gateway.path(bucket, "prefix")
		fi, eno := jfs.Stat(mctx, directoryPath)
		if eno != 0 {
			t.Fatalf("stat implicit directory: %s", eno)
		}
		attr := meta.Attr{Atime: 321, Atimensec: 654000000}
		if eno = jfs.Meta().SetAttr(mctx, fi.Inode(), meta.SetAttrAtime, 0, &attr); eno != 0 {
			t.Fatalf("set implicit directory atime: %s", eno)
		}
		jfs.InvalidateAttr(fi.Inode())
		originalXattrs := map[string][]byte{
			s3Etag: []byte("posix-etag"),
			s3Tags: []byte("owner=posix"),
			s3Meta: []byte(`{"x-amz-meta-owner":"posix"}`),
		}
		for name, value := range originalXattrs {
			if eno = jfs.SetXattr(mctx, directoryPath, name, value, 0); eno != 0 {
				t.Fatalf("set original xattr %s: %s", name, eno)
			}
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		srcInfo.UserDefined[xhttp.AmzObjectTagging] = srcInfo.UserTags

		_, err = gateway.CopyObject(ctx, bucket, "source", bucket, "prefix/", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		if err != nil {
			t.Fatalf("promote directory by copy: %v", err)
		}
		after, eno := jfs.Stat(mctx, directoryPath)
		if eno != 0 {
			t.Fatalf("stat rejected implicit destination: %s", eno)
		}
		if after.Inode() != fi.Inode() || !isExplicitDirectoryMarker(after.Attr()) {
			t.Fatalf("copy did not preserve inode and publish marker: %+v", after)
		}
		info, err := gateway.GetObjectInfo(ctx, bucket, "prefix/", minio.ObjectOptions{})
		if err != nil || info.ETag != srcInfo.ETag || info.UserTags != "owner=source" || info.UserDefined["x-amz-meta-owner"] != "source" {
			t.Fatalf("copied marker attributes: %+v, %v", info, err)
		}
		_, err = gateway.CopyObject(ctx, bucket, "source", bucket, "prefix/", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
		assertHeadObject(t, gateway, bucket, "prefix/", true)
		if got := readGatewayObject(t, gateway, bucket, "prefix/child"); !bytes.Equal(got, []byte("child")) {
			t.Fatalf("child object changed: got %q, want %q", got, "child")
		}
	})

	t.Run("non-empty source does not create a directory marker", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, []byte("source")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create source: %s", err)
		}
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		_, err = gateway.CopyObject(ctx, bucket, "source", bucket, "prefix/", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		var existsAsDirectory minio.ObjectExistsAsDirectory
		if !errors.As(err, &existsAsDirectory) {
			t.Fatalf("expected ObjectExistsAsDirectory, got %T: %v", err, err)
		}
		assertHeadObject(t, gateway, bucket, "prefix/", false)
	})

	t.Run("head dir treats an implicit destination as existing", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{HeadDir: true})
		ctx := context.Background()
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, nil), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create source: %s", err)
		}
		if _, err := gateway.PutObject(ctx, bucket, "prefix/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create child object: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		_, err = gateway.CopyObject(ctx, bucket, "source", bucket, "prefix/", srcInfo,
			minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
		assertPreconditionFailed(t, err)
		fi, eno := jfs.Stat(mctx, "/prefix")
		if eno != 0 {
			t.Fatalf("stat implicit directory: %s", eno)
		}
		if isExplicitDirectoryMarker(fi.Attr()) {
			t.Fatal("failed conditional copy changed the implicit directory into a marker")
		}
	})

	t.Run("unconditional PUT and conditional directory copy preserve attributes", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{KeepEtag: true, ObjTag: true, ObjMeta: true})
		ctx := context.Background()
		sourceOpts := minio.ObjectOptions{UserDefined: map[string]string{
			"x-amz-meta-owner":     "copy",
			xhttp.AmzObjectTagging: "winner=copy",
		}}
		if _, err := gateway.PutObject(ctx, bucket, "source", newPutObjectReader(t, nil), sourceOpts); err != nil {
			t.Fatalf("create source: %s", err)
		}
		srcInfo, err := gateway.GetObjectInfo(ctx, bucket, "source", minio.ObjectOptions{})
		if err != nil {
			t.Fatalf("get source info: %s", err)
		}

		const iterations = 128
		for i := 0; i < iterations; i++ {
			object := fmt.Sprintf("concurrent-prefix-%03d", i)
			implicitDirectory := i%2 == 0
			if implicitDirectory {
				if _, err = gateway.PutObject(ctx, bucket, object+"/child", newPutObjectReader(t, []byte("child")), minio.ObjectOptions{}); err != nil {
					t.Fatalf("create child for %s: %s", object, err)
				}
			}

			start := make(chan struct{})
			putResult := make(chan error, 1)
			copyResult := make(chan error, 1)
			putReader := newPutObjectReader(t, nil)
			go func() {
				<-start
				_, putErr := gateway.PutObject(ctx, bucket, object+"/", putReader, minio.ObjectOptions{})
				putResult <- putErr
			}()
			go func() {
				<-start
				_, copyErr := gateway.CopyObject(ctx, bucket, "source", bucket, object+"/", srcInfo,
					minio.ObjectOptions{}, minio.ObjectOptions{IfNoneMatch: true})
				copyResult <- copyErr
			}()
			close(start)

			if putErr := <-putResult; putErr != nil {
				t.Fatalf("unconditional marker PUT for %s: %s", object, putErr)
			}
			copyErr := <-copyResult
			var preconditionFailed minio.PreConditionFailed
			if copyErr != nil && !errors.As(copyErr, &preconditionFailed) {
				t.Fatalf("conditional marker Copy for %s: %T: %v", object, copyErr, copyErr)
			}

			assertHeadObject(t, gateway, bucket, object+"/", true)
			info, infoErr := gateway.GetObjectInfo(ctx, bucket, object+"/", minio.ObjectOptions{})
			if infoErr != nil {
				t.Fatalf("get final marker %s: %s", object, infoErr)
			}
			if info.ETag != "" || info.UserTags != "" || info.UserDefined["x-amz-meta-owner"] != "" {
				t.Fatalf("copy attributes leaked into PUT marker %s: ETag=%q tags=%q metadata=%q", object, info.ETag, info.UserTags, info.UserDefined)
			}
			for _, name := range []string{s3Etag, s3Tags, s3Meta} {
				if value, eno := jfs.GetXattr(mctx, gateway.path(bucket, object), name); eno != meta.ENOATTR {
					t.Fatalf("copy xattr %s leaked into PUT marker %s: value=%q, errno=%s", name, object, value, eno)
				}
			}
		}
	})

	t.Run("temporary marker inode is removed after publish failure", func(t *testing.T) {
		gateway, jfs, bucket := newTestGateway(t, Config{KeepEtag: true, ObjTag: true, ObjMeta: true})
		ctx := context.Background()
		var countTemporaryObjects func(string) int
		countTemporaryObjects = func(directory string) int {
			t.Helper()
			f, eno := jfs.Open(mctx, directory, 0)
			if fs.IsNotExist(eno) {
				return 0
			}
			if eno != 0 {
				t.Fatalf("open temporary directory %s: %s", directory, eno)
			}
			entries, eno := f.ReaddirPlus(mctx, 0)
			if closeErr := f.Close(mctx); closeErr != 0 {
				t.Fatalf("close temporary directory %s: %s", directory, closeErr)
			}
			if eno != 0 {
				t.Fatalf("read temporary directory %s: %s", directory, eno)
			}
			count := 0
			for _, entry := range entries {
				if entry.Attr.Typ == meta.TypeDirectory && len(entry.Name) == subDirPrefix {
					count += countTemporaryObjects(directory + sep + string(entry.Name))
					continue
				}
				count++
			}
			return count
		}
		tmpRoot := gateway.tpath(bucket, "tmp")
		invalidXattr := objectXattr{name: "", value: []byte("invalid")}
		before := countTemporaryObjects(tmpRoot)
		if err := gateway.putDirectoryObject(ctx, bucket, "/new-marker", true, []objectXattr{invalidXattr}); err == nil {
			t.Fatal("expected new marker attribute failure")
		}
		if _, eno := jfs.Stat(mctx, "/new-marker"); !fs.IsNotExist(eno) {
			t.Fatalf("failed marker became visible: %s", eno)
		}
		if after := countTemporaryObjects(tmpRoot); after != before {
			t.Fatalf("attribute failure leaked temporary marker: before=%d after=%d", before, after)
		}

		if _, err := gateway.PutObject(ctx, bucket, "existing-marker/", newPutObjectReader(t, nil), minio.ObjectOptions{}); err != nil {
			t.Fatalf("create existing marker: %s", err)
		}
		before = countTemporaryObjects(tmpRoot)
		err := gateway.publishNewDirectoryObject(ctx, bucket, gateway.path(bucket, "existing-marker"), nil)
		if !errors.Is(err, syscall.EEXIST) {
			t.Fatalf("expected marker publish collision, got %T: %v", err, err)
		}
		if after := countTemporaryObjects(tmpRoot); after != before {
			t.Fatalf("publish collision leaked temporary marker: before=%d after=%d", before, after)
		}

		before = countTemporaryObjects(tmpRoot)
		if err = gateway.putDirectoryObject(ctx, bucket, gateway.path(bucket, "successful-marker"), true, nil); err != nil {
			t.Fatalf("publish new marker: %s", err)
		}
		if after := countTemporaryObjects(tmpRoot); after != before {
			t.Fatalf("successful publish left temporary marker: before=%d after=%d", before, after)
		}
	})
}

func TestCompleteMultipartUploadIfNoneMatch(t *testing.T) {
	t.Run("existing object remains unchanged and a new object is created", func(t *testing.T) {
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
	})

	t.Run("concurrent completions have one winner", func(t *testing.T) {
		gateway, _, bucket := newTestGateway(t, Config{})
		ctx := context.Background()
		const writers = 8
		const object = "race"
		type attempt struct {
			uploadID string
			part     minio.CompletePart
			payload  []byte
		}
		attempts := make([]attempt, writers)
		for i := range attempts {
			payload := []byte(fmt.Sprintf("multipart-writer-%02d", i))
			uploadID, err := gateway.NewMultipartUpload(ctx, bucket, object, minio.ObjectOptions{})
			if err != nil {
				t.Fatalf("create multipart upload %d: %s", i, err)
			}
			part, err := gateway.PutObjectPart(ctx, bucket, object, uploadID, 1, newPutObjectReader(t, payload), minio.ObjectOptions{})
			if err != nil {
				t.Fatalf("put multipart part %d: %s", i, err)
			}
			attempts[i] = attempt{
				uploadID: uploadID,
				part:     minio.CompletePart{PartNumber: 1, ETag: part.ETag},
				payload:  payload,
			}
		}

		type result struct {
			index int
			err   error
		}
		start := make(chan struct{})
		results := make(chan result, writers)
		var waitGroup sync.WaitGroup
		for i := range attempts {
			waitGroup.Add(1)
			go func(index int) {
				defer waitGroup.Done()
				<-start
				attempt := attempts[index]
				_, err := gateway.CompleteMultipartUpload(ctx, bucket, object, attempt.uploadID,
					[]minio.CompletePart{attempt.part}, minio.ObjectOptions{IfNoneMatch: true})
				results <- result{index: index, err: err}
			}(i)
		}
		close(start)
		waitGroup.Wait()
		close(results)

		succeeded := 0
		failed := 0
		winningIndex := -1
		failedIndex := -1
		for result := range results {
			if result.err == nil {
				succeeded++
				winningIndex = result.index
				continue
			}
			var preconditionFailed minio.PreConditionFailed
			if errors.As(result.err, &preconditionFailed) {
				failed++
				failedIndex = result.index
				continue
			}
			t.Fatalf("unexpected concurrent multipart error: %T: %v", result.err, result.err)
		}
		if succeeded != 1 || failed != writers-1 {
			t.Fatalf("conditional multipart results: succeeded=%d failed=%d", succeeded, failed)
		}
		if got := readGatewayObject(t, gateway, bucket, object); !bytes.Equal(got, attempts[winningIndex].payload) {
			t.Fatalf("stored payload %q does not match successful upload %q", got, attempts[winningIndex].payload)
		}

		if _, err := gateway.DeleteObject(ctx, bucket, object, minio.ObjectOptions{}); err != nil {
			t.Fatalf("delete winning object: %s", err)
		}
		failedAttempt := attempts[failedIndex]
		if _, err := gateway.CompleteMultipartUpload(ctx, bucket, object, failedAttempt.uploadID,
			[]minio.CompletePart{failedAttempt.part}, minio.ObjectOptions{IfNoneMatch: true}); err != nil {
			t.Fatalf("retry losing multipart upload: %s", err)
		}
		if got := readGatewayObject(t, gateway, bucket, object); !bytes.Equal(got, failedAttempt.payload) {
			t.Fatalf("retried multipart payload: got %q, want %q", got, failedAttempt.payload)
		}
	})
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
