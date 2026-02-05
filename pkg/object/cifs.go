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
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cloudsoda/go-smb2"
)

type cifsConn struct {
	session  *smb2.Session
	share    *smb2.Share
	lastUsed time.Time
}

var _ ObjectStorage = (*cifsStore)(nil)
var _ FileSystem = (*cifsStore)(nil)

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

// Chmod changes the mode of the file to mode.
//
// Note: SAMBA protocol has limited support for Unix file permissions.
// it controls the FILE_ATTRIBUTE_READONLY attribute. All other permission bits are ignored.
//
// Examples:
//   - chmod(0644), chmod(0666), chmod(0755) -> file becomes writable(666)
//   - chmod(0444), chmod(0400), chmod(0555) -> file becomes read-only(444)
//
// The returned mode from Stat() will always be either 0666 (writable) or 0444 (read-only)
// regardless of the specific mode bits passed to this function.
func (c *cifsStore) Chmod(path string, mode os.FileMode) error {
	return c.withConn(context.Background(), func(conn *cifsConn) error {
		return conn.share.Chmod(path, mode)
	})
}

// Chown implements FileSystem.
func (c *cifsStore) Chown(path string, owner string, group string) error {
	return notSupported
}

// Chtimes implements MtimeChanger.
func (c *cifsStore) Chtimes(path string, mtime time.Time) error {
	return c.withConn(context.Background(), func(conn *cifsConn) error {
		return conn.share.Chtimes(path, time.Time{}, mtime)
	})
}

func (c *cifsStore) String() string {
	return fmt.Sprintf("cifs://%s@%s:%s/%s/", c.user, c.host, c.port, c.share)
}

// getConnection returns a CIFS connection from the pool or creates a new one
func (c *cifsStore) getConnection(ctx context.Context) (*cifsConn, error) {
	now := time.Now()
	for {
		select {
		case conn := <-c.pool:
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
	address := net.JoinHostPort(c.host, c.port)
	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     c.user,
			Password: c.password,
		},
	}

	var err error
	conn.session, err = d.Dial(ctx, address)
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

func (c *cifsStore) withConn(ctx context.Context, f func(*cifsConn) error) error {
	conn, err := c.getConnection(ctx)
	if err != nil {
		return err
	}
	err = f(conn)
	c.releaseConnection(conn, err)
	return err
}

func (c *cifsStore) Head(ctx context.Context, key string) (oj Object, err error) {
	err = c.withConn(ctx, func(conn *cifsConn) error {
		fi, err := conn.share.Lstat(key)
		if err != nil {
			return err
		}
		isSymlink := fi.Mode()&os.ModeSymlink != 0
		if isSymlink {
			// SMB doesn't fully support symlinks like POSIX, but we'll try our best
			fi, err = conn.share.Stat(key)
			if err != nil {
				return err
			}
		}
		oj = c.fileInfo(key, fi, isSymlink)
		return nil
	})
	return oj, err
}

func (c *cifsStore) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	var readCloser io.ReadCloser
	err := c.withConn(ctx, func(conn *cifsConn) error {
		f, err := conn.share.Open(key)
		if err != nil {
			return err
		}
		finfo, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		if finfo.IsDir() || off >= finfo.Size() {
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

func (c *cifsStore) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) (err error) {
	return c.withConn(ctx, func(conn *cifsConn) error {
		p := key
		if strings.HasSuffix(key, dirSuffix) {
			// perm will not take effect, is not used
			// ref: https://github.com/cloudsoda/go-smb2/blob/c8e61c7a5fa7bcd1143359f071f9425a9f4dda3f/client.go#L341-L370
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
			tmp = TmpFilePath(p, name)
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
			err = conn.share.Rename(tmp, p)
		}
		return err
	})
}

func (c *cifsStore) Delete(ctx context.Context, key string, getters ...AttrGetter) (err error) {
	return c.withConn(ctx, func(conn *cifsConn) error {
		p := strings.TrimRight(key, dirSuffix)
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

func (c *cifsStore) List(ctx context.Context, prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "/" {
		return nil, false, "", notSupported
	}

	dir := prefix
	var objs []Object
	if !strings.HasSuffix(dir, "/") {
		dir = path.Dir(dir)
		if !strings.HasSuffix(dir, dirSuffix) {
			dir += dirSuffix
		}
	} else if marker == "" {
		obj, err := c.Head(ctx, prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, false, "", nil
			}
			return nil, false, "", err
		}
		objs = append(objs, obj)
	}
	var mEntries []*mEntry
	err := c.withConn(ctx, func(conn *cifsConn) error {
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

func (c *cifsStore) Copy(ctx context.Context, dst, src string) error {
	r, err := c.Get(ctx, src, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	return c.Put(ctx, dst, r)
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
	conn, err := store.getConnection(context.Background())
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
