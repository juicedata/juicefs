//go:build !nocifs
// +build !nocifs

/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/hirochachacha/go-smb2"
)

type cifsConn struct {
	session  *smb2.Session
	share    *smb2.Share
	lastUsed time.Time
}

var _ ObjectStorage = &cifsStore{}

type cifsStore struct {
	DefaultObjectStorage
	host            string
	port            string
	share           string
	user            string
	password        string
	pool            chan *cifsConn
	connIdleTimeout time.Duration
}

func (c *cifsStore) String() string {
	return fmt.Sprintf("cifs://%s@%s:%s/%s/", c.user, c.host, c.port, c.share)
}

// path converts object key to file path in CIFS share
func (c *cifsStore) path(key string) string {
	return key
}

// getConnection returns a CIFS connection from the pool or creates a new one
func (c *cifsStore) getConnection() (*cifsConn, error) {
	now := time.Now()
	for {
		select {
		case conn := <-c.pool:
			if conn == nil {
				continue
			}
			if conn.session == nil {
				continue
			}
			// TODO: do it in a new goroutine?
			if now.Sub(conn.lastUsed) > c.connIdleTimeout {
				_ = conn.session.Logoff()
				continue
			}
			conn.lastUsed = now
			return conn, nil
		default:
			goto CREATE
		}
	}

CREATE:
	// Create new connection
	// FIXME: may create a large number of connection in a short period, exceeding the limit.
	conn := &cifsConn{}
	conn.lastUsed = now

	// Establish SMB connection
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	address := net.JoinHostPort(c.host, c.port)
	netConn, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %v", address, err)
	}

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     c.user,
			Password: c.password,
		},
	}

	conn.session, err = d.Dial(netConn)
	if err != nil {
		return nil, fmt.Errorf("SMB authentication failed: %v", err)
	}

	conn.share, err = conn.session.Mount(c.share)
	if err != nil {
		_ = conn.session.Logoff()
		return nil, fmt.Errorf("failed to mount SMB share %s: %v", c.share, err)
	}

	return conn, nil
}

// releaseConnection returns a connection to the pool or closes it if there's an error
func (c *cifsStore) releaseConnection(conn *cifsConn, err error) {
	if conn == nil {
		return
	}

	if err == nil {
		select {
		case c.pool <- conn:
			return
		default:
		}
	}

	// close connection if there's an error or if the pool is full
	if conn.session != nil {
		_ = conn.session.Logoff()
	}
}

func (c *cifsStore) withConn(f func(*cifsConn) error) error {
	conn, err := c.getConnection()
	if err != nil {
		return err
	}
	err = f(conn)
	c.releaseConnection(conn, err)
	return err
}

func (c *cifsStore) Head(key string) (oj Object, err error) {
	err = c.withConn(func(conn *cifsConn) error {
		p := c.path(key)
		fi, err := conn.share.Lstat(p)
		if err != nil {
			return err
		}
		isSymlink := fi.Mode()&os.ModeSymlink != 0
		if isSymlink {
			// SMB doesn't fully support symlinks like POSIX, but we'll try our best
			fi, err = conn.share.Stat(p)
			if err != nil {
				return err
			}
		}
		oj = c.fileInfo(key, fi, isSymlink)
		return nil
	})
	return oj, err
}

func (c *cifsStore) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	var readCloser io.ReadCloser
	err := c.withConn(func(conn *cifsConn) error {
		p := c.path(key)
		f, err := conn.share.Open(p)
		if err != nil {
			return err
		}
		finfo, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		if finfo.IsDir() || off > finfo.Size() {
			_ = f.Close()
			readCloser = io.NopCloser(bytes.NewBuffer([]byte{}))
			return nil
		}
		if limit > 0 {
			readCloser = &SectionReaderCloser{
				SectionReader: io.NewSectionReader(f, off, limit),
				Closer:        f,
			}
			return nil
		}
		readCloser = f
		return nil
	})
	return readCloser, err
}

func (c *cifsStore) Put(key string, in io.Reader, getters ...AttrGetter) (err error) {
	return c.withConn(func(conn *cifsConn) error {
		p := c.path(key)
		if strings.HasSuffix(p, dirSuffix) {
			// perm will not take effect, is not used
			// ref: https://github.com/hirochachacha/go-smb2/blob/c8e61c7a5fa7bcd1143359f071f9425a9f4dda3f/client.go#L341-L370
			return conn.share.MkdirAll(p, 0755)
		}

		var tmp string
		if PutInplace {
			tmp = p
		} else {
			name := path.Base(p)
			if len(name) > 200 {
				name = name[:200]
			}
			tmp = path.Join(path.Dir(p), fmt.Sprintf(".%s.tmp.%d", name, rand.Int()))
			defer func() {
				if err != nil {
					_ = conn.share.Remove(tmp)
				}
			}()
		}

		f, err := conn.share.Create(tmp)
		if err != nil && os.IsNotExist(err) {
			dirPath := path.Dir(p)
			if dirPath != "/" {
				err = conn.share.MkdirAll(dirPath, 0755)
				if err != nil {
					return err
				}
			}
			f, err = conn.share.Create(tmp)
		}
		if err != nil {
			return err
		}

		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)
		_, err = io.CopyBuffer(f, in, *buf)
		if err != nil {
			_ = f.Close()
			return err
		}

		err = f.Close()
		if err != nil {
			return err
		}

		if !PutInplace {
			err := conn.share.Rename(tmp, p)
			if err != nil && os.IsNotExist(err) {
				_ = conn.share.Remove(tmp)
				return conn.share.Rename(tmp, p)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *cifsStore) Delete(key string, getters ...AttrGetter) (err error) {
	return c.withConn(func(conn *cifsConn) error {
		p := strings.TrimRight(c.path(key), dirSuffix)
		err = conn.share.Remove(p)
		if err != nil && os.IsNotExist(err) {
			err = nil
		}
		return err
	})
}

func (c *cifsStore) fileInfo(key string, fi os.FileInfo, isSymlink bool) Object {
	owner, group := "nobody", "nobody"
	ff := &file{
		obj{key, fi.Size(), fi.ModTime(), fi.IsDir(), ""},
		owner,
		group,
		fi.Mode(),
		isSymlink,
	}
	if fi.IsDir() {
		if key != "" && !strings.HasSuffix(key, "/") {
			ff.key += "/"
		}
	}
	return ff
}

func (c *cifsStore) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "/" {
		return nil, false, "", notSupported
	}

	dir := c.path(prefix)
	if !strings.HasSuffix(dir, "/") {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	}
	var mEntries []*mEntry
	err := c.withConn(func(conn *cifsConn) error {
		// Ensure directory exists before listing
		_, err := conn.share.Stat(dir)
		if err != nil {
			return err
		}

		// Read directory entries
		entries, err := conn.share.ReadDir(dir)
		if err != nil {
			return err
		}

		// Process entries
		mEntries = make([]*mEntry, 0, len(entries))
		for _, e := range entries {
			isSymlink := e.Mode()&os.ModeSymlink != 0
			if e.IsDir() {
				mEntries = append(mEntries, &mEntry{e, e.Name() + dirSuffix, nil, false})
			} else if isSymlink && followLink {
				// SMB doesn't fully support symlinks like POSIX, but we'll try our best
				fi, err := conn.share.Stat(path.Join(dir, e.Name()))
				if err != nil {
					mEntries = append(mEntries, &mEntry{e, e.Name(), nil, true})
					continue
				}
				name := e.Name()
				if fi.IsDir() {
					name = e.Name() + dirSuffix
				}
				mEntries = append(mEntries, &mEntry{e, name, fi, false})
			} else {
				mEntries = append(mEntries, &mEntry{e, e.Name(), nil, isSymlink})
			}
		}
		return nil
	})
	if os.IsNotExist(err) || os.IsPermission(err) {
		logger.Warnf("skip %s: %s", dir, err)
		return nil, false, "", nil
	}

	// Sort entries by name
	sort.Slice(mEntries, func(i, j int) bool { return mEntries[i].Name() < mEntries[j].Name() })

	// Generate object list
	var objs []Object
	for _, e := range mEntries {
		p := path.Join(dir, e.Name())
		if e.IsDir() && !strings.HasSuffix(p, "/") {
			p = p + "/"
		}
		key := p
		if !strings.HasPrefix(key, prefix) || (marker != "" && key <= marker) {
			continue
		}

		info := e.Info()
		f := c.fileInfo(key, info, e.isSymlink)
		objs = append(objs, f)
		if len(objs) == int(limit) {
			break
		}
	}

	return generateListResult(objs, limit)
}

func (c *cifsStore) Copy(dst, src string) error {
	r, err := c.Get(src, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	return c.Put(dst, r)
}

func parseEndpoint(endpoint string) (host, port, share string, err error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = "cifs://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return
	}
	if u.Scheme != "" && (u.Scheme != "cifs" && u.Scheme != "smb") {
		err = fmt.Errorf("invalid scheme %s, should be cifs:// or smb://", u.Scheme)
		return
	}

	host = u.Hostname()
	port = u.Port()
	if port == "" {
		port = "445" // Default SMB port
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) < 2 || parts[1] == "" {
		err = fmt.Errorf("endpoint should be a valid share name (%s)", "\\\\<server>\\<share>")
		return
	}
	if len(parts) > 2 && parts[2] != "" {
		err = fmt.Errorf("endpoint should be a valid share name (%s)", "\\\\<server>\\<share>")
		return
	}
	share = parts[1]
	return
}

func newCifs(endpoint, username, password, _ string) (ObjectStorage, error) {
	host, port, share, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, fmt.Errorf("CIFS username/ak is required")
	}

	if password == "" {
		return nil, fmt.Errorf("CIFS password/sk is required")
	}

	store := &cifsStore{
		host:            host,
		port:            port,
		share:           share,
		user:            username,
		password:        password,
		connIdleTimeout: 5 * time.Minute,
		pool:            make(chan *cifsConn, 8),
	}

	// Test connection
	conn, err := store.getConnection()
	if err != nil {
		return nil, err
	}
	store.releaseConnection(conn, nil)

	return store, nil
}

func init() {
	// Allow both cifs:// and smb:// schemes
	Register("cifs", newCifs)
	Register("smb", newCifs)
}
