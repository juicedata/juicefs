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
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	minio "github.com/minio/minio/cmd"
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
