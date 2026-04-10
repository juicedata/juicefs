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

func TestGetObjModTime(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	fileModTime := now.Add(-time.Hour)
	mtimeStr := now.Format(time.RFC3339)

	tests := []struct {
		name     string
		objMeta  map[string]string
		expected time.Time
	}{
		{
			name:     "nil metadata returns fileModTime",
			objMeta:  nil,
			expected: fileModTime,
		},
		{
			name:     "empty metadata returns fileModTime",
			objMeta:  map[string]string{},
			expected: fileModTime,
		},
		{
			name:     "missing mtime key returns fileModTime",
			objMeta:  map[string]string{"x-amz-meta-other": "value"},
			expected: fileModTime,
		},
		{
			name:     "empty mtime value returns fileModTime",
			objMeta:  map[string]string{"x-amz-meta-mtime": ""},
			expected: fileModTime,
		},
		{
			name:     "lowercase mtime key works",
			objMeta:  map[string]string{"x-amz-meta-mtime": mtimeStr},
			expected: now,
		},
		{
			name:     "uppercase mtime key works (case-insensitive)",
			objMeta:  map[string]string{"X-AMZ-META-MTIME": mtimeStr},
			expected: now,
		},
		{
			name:     "mixed case mtime key works",
			objMeta:  map[string]string{"X-Amz-Meta-Mtime": mtimeStr},
			expected: now,
		},
		{
			name:     "invalid mtime format returns fileModTime",
			objMeta:  map[string]string{"x-amz-meta-mtime": "invalid-time"},
			expected: fileModTime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jfsObj := &jfsObjects{gConf: &Config{UseMetaModTime: true}}
			result := jfsObj.getObjModTime(tt.objMeta, fileModTime)
			if !result.Equal(tt.expected) {
				t.Errorf("getObjModTime() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetObjModTimeDisabled(t *testing.T) {
	fileModTime := time.Now()
	jfsObj := &jfsObjects{gConf: &Config{UseMetaModTime: false}}

	objMeta := map[string]string{"x-amz-meta-mtime": time.Now().Format(time.RFC3339)}
	result := jfsObj.getObjModTime(objMeta, fileModTime)

	if !result.Equal(fileModTime) {
		t.Errorf("getObjModTime() should return fileModTime when UseMetaModTime is false, got %v", result)
	}
}
