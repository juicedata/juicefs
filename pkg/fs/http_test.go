/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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

package fs

import (
	"context"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

func TestWebdav(t *testing.T) {
	jfs := createTestFS(t)
	webdavFS := &webdavFS{meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())}), jfs, uint16(utils.GetUmask()), WebdavConfig{EnableProppatch: true}}
	ctx := context.Background()
	_, err := webdavFS.Stat(ctx, "/")
	if err != nil {
		t.Fatalf("webdavFS stat failed: %s", err)
	}
	aFile, err := webdavFS.OpenFile(ctx, "/a", os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("webdavFS create failed: %s", err)
	}
	_, err = webdavFS.OpenFile(ctx, "/b/", os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("webdavFS create failed: %s", err)
	}
	aInfo, err := aFile.Stat()
	if err != nil || aInfo.Name() != "a" || aInfo.Mode().Perm() != fs.FileMode(0644) {
		t.Fatalf("webdavFS stat failed: %s", err)
	}
	if n, err := aFile.Write([]byte("world")); err != nil || n != 5 {
		t.Fatalf("webdavFS write 5 bytes: %d %s", n, err)
	}
	if n, err := aFile.Seek(-3, io.SeekEnd); err != nil || n != 2 {
		t.Fatalf("webdavFS seek 3 bytes before end: %d %s", n, err)
	}
	buf := make([]byte, 100)
	if n, err := aFile.Read(buf); err != nil || n != 3 || string(buf[:n]) != "rld" {
		t.Fatalf("webdavFS read(): %d %s %s", n, err, string(buf[:n]))
	}

	if err = webdavFS.Mkdir(ctx, "/d1", 0755); err != nil {
		t.Fatalf("webdavFS mkdir failed: %s", err)
	}
	if d1Info, err := webdavFS.Stat(ctx, "/d1"); err != nil || d1Info.Name() != "d1" || d1Info.Mode().Perm() != fs.FileMode(0755) {
		t.Fatalf("webdavFS stat failed: %s", err)
	}
	if webdavFS.Rename(ctx, "/d1", "/d2") != nil {
		t.Fatalf("webdavFS rename failed: %s", err)
	}
	if stat, err := webdavFS.Stat(ctx, "/d2"); err != nil || !stat.IsDir() {
		t.Fatalf("webdavFS rename failed: %s", err)
	}
	for _, name := range []string{"/d2/a", "/d2/b", "/d2/c", "/d2/d"} {
		if _, err := webdavFS.OpenFile(ctx, name, os.O_CREATE, 0644); err != nil {
			t.Fatalf("webdavFS create failed: %s", err)
		}
	}
	if webdavFS.RemoveAll(ctx, "/d2") != nil {
		t.Fatalf("webdavFS removeAll failed: %s", err)
	}
	if _, err = webdavFS.Stat(ctx, "/d2"); err != os.ErrNotExist {
		t.Fatalf("webdavFS removeAll failed: %s", err)
	}
	if err = aFile.Close(); err != nil {
		t.Fatalf("webdavFS close file failed: %s", err)
	}
}
