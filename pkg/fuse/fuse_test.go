//go:build linux
// +build linux

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

//nolint:errcheck
package fuse

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/hanwen/go-fuse/v2/posixtest"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/pkg/xattr"
)

func format(url string) {
	m := meta.NewClient(url, nil)
	format := &meta.Format{
		Name:      "test",
		UUID:      uuid.New().String(),
		Storage:   "file",
		Bucket:    os.TempDir() + "/",
		BlockSize: 4096,
		DirStats:  true,
	}
	err := m.Init(format, true)
	if err != nil {
		log.Fatalf("format: %s", err)
	}
}

func mount(url, mp string) {
	if err := os.MkdirAll(mp, 0777); err != nil {
		log.Fatalf("create %s: %s", mp, err)
	}

	metaConf := meta.DefaultConf()
	metaConf.MountPoint = mp
	m := meta.NewClient(url, metaConf)
	format, err := m.Load(true)
	if err != nil {
		log.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize:  format.BlockSize * 1024,
		Compress:   format.Compression,
		MaxUpload:  20,
		BufferSize: 300 << 20,
		CacheSize:  1024,
		CacheDir:   "memory",
	}

	blob, err := object.CreateStorage(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey, format.SessionToken)
	if err != nil {
		log.Fatalf("object storage: %s", err)
	}
	blob = object.WithPrefix(blob, format.Name+"/")
	store := chunk.NewCachedStore(blob, chunkConf, nil)

	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		sliceId := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, sliceId)
	}))

	conf := &vfs.Config{
		Meta:     metaConf,
		Format:   *format,
		Chunk:    &chunkConf,
		FuseOpts: &vfs.FuseOptions{},
	}

	err = m.NewSession(true)
	if err != nil {
		log.Fatalf("new session: %s", err)
	}

	conf.AttrTimeout = time.Second
	conf.EntryTimeout = time.Second
	conf.DirEntryTimeout = time.Second
	conf.HideInternal = true
	v := vfs.NewVFS(conf, m, store, nil, nil)
	err = Serve(v, "", true, true)
	if err != nil {
		log.Fatalf("fuse server err: %s\n", err)
	}
	_ = m.CloseSession()
}

func umount(mp string, force bool) {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("fusermount"); err == nil {
		if force {
			cmd = exec.Command("fusermount", "-uz", mp)
		} else {
			cmd = exec.Command("fusermount", "-u", mp)
		}
	} else {
		if force {
			cmd = exec.Command("umount", "-l", mp)
		} else {
			cmd = exec.Command("umount", mp)
		}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print(string(out))
	}
}

func waitMountpoint(mp string) chan error {
	ch := make(chan error, 1)
	for i := 0; i < 20; i++ {
		time.Sleep(time.Millisecond * 500)
		st, err := os.Stat(mp)
		if err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == 1 {
				ch <- nil
				return ch
			}
		}
	}
	ch <- errors.New("not ready in 10 seconds")
	return ch
}

func setUp(metaUrl, mp string) error {
	format(metaUrl)
	go mount(metaUrl, mp)
	return <-waitMountpoint(mp)
}

func cleanup(mp string) {
	parent, err := os.Open(mp)
	if err != nil {
		return
	}
	defer parent.Close()
	names, err := parent.Readdirnames(-1)
	if err != nil {
		return
	}
	for _, n := range names {
		os.RemoveAll(filepath.Join(mp, n))
	}
}

func StatFS(t *testing.T, mp string) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mp, &st); err != nil {
		t.Fatal(err)
	}
	if st.Bsize != 4096 {
		t.Fatalf("bsize should be 4096 but got %d ", st.Bsize)
	}
	if st.Blocks-st.Bavail != 0 {
		t.Fatalf("used blocks should be 0 but got %d", st.Blocks-st.Bavail)
	}
	if st.Files-st.Ffree != 0 {
		t.Fatalf("used files should be 0 but got %d", st.Files)
	}
}

func Xattrs(t *testing.T, mp string) {
	path := filepath.Join(mp, "myfile")
	os.WriteFile(path, []byte(""), 0644)

	const prefix = "user."
	var value = []byte("test-attr-value")
	if err := xattr.Set(path, prefix+"test", value); err != nil {
		t.Fatal(err)
	}
	if _, err := xattr.List(path); err != nil {
		t.Fatal(err)
	}

	if data, err := xattr.Get(path, prefix+"test"); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(data, value) {
		t.Fatalf("expect %v bot got %v", value, data)
	}
	if err := xattr.Remove(path, prefix+"test"); err != nil {
		t.Fatal(err)
	}
	// One can also specify the flags parameter to be passed to the OS.
	if err := xattr.SetWithFlags(path, prefix+"test", []byte("test-attr-value2"), xattr.XATTR_CREATE); err != nil {
		t.Fatal(err)
	}
}

func Flock(t *testing.T, mp string) {
	path := filepath.Join(mp, "go-lock.lock")
	os.WriteFile(path, []byte(""), 0644)

	fileLock := flock.New(path)
	locked, err := fileLock.TryLock()
	if err != nil {
		t.Fatalf("try lock: %s", err)
	}
	if locked {
		fileLock.Unlock()
	} else {
		t.Fatal("no lock")
	}
}

func PosixLock(t *testing.T, mp string) {
	path := filepath.Join(mp, "go-lock.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("hello")
	if err := f.Sync(); err != nil {
		t.Fatalf("fsync: %s", err)
	}
	var fl syscall.Flock_t
	fl.Pid = int32(os.Getpid())
	fl.Type = syscall.F_WRLCK
	fl.Whence = io.SeekStart
	err = syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &fl)
	for err == syscall.EAGAIN {
		err = syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &fl)
	}
	if err != nil {
		t.Fatalf("lock: %s", err)
	}
	if err = syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &fl); err != nil {
		t.Fatalf("getlk: %s", err)
	}
	if int(fl.Pid) != os.Getpid() {
		t.Fatalf("pid: %d != %d", fl.Pid, os.Getpid())
	}
	fl.Type = syscall.F_UNLCK
	if err = syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &fl); err != nil {
		t.Fatalf("unlock: %s", err)
	}
}

func TestFUSE(t *testing.T) {
	f, err := os.CreateTemp("", "meta")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	metaUrl := "sqlite3://" + f.Name()
	mp, err := os.MkdirTemp("", "mp")
	if err != nil {
		t.Fatal(err)
	}
	err = setUp(metaUrl, mp)
	if err != nil {
		t.Fatalf("setup: %s", err)
	}
	defer umount(mp, true)

	t.Run("StatFS", func(t *testing.T) {
		StatFS(t, mp)
	})
	delete(posixtest.All, "FdLeak")
	delete(posixtest.All, "FcntlFlockLocksFile") // FIXME: check gofuse in posixtest/posixtest_test.go
	posixtest.All["Xattrs"] = Xattrs
	posixtest.All["Flock"] = Flock
	posixtest.All["POSIXLock"] = PosixLock
	for c, f := range posixtest.All {
		cleanup(mp)
		t.Run(c, func(t *testing.T) {
			f(t, mp)
		})
	}
}
