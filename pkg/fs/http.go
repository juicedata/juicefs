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
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
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
	ctx    meta.Context
	fs     *FileSystem
	umask  uint16
	config WebdavConfig
}

func (hfs *webdavFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return econv(hfs.fs.Mkdir(hfs.ctx, name, uint16(perm), hfs.umask))
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
			f, err = hfs.fs.Create(hfs.ctx, name, uint16(perm), hfs.umask)
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
	return &davFile{f, hfs.ctx, hfs.fs, hfs.config}, econv(err)
}

func (hfs *webdavFS) RemoveAll(ctx context.Context, name string) error {
	return econv(hfs.fs.Rmr(hfs.ctx, name, hfs.config.MaxDeletes))
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
	mctx   meta.Context
	fs     *FileSystem
	config WebdavConfig
}

const webdavDeadProps = "webdav-dead-props"

type localProperty struct {
	N xml.Name        `json:"name"`
	P webdav.Property `json:"property"`
}

func (f *davFile) DeadProps() (map[xml.Name]webdav.Property, error) {
	if !f.config.EnableProppatch {
		return nil, nil
	}
	result, err := f.fs.GetXattr(f.mctx, f.path, webdavDeadProps)
	if err != 0 {
		if errors.Is(err, meta.ENOATTR) {
			return nil, nil
		}
		return nil, econv(err)
	}

	var lProperty []localProperty
	if err := json.Unmarshal(result, &lProperty); err != nil {
		return nil, econv(err)
	}
	var property = make(map[xml.Name]webdav.Property)
	for _, p := range lProperty {
		property[p.N] = p.P
	}
	return property, nil
}

func (f *davFile) Patch(patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	if !f.config.EnableProppatch {
		return nil, nil
	}
	pstat := webdav.Propstat{Status: http.StatusOK}
	deadProps, err := f.DeadProps()
	if err != nil {
		return nil, err
	}
	for _, patch := range patches {
		for _, p := range patch.Props {
			pstat.Props = append(pstat.Props, webdav.Property{XMLName: p.XMLName})
			if patch.Remove && deadProps != nil {
				delete(deadProps, p.XMLName)
				continue
			}
			if deadProps == nil {
				deadProps = map[xml.Name]webdav.Property{}
			}
			deadProps[p.XMLName] = p
		}
	}

	if deadProps != nil {
		var property []localProperty
		for name, p := range deadProps {
			property = append(property, localProperty{N: name, P: p})
		}

		jsonData, err := json.Marshal(&property)
		if err != nil {
			return nil, err
		}
		errno := f.fs.SetXattr(f.mctx, f.path, webdavDeadProps, jsonData, 0)
		if errno != 0 {
			return nil, econv(errno)
		}
	}
	return []webdav.Propstat{pstat}, nil
}

func (f *davFile) Seek(offset int64, whence int) (int64, error) {
	n, err := f.File.Seek(meta.Background(), offset, whence)
	return n, econv(err)
}

func (f *davFile) Read(b []byte) (n int, err error) {
	n, err = f.File.Read(meta.Background(), b)
	return n, econv(err)
}

func (f *davFile) Write(buf []byte) (n int, err error) {
	n, err = f.File.Write(meta.Background(), buf)
	return n, econv(err)
}

func (f *davFile) Readdir(count int) (fi []os.FileInfo, err error) {
	fi, err = f.File.Readdir(meta.Background(), count)
	// skip the first two (. and ..)
	for len(fi) > 0 && (fi[0].Name() == "." || fi[0].Name() == "..") {
		fi = fi[1:]
	}
	return fi, econv(err)
}

func (f *davFile) Close() error {
	return econv(f.File.Close(meta.Background()))
}

type WebdavConfig struct {
	Addr            string
	DisallowList    bool
	EnableProppatch bool
	EnableGzip      bool
	Username        string
	Password        string
	CertFile        string
	KeyFile         string
	MaxDeletes	int
}

type indexHandler struct {
	*webdav.Handler
	WebdavConfig
}

func (h *indexHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	//http://www.webdav.org/specs/rfc4918.html#n-guidance-for-clients-desiring-to-authenticate
	if h.Username != "" && h.Password != "" {
		userName, pwd, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if userName != h.Username || pwd != h.Password {
			http.Error(w, "WebDAV: need authorized!", http.StatusUnauthorized)
			return
		}
	}

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
			if h.DisallowList {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			r.Method = "PROPFIND"
			if r.Header.Get("Depth") == "" {
				r.Header.Add("Depth", "1")
			}
		}
	}

	// The next line would normally be:
	//	http.Handle("/", h)
	// but we wrap that HTTP handler h to cater for a special case.
	//
	// The propfind_invalid2 litmus test case expects an empty namespace prefix
	// declaration to be an error. The FAQ in the webdav litmus test says:
	//
	// "What does the "propfind_invalid2" test check for?...
	//
	// If a request was sent with an XML body which included an empty namespace
	// prefix declaration (xmlns:ns1=""), then the server must reject that with
	// a "400 Bad Request" response, as it is invalid according to the XML
	// Namespace specification."
	//
	// On the other hand, the Go standard library's encoding/xml package
	// accepts an empty xmlns namespace, as per the discussion at
	// https://github.com/golang/go/issues/8068
	//
	// Empty namespaces seem disallowed in the second (2006) edition of the XML
	// standard, but allowed in a later edition. The grammar differs between
	// http://www.w3.org/TR/2006/REC-xml-names-20060816/#ns-decl and
	// http://www.w3.org/TR/REC-xml-names/#dt-prefix
	//
	// Thus, we assume that the propfind_invalid2 test is obsolete, and
	// hard-code the 400 Bad Request response that the test expects.
	if r.Header.Get("X-Litmus") == "props: 3 (propfind_invalid2)" {
		http.Error(w, "400 Bad Request", http.StatusBadRequest)
		return
	}

	if !h.EnableProppatch && r.Method == "PROPPATCH" {
		http.Error(w, "The PROPPATCH method is not currently enabled,please add the --enable-proppatch parameter and run it again", http.StatusNotImplemented)
		return
	}

	h.Handler.ServeHTTP(w, r)
}

func StartHTTPServer(fs *FileSystem, config WebdavConfig) {
	ctx := meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	hfs := &webdavFS{ctx, fs, uint16(utils.GetUmask()), config}
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
	var h http.Handler = &indexHandler{Handler: srv, WebdavConfig: config}
	if config.EnableGzip {
		h = makeGzipHandler(h)
	}
	http.Handle("/", h)
	logger.Infof("WebDAV listening on %s", config.Addr)
	var err error
	if config.CertFile != "" && config.KeyFile != "" {
		err = http.ListenAndServeTLS(config.Addr, config.CertFile, config.KeyFile, nil)
	} else {
		err = http.ListenAndServe(config.Addr, nil)
	}
	if err != nil {
		logger.Fatalf("Error with WebDAV server: %v", err)
	}
}

func removeNewLine(input string) string {
	return strings.Replace(strings.Replace(input, "\n", "", -1), "\r", "", -1)
}
