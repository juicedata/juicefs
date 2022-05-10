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
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
	"golang.org/x/net/webdav"
)

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

type gzipHandler struct {
	handler http.Handler
}

func (g *gzipHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		g.handler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	gz := gzip.NewWriter(w)
	defer gz.Close()
	gzr := gzipResponseWriter{Writer: gz, ResponseWriter: w}
	g.handler.ServeHTTP(gzr, r)
}

func makeGzipHandler(h http.Handler) http.Handler {
	return &gzipHandler{h}
}

var errmap = map[syscall.Errno]error{
	0:              nil,
	syscall.EPERM:  os.ErrPermission,
	syscall.ENOENT: os.ErrNotExist,
	syscall.EEXIST: os.ErrExist,
}

func econv(err error) error {
	if err == nil {
		return nil
	}
	eno, ok := err.(syscall.Errno)
	if !ok {
		return err
	}
	if e, ok := errmap[eno]; ok {
		return e
	}
	return err
}

type webdavFS struct {
	ctx meta.Context
	fs  *FileSystem
}

func (hfs *webdavFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return econv(hfs.fs.Mkdir(hfs.ctx, name, uint16(perm)))
}

func (hfs *webdavFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	var mode int
	if flag&(os.O_RDONLY|os.O_RDWR) != 0 {
		mode |= vfs.MODE_MASK_R
	}
	if flag&(os.O_APPEND|os.O_RDWR|os.O_WRONLY) != 0 {
		mode |= vfs.MODE_MASK_W
	}
	if flag&(os.O_EXCL) != 0 {
		mode |= vfs.MODE_MASK_X
	}
	name = strings.TrimRight(name, "/")
	f, err := hfs.fs.Open(hfs.ctx, name, uint32(mode))
	if err != 0 {
		if err == syscall.ENOENT && flag&os.O_CREATE != 0 {
			f, err = hfs.fs.Create(hfs.ctx, name, uint16(perm))
		}
	} else if flag&os.O_TRUNC != 0 {
		if errno := hfs.fs.Truncate(hfs.ctx, name, 0); errno != 0 {
			return nil, errno
		}
	} else if flag&os.O_APPEND != 0 {
		if _, err := f.Seek(hfs.ctx, 0, 2); err != nil {
			return nil, err
		}
	}
	return &davFile{f}, econv(err)
}

func (hfs *webdavFS) RemoveAll(ctx context.Context, name string) error {
	return econv(hfs.fs.Rmr(hfs.ctx, name))
}

func (hfs *webdavFS) Rename(ctx context.Context, oldName, newName string) error {
	return econv(hfs.fs.Rename(hfs.ctx, oldName, newName, 0))
}

func (hfs *webdavFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	fi, err := hfs.fs.Stat(hfs.ctx, removeNewLine(name))
	return fi, econv(err)
}

type davFile struct {
	*File
}

func (f *davFile) Seek(offset int64, whence int) (int64, error) {
	n, err := f.File.Seek(meta.Background, offset, whence)
	return n, econv(err)
}

func (f *davFile) Read(b []byte) (n int, err error) {
	n, err = f.File.Read(meta.Background, b)
	return n, econv(err)
}

func (f *davFile) Write(buf []byte) (n int, err error) {
	n, err = f.File.Write(meta.Background, buf)
	return n, econv(err)
}

func (f *davFile) Readdir(count int) (fi []os.FileInfo, err error) {
	fi, err = f.File.Readdir(meta.Background, count)
	// skip the first two (. and ..)
	for len(fi) > 0 && (fi[0].Name() == "." || fi[0].Name() == "..") {
		fi = fi[1:]
	}
	return fi, econv(err)
}

func (f *davFile) Close() error {
	return econv(f.File.Close(meta.Background))
}

type indexHandler struct {
	*webdav.Handler
	disallowList bool
}

func (h *indexHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Excerpt from RFC4918, section 9.4:
	//
	// 		GET, when applied to a collection, may return the contents of an
	//		"index.html" resource, a human-readable view of the contents of
	//		the collection, or something else altogether.
	//
	// Get, when applied to collection, will return the same as PROPFIND method.
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, h.Handler.Prefix) {
		info, err := h.Handler.FileSystem.Stat(context.TODO(), strings.TrimPrefix(r.URL.Path, h.Handler.Prefix))
		if err == nil && info.IsDir() {
			if h.disallowList {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			r.Method = "PROPFIND"
			if r.Header.Get("Depth") == "" {
				r.Header.Add("Depth", "1")
			}
		}
	}
	h.Handler.ServeHTTP(w, r)
}

func StartHTTPServer(fs *FileSystem, addr string, gzipEnabled bool, disallowList bool) {
	ctx := meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	hfs := &webdavFS{ctx, fs}
	srv := &webdav.Handler{
		FileSystem: hfs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				logger.Errorf("WEBDAV [%s]: %s, ERROR: %s", r.Method, r.URL, err)
			} else {
				logger.Debugf("WEBDAV [%s]: %s", r.Method, r.URL)
			}
		},
	}
	var h http.Handler = &indexHandler{srv, disallowList}
	if gzipEnabled {
		h = makeGzipHandler(h)
	}
	http.Handle("/", h)
	logger.Infof("WebDAV listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logger.Fatalf("Error with WebDAV server: %v", err)
	}
}

func removeNewLine(input string) string {
	return strings.Replace(strings.Replace(input, "\n", "", -1), "\r", "", -1)
}
