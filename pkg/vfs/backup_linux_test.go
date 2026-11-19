/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package vfs

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type backupWriterMeta struct {
	meta.Meta
	payload []byte
	err     error
}

func (m backupWriterMeta) Name() string { return "test" }

func (m backupWriterMeta) DumpMeta(w io.Writer, _ meta.Ino, _ int, _, _, _ bool) error {
	if _, err := w.Write(m.payload); err != nil {
		return err
	}
	return m.err
}

func TestBackupGzipCloseErrors(t *testing.T) {
	// A subprocess confines the process-wide file limit and signal handling;
	// other VFS tests can leave background backup goroutines running.
	if os.Getenv("JUICEFS_TEST_BACKUP_GZIP_CLOSE") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBackupGzipCloseErrors$", "-test.v")
		cmd.Env = append(os.Environ(), "JUICEFS_TEST_BACKUP_GZIP_CLOSE=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("backup subprocess: %v\n%s", err, output)
		}
		return
	}

	dumpErr := errors.New("metadata dump failed")
	for _, tc := range []struct {
		name    string
		limited bool
		dumpErr error
	}{
		{name: "complete"},
		{name: "close_error", limited: true},
		{name: "dump_error", limited: true, dumpErr: dumpErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			blob, err := object.CreateStorage("file", t.TempDir()+"/", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if tc.limited {
				var original syscall.Rlimit
				if err = syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
					t.Fatal(err)
				}
				ignored := signal.Ignored(syscall.SIGXFSZ)
				signal.Ignore(syscall.SIGXFSZ)
				t.Cleanup(func() {
					if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
						t.Error(err)
					}
					if !ignored {
						signal.Reset(syscall.SIGXFSZ)
					}
				})
				limit := original
				// The gzip header fits; flushing the buffered data/footer does not.
				limit.Cur = 10
				if err = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
			key := "meta/dump-" + now.Format("2006-01-02-150405") + ".json.gz"
			payload := []byte(`{"Setting":{},"Counters":{},"Sustained":[]}`)
			path, err := backup(backupWriterMeta{payload: payload, err: tc.dumpErr}, blob, now, true, false)
			if tc.limited {
				want := error(syscall.EFBIG)
				if tc.dumpErr != nil {
					want = tc.dumpErr
				}
				if !errors.Is(err, want) {
					t.Fatalf("backup returned path %q, error %v; want %v", path, err, want)
				}
				if _, err = blob.Head(context.Background(), key); !os.IsNotExist(err) {
					t.Fatalf("failed backup published an object: %v", err)
				}
				return
			}
			if err != nil || path == "" {
				t.Fatalf("complete backup: path %q, error %v", path, err)
			}
			reader, err := blob.Get(context.Background(), key, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			zr, err := gzip.NewReader(reader)
			if err != nil {
				t.Fatal(err)
			}
			defer zr.Close()
			got, err := io.ReadAll(zr)
			if err != nil || string(got) != string(payload) {
				t.Fatalf("backup contents: %q, error %v", got, err)
			}
		})
	}
}
