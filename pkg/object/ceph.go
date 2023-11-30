//go:build ceph
// +build ceph

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

package object

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/ceph/go-ceph/rados"
)

type ceph struct {
	DefaultObjectStorage
	name string
	conn *rados.Conn
	free chan *rados.IOContext
}

func (c *ceph) String() string {
	return fmt.Sprintf("ceph://%s/", c.name)
}

func (c *ceph) Create() error {
	names, err := c.conn.ListPools()
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == c.name {
			return nil
		}
	}
	return c.conn.MakePool(c.name)
}

func (c *ceph) newContext() (*rados.IOContext, error) {
	select {
	case ctx := <-c.free:
		return ctx, nil
	default:
		ctx, err := c.conn.OpenIOContext(c.name)
		if err == nil {
			_ = ctx.SetPoolFullTry()
		}
		return ctx, err
	}
}

func (c *ceph) release(ctx *rados.IOContext) {
	select {
	case c.free <- ctx:
	default:
		ctx.Destroy()
	}
}

func (c *ceph) do(f func(ctx *rados.IOContext) error) (err error) {
	ctx, err := c.newContext()
	if err != nil {
		return err
	}
	err = f(ctx)
	if err != nil {
		ctx.Destroy()
	} else {
		c.release(ctx)
	}
	return
}

type cephReader struct {
	c     *ceph
	ctx   *rados.IOContext
	key   string
	off   int64
	limit int64
}

func (r *cephReader) Read(buf []byte) (n int, err error) {
	if r.limit == 0 {
		return 0, io.EOF
	}
	if r.limit > 0 && int64(len(buf)) > r.limit {
		buf = buf[:r.limit]
	}
	n, err = r.ctx.Read(r.key, buf, uint64(r.off))
	r.off += int64(n)
	if r.limit > 0 {
		r.limit -= int64(n)
	}
	if err == nil && n < len(buf) {
		err = io.EOF
	}
	return
}

func (r *cephReader) Close() error {
	if r.ctx != nil {
		r.c.release(r.ctx)
		r.ctx = nil
	}
	return nil
}

func (c *ceph) Get(key string, off, limit int64) (io.ReadCloser, error) {
	if _, err := c.Head(key); err != nil {
		return nil, err
	}
	ctx, err := c.newContext()
	if err != nil {
		return nil, err
	}
	return &cephReader{c, ctx, key, off, limit}, nil
}

var cephPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 1<<20)
	},
}

func (c *ceph) Put(key string, in io.Reader) error {
	// ceph default osd_max_object_size = 128M
	return c.do(func(ctx *rados.IOContext) error {
		if b, ok := in.(*bytes.Reader); ok {
			v := reflect.ValueOf(b)
			data := v.Elem().Field(0).Bytes()
			if len(data) == 0 {
				return notSupported
			}
			// If the data exceeds 90M, ceph will report an error: 'rados: ret=-90, Message too long'
			if len(data) < 85<<20 {
				return ctx.WriteFull(key, data)
			}
		}
		buf := cephPool.Get().([]byte)
		defer cephPool.Put(buf)
		var off uint64
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if err = ctx.Write(key, buf[:n], off); err != nil {
					return err
				}
				off += uint64(n)
			} else {
				if err == io.EOF {
					if off == 0 {
						return errors.New("ceph: can't put empty file")
					}
					return nil
				}
				return err
			}
		}
	})
}

func (c *ceph) Delete(key string) error {
	err := c.do(func(ctx *rados.IOContext) error {
		return ctx.Delete(key)
	})
	if err == rados.ErrNotFound {
		err = nil
	}
	return err
}

func (c *ceph) Head(key string) (Object, error) {
	var o *obj
	err := c.do(func(ctx *rados.IOContext) error {
		stat, err := ctx.Stat(key)
		if err != nil {
			return err
		}
		o = &obj{key, int64(stat.Size), stat.ModTime, strings.HasSuffix(key, "/"), ""}
		return nil
	})
	if err == rados.ErrNotFound {
		err = os.ErrNotExist
	}
	return o, err
}

func (c *ceph) ListAll(prefix, marker string, followLink bool) (<-chan Object, error) {
	ctx, err := c.newContext()
	if err != nil {
		return nil, err
	}
	iter, err := ctx.Iter()
	if err != nil {
		ctx.Destroy()
		return nil, err
	}
	defer iter.Close()

	// FIXME: this will be really slow for many objects
	keys := make([]string, 0, 1000)
	for iter.Next() {
		key := iter.Value()
		if key <= marker || !strings.HasPrefix(key, prefix) {
			continue
		}
		keys = append(keys, key)
	}
	// the keys are not ordered, sort them first
	sort.Strings(keys)
	c.release(ctx)

	var objs = make(chan Object, 1000)
	var concurrent = 20
	ms := make([]sync.Mutex, concurrent)
	conds := make([]*sync.Cond, concurrent)
	ready := make([]bool, concurrent)
	results := make([]Object, concurrent)
	errs := make([]error, concurrent)
	for c := 0; c < concurrent; c++ {
		conds[c] = sync.NewCond(&ms[c])
		if c < len(keys) {
			go func(c int) {
				ctx, err := c.newContext()
				if err != nil {
					logger.Errorf("new context: %s", err)
					errs[c] = err
					return
				}
				defer ctx.Destroy()
				for i := c; i < len(keys); i += concurrent {
					key := keys[i]
					st, err := ctx.Stat(key)
					if err != nil {
						if errors.Is(err, rados.ErrNotFound) {
							logger.Debugf("Skip non-existent key: %s", key)
							results[c] = nil
						} else {
							logger.Errorf("Stat key %s: %s", key, err)
							errs[c] = err
						}
					} else {
						results[c] = &obj{key, int64(st.Size), st.ModTime, strings.HasSuffix(key, "/"), ""}
					}

					ms[c].Lock()
					ready[c] = true
					conds[c].Signal()
					if errs[c] != nil {
						ms[c].Unlock()
						break
					}
					for ready[c] {
						conds[c].Wait()
					}
					ms[c].Unlock()
				}
			}(c)
		}
	}
	go func() {
		defer close(objs)
		for i := range keys {
			c := i % concurrent
			ms[c].Lock()
			for !ready[c] {
				conds[c].Wait()
			}
			if errs[c] != nil {
				objs <- nil
				ms[c].Unlock()
				// some goroutines will be leaked, but it's ok
				// since we won't call ListAll() many times in a process
				break
			} else if results[c] != nil {
				objs <- results[c]
			}
			ready[c] = false
			conds[c].Signal()
			ms[c].Unlock()
		}
	}()
	return objs, nil
}

func newCeph(endpoint, cluster, user, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("ceph://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	name := uri.Host
	conn, err := rados.NewConnWithClusterAndUser(cluster, user)
	if err != nil {
		return nil, fmt.Errorf("Can't create connection to cluster %s for user %s: %s", cluster, user, err)
	}
	if os.Getenv("JFS_NO_CHECK_OBJECT_STORAGE") == "" {
		if err := conn.ReadDefaultConfigFile(); err != nil {
			return nil, fmt.Errorf("Can't read default config file: %s", err)
		}
		if err := conn.Connect(); err != nil {
			return nil, fmt.Errorf("Can't connect to cluster %s: %s", cluster, err)
		}
	}
	return &ceph{
		name: name,
		conn: conn,
		free: make(chan *rados.IOContext, 50),
	}, nil
}

func init() {
	Register("ceph", newCeph)
}
