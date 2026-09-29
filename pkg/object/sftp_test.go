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
	"strconv"
	"strings"
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
	beforeWrite func([]byte, int64) error
	closeErr    error
}

func (w sftpTestFileWriter) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := w.FileWriter.Filewrite(r)
	if err != nil {
		return nil, err
	}
	return &sftpTestWriterAt{f, w.beforeWrite, w.closeErr}, nil
}

type sftpTestWriterAt struct {
	io.WriterAt
	beforeWrite func([]byte, int64) error
	closeErr    error
}

func (w *sftpTestWriterAt) WriteAt(p []byte, off int64) (int, error) {
	if w.beforeWrite != nil {
		if err := w.beforeWrite(p, off); err != nil {
			return 0, err
		}
	}
	return w.WriterAt.WriteAt(p, off)
}

func (w *sftpTestWriterAt) Close() error {
	if c, ok := w.WriterAt.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return err
		}
	}
	return w.closeErr
}

func newSftpTestStore(t *testing.T, beforeWrite func([]byte, int64) error, closeErr error) *sftpStore {
	t.Helper()
	handlers := sftp.InMemHandler()
	handlers.FilePut = sftpTestFileWriter{handlers.FilePut, beforeWrite, closeErr}
	a, b := net.Pipe()
	server := sftp.NewRequestServer(b, handlers)
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve() }()
	t.Cleanup(func() {
		_ = a.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("SFTP server did not stop")
		}
	})
	client, err := sftp.NewClientPipe(a, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &sftpStore{root: "/", pool: []*conn{{sftpClient: client, err: make(chan error, 1)}}}
}

func TestSftpPut(t *testing.T) {
	originalInplace := PutInplace
	t.Cleanup(func() { PutInplace = originalInplace })
	data := bytes.Repeat([]byte("sftp concurrent upload\n"), 12000)
	for _, inplace := range []bool{false, true} {
		name := "temporary"
		if inplace {
			name = "inplace"
		}
		t.Run(name, func(t *testing.T) {
			PutInplace = inplace
			for _, readerType := range []string{"stream", "limited", "writer-to", "short-stream", "short-limited"} {
				t.Run("readers/"+readerType, func(t *testing.T) {
					nextWrite := make(chan struct{})
					store := newSftpTestStore(t, func(p []byte, off int64) error {
						if inplace {
							return nil
						}
						switch off {
						case 0:
							select {
							case <-nextWrite:
							case <-time.After(2 * time.Second):
								return errors.New("upload waited for the first write response before sending the second request")
							}
						case 32768:
							close(nextWrite)
						}
						return nil
					}, nil)
					var in io.Reader = struct{ io.Reader }{bytes.NewReader(data)}
					if readerType == "limited" {
						in = io.LimitReader(in, int64(len(data)))
					}
					if readerType == "writer-to" {
						in = bytes.NewReader(data)
					}
					if strings.HasPrefix(readerType, "short-") {
						in = &sftpShortReader{bytes.NewReader(data), 16 << 10}
						if readerType == "short-limited" {
							in = io.LimitReader(in, int64(len(data)))
						}
					}
					if err := store.Put(context.Background(), "file", in); err != nil {
						t.Fatal(err)
					}
					got, err := get(store, "file", 0, -1)
					if err != nil || !bytes.Equal([]byte(got), data) {
						t.Fatalf("content mismatch: len=%d, err=%v", len(got), err)
					}
				})
			}
			for _, size := range []int{0, 1, 32768, 32769} {
				t.Run("size/"+strconv.Itoa(size), func(t *testing.T) {
					store := newSftpTestStore(t, nil, nil)
					if err := store.Put(context.Background(), "file", struct{ io.Reader }{bytes.NewReader(data[:size])}); err != nil {
						t.Fatal(err)
					}
					got, err := get(store, "file", 0, -1)
					if err != nil || !bytes.Equal([]byte(got), data[:size]) {
						t.Fatalf("content mismatch: len=%d, err=%v", len(got), err)
					}
				})
			}
			for _, failure := range []string{"read", "write", "close"} {
				t.Run("failure/"+failure, func(t *testing.T) {
					injected := errors.New("injected upload failure")
					var beforeWrite func([]byte, int64) error
					var closeErr error
					if failure == "write" {
						laterWrite := make(chan struct{})
						beforeWrite = func(p []byte, off int64) error {
							switch off {
							case 32768:
								if !inplace {
									select {
									case <-laterWrite:
									case <-time.After(2 * time.Second):
									}
								}
								return injected
							case 65536:
								close(laterWrite)
							}
							return nil
						}
					}
					if failure == "close" {
						closeErr = injected
					}
					store := newSftpTestStore(t, beforeWrite, closeErr)
					client := store.pool[0].sftpClient
					old := []byte("existing destination")
					f, err := client.Create("/file")
					if err != nil {
						t.Fatal(err)
					}
					if _, err = f.Write(old); err != nil {
						t.Fatal(err)
					}
					// The injected close error also applies while seeding the destination.
					_ = f.Close()
					var in io.Reader = struct{ io.Reader }{bytes.NewReader(data)}
					if failure == "read" {
						in = io.MultiReader(bytes.NewReader(data[:32768]), iotest.ErrReader(injected))
					}
					err = store.Put(context.Background(), "file", in)
					if err == nil {
						t.Fatal("expected upload failure")
					}
					if failure == "read" && !errors.Is(err, injected) {
						t.Fatalf("lost source error: %v", err)
					}
					if !inplace {
						got, err := get(store, "file", 0, -1)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal([]byte(got), old) {
							t.Fatalf("destination changed after failed upload: got %q, want %q", got, old)
						}
					}
					entries, err := client.ReadDir("/")
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 1 || entries[0].Name() != "file" {
						t.Fatalf("temporary file leaked: %v", entries)
					}
				})
			}
		})
	}
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

func TestSftpPutFailedInplaceSize(t *testing.T) {
	oldInplace := PutInplace
	PutInplace = true
	t.Cleanup(func() { PutInplace = oldInplace })
	data := bytes.Repeat([]byte("x"), (8*32768)+1)
	for _, writerTo := range []bool{false, true} {
		t.Run(strconv.FormatBool(writerTo), func(t *testing.T) {
			store := newSftpTestStore(t, func(p []byte, off int64) error {
				if off == 32768 {
					return errors.New("failed middle block")
				}
				return nil
			}, nil)
			var in io.Reader = bytes.NewReader(data)
			if !writerTo {
				in = struct{ io.Reader }{in}
			}
			err := store.Put(context.Background(), "file", in)
			if err == nil {
				t.Fatal("expected failed middle block")
			}
			got, err := get(store, "file", 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal([]byte(got), data[:32768]) {
				t.Fatalf("failed in-place upload did not leave a prefix: got %d bytes, want 32768", len(got))
			}
		})
	}
}
