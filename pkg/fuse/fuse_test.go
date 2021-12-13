// +build linux

/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package fuse

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hanwen/go-fuse/v2/posixtest"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
)

func format(url string) {
	m := meta.NewClient(url, &meta.Config{})
	format := meta.Format{
		Name:      "test",
		UUID:      uuid.New().String(),
		Storage:   "file",
		Bucket:    os.TempDir(),
		BlockSize: 4096,
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

	metaConf := &meta.Config{
		Retries:    10,
		Strict:     true,
		MountPoint: mp,
	}
	m := meta.NewClient(url, metaConf)
	format, err := m.Load()
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

	blob, err := object.CreateStorage(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey)
	if err != nil {
		log.Fatalf("object storage: %s", err)
	}
	blob = object.WithPrefix(blob, format.Name+"/")
	store := chunk.NewCachedStore(blob, chunkConf)

	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	}))

	conf := &vfs.Config{
		Meta:       metaConf,
		Format:     format,
		Version:    "Juicefs",
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}

	err = m.NewSession()
	if err != nil {
		log.Fatalf("new session: %s", err)
	}

	conf.AttrTimeout = time.Second
	conf.EntryTimeout = time.Second
	conf.DirEntryTimeout = time.Second
	conf.HideInternal = true
	v := vfs.NewVFS(conf, m, store)
	err = Serve(v, "", true)
	if err != nil {
		log.Fatalf("fuse server err: %s\n", err)
	}
	_ = m.CloseSession()
}

func umount(mp string, force bool) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if force {
			cmd = exec.Command("diskutil", "umount", "force", mp)
		} else {
			cmd = exec.Command("diskutil", "umount", mp)
		}
	case "linux":
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
	case "windows":
		if !force {
			_ = os.Mkdir(filepath.Join(mp, ".UMOUNTIT"), 0755)
			return nil
		} else {
			cmd = exec.Command("taskkill", "/IM", "juicefs.exe", "/F")
		}
	default:
		return fmt.Errorf("OS %s is not supported", runtime.GOOS)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print(string(out))
	}
	return err
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
	for c, f := range posixtest.All {
		cleanup(mp)
		t.Run(c, func(t *testing.T) {
			f(t, mp)
		})
	}
}
