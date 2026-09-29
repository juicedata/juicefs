//go:build !nosftp
// +build !nosftp

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

package object

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/pkg/sftp"
)

func TestSftp(t *testing.T) { //skip mutate
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	b, _ := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	testStorage(t, b)
	if _, err := b.Head(context.Background(), "unit-test/"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("testStorage left unit-test/ behind: %v", err)
	}
}

func TestSftp2(t *testing.T) { //skip mutate
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	sftp, err := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	if err != nil {
		t.Fatalf("sftp: %s", err)
	}
	testFileSystem(t, sftp)
}

func TestParseSftpEndpoint(t *testing.T) {
	tests := []struct {
		name               string
		endpoint           string
		wantHost, wantPort string
		wantRoot           string
		wantErr            string
	}{
		{
			name:     "default port with timestamp colons",
			endpoint: "host:/path/T05:53:21",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "explicit port with timestamp colons",
			endpoint: "host:2022:/path/T05:53:21",
			wantHost: "host",
			wantPort: "2022",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "IPv6 explicit port with timestamp colons",
			endpoint: "[2001:db8::1]:2022:/path/T05:53:21",
			wantHost: "2001:db8::1",
			wantPort: "2022",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "IPv6 default port with timestamp colons",
			endpoint: "[2001:db8::1]:/path/T05:53:21",
			wantHost: "2001:db8::1",
			wantPort: "22",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "relative path remains supported",
			endpoint: "host:backup/path",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "backup/path",
		},
		{
			name:     "relative path with timestamp colons",
			endpoint: "host:T05:53:21",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "T05:53:21",
		},
		{
			name:     "relative path with ISO timestamp colons",
			endpoint: "host:2026-07-23T05:53:21",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "2026-07-23T05:53:21",
		},
		{
			name:     "explicit port with relative path",
			endpoint: "host:2022:backup/path",
			wantHost: "host",
			wantPort: "2022",
			wantRoot: "backup/path",
		},
		{
			name:     "relative path with symbols",
			endpoint: "host:!@#$%^&*()_+-=[]{}|;,.<>?",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "!@#$%^&*()_+-=[]{}|;,.<>?",
		},
		{
			name:     "relative path starts with colon",
			endpoint: "host::backup/path",
			wantHost: "host",
			wantPort: "22",
			wantRoot: ":backup/path",
		},
		{
			name:     "numeric relative path",
			endpoint: "host:2022",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "2022",
		},
		{
			name:     "empty path",
			endpoint: "host:",
			wantErr:  "missing path",
		},
		{
			name:     "explicit port with empty path",
			endpoint: "host:2022:",
			wantErr:  "missing path",
		},
		{
			name:     "IPv6 default port with empty path",
			endpoint: "[2001:db8::1]:",
			wantErr:  "missing path",
		},
		{
			name:     "missing port path separator",
			endpoint: "host:2022/path",
			wantErr:  "missing colon between port and path",
		},
		{
			name:     "IPv6 missing port path separator",
			endpoint: "[2001:db8::1]:2022/path",
			wantErr:  "missing colon between port and path",
		},
		{
			name:     "console endpoint missing port path separator",
			endpoint: "172.28.39.219:22/root/bak/jfs-console-dump-2026-07-23T05:53:21.json.gz.gpg",
			wantErr:  "missing colon between port and path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, port, root, err := parseSftpEndpoint(test.endpoint)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseSftpEndpoint(%q) error = %v, want error containing %q", test.endpoint, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if host != test.wantHost || port != test.wantPort || root != test.wantRoot {
				t.Fatalf("parseSftpEndpoint(%q) = (%q, %q, %q), want (%q, %q, %q)",
					test.endpoint, host, port, root, test.wantHost, test.wantPort, test.wantRoot)
			}
		})
	}
}

type sftpTestFileWriter struct {
	sftp.FileWriter
	beforeWrite func(off int64) error
}

func (w sftpTestFileWriter) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := w.FileWriter.Filewrite(r)
	if err != nil {
		return nil, err
	}
	return sftpTestWriterAt{f, w.beforeWrite}, nil
}

type sftpTestWriterAt struct {
	io.WriterAt
	beforeWrite func(off int64) error
}

func (w sftpTestWriterAt) WriteAt(p []byte, off int64) (int, error) {
	if err := w.beforeWrite(off); err != nil {
		return 0, err
	}
	return w.WriterAt.WriteAt(p, off)
}

func newSftpTestStore(t *testing.T, beforeWrite func(off int64) error) *sftpStore {
	handlers := sftp.InMemHandler()
	handlers.FilePut = sftpTestFileWriter{handlers.FilePut, beforeWrite}
	a, b := net.Pipe()
	server := sftp.NewRequestServer(b, handlers)
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(a, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return &sftpStore{root: "/", pool: []*conn{{sftpClient: client, err: make(chan error, 1)}}}
}

// sftpShortReader returns at most n bytes per Read, like a TLS-backed HTTP body.
type sftpShortReader struct {
	r io.Reader
	n int
}

func (r *sftpShortReader) Read(p []byte) (int, error) {
	if len(p) > r.n {
		p = p[:r.n]
	}
	return r.r.Read(p)
}

func TestSftpPut(t *testing.T) {
	defer func(v bool) { PutInplace = v }(PutInplace)
	data := bytes.Repeat([]byte("sftp concurrent upload\n"), 12000)
	injected := errors.New("injected failure")

	PutInplace = false
	t.Run("pipelined", func(t *testing.T) {
		second := make(chan struct{})
		store := newSftpTestStore(t, func(off int64) error {
			switch off {
			case 0:
				select {
				case <-second:
				case <-time.After(2 * time.Second):
					return errors.New("second write was not sent before the first one completed")
				}
			case 32768:
				close(second)
			}
			return nil
		})
		if err := store.Put(context.Background(), "file", &sftpShortReader{bytes.NewReader(data), 16 << 10}); err != nil {
			t.Fatal(err)
		}
		if got, err := get(store, "file", 0, -1); err != nil || got != string(data) {
			t.Fatalf("content mismatch: len=%d, err=%v", len(got), err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		store := newSftpTestStore(t, func(int64) error { return nil })
		if err := store.Put(context.Background(), "file", bytes.NewReader(nil)); err != nil {
			t.Fatal(err)
		}
		if got, err := get(store, "file", 0, -1); err != nil || got != "" {
			t.Fatalf("got %q, err=%v", got, err)
		}
	})
	for _, failure := range []string{"read", "write"} {
		t.Run("failure/"+failure, func(t *testing.T) {
			var failing atomic.Bool
			store := newSftpTestStore(t, func(off int64) error {
				if failing.Load() && failure == "write" && off == 32768 {
					return injected
				}
				return nil
			})
			if err := store.Put(context.Background(), "file", bytes.NewReader([]byte("old"))); err != nil {
				t.Fatal(err)
			}
			failing.Store(true)
			var in io.Reader = bytes.NewReader(data)
			if failure == "read" {
				in = io.MultiReader(bytes.NewReader(data[:32768]), iotest.ErrReader(injected))
			}
			// Write errors come back as SFTP status errors; source errors must be returned as is.
			if err := store.Put(context.Background(), "file", in); err == nil || failure == "read" && !errors.Is(err, injected) {
				t.Fatalf("expected injected failure, got %v", err)
			}
			if got, err := get(store, "file", 0, -1); err != nil || got != "old" {
				t.Fatalf("destination changed after failed upload: %q, err=%v", got, err)
			}
			if entries, err := store.pool[0].sftpClient.ReadDir("/"); err != nil || len(entries) != 1 {
				t.Fatalf("temporary file leaked: %v, err=%v", entries, err)
			}
		})
	}

	PutInplace = true
	t.Run("inplace/failure", func(t *testing.T) {
		big := data[:8*32768+1]
		// A WriterTo source reaches File.Write instead of File.ReadFrom.
		for _, in := range []io.Reader{struct{ io.Reader }{bytes.NewReader(big)}, bytes.NewReader(big)} {
			store := newSftpTestStore(t, func(off int64) error {
				if off == 32768 {
					return injected
				}
				return nil
			})
			if err := store.Put(context.Background(), "file", in); err == nil {
				t.Fatal("expected injected failure")
			}
			if got, err := get(store, "file", 0, -1); err != nil || got != string(big[:32768]) {
				t.Fatalf("failed in-place upload left %d bytes, want a 32768-byte prefix, err=%v", len(got), err)
			}
		}
	})
}
