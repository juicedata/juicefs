package object

import (
	"bytes"
	"fmt"
	"github.com/bradfitz/gomemcache/memcache"
	"io"
	"io/ioutil"
	"strings"
)

type cached struct {
	ObjectStorage
	hot *memcache.Client
}

func (c *cached) String() string {
	return fmt.Sprintf("cached://%s/%s", c.hot, c.ObjectStorage)
}

func (c *cached) Get(key string, off, limit int64) (io.ReadCloser, error) {
	item, err := c.hot.Get(key)
	if err != nil {
		r, err := c.ObjectStorage.Get(key, off, limit)
		if err == nil && off == 0 && limit == -1 {
			// TODO: guess the size and allocate the memory first
			data, err := ioutil.ReadAll(r)
			r.Close()
			if err != nil {
				return nil, err
			}

			go func() {
				_ = c.hot.Set(&memcache.Item{Key: key, Value: data})
			}()
			return ioutil.NopCloser(bytes.NewBuffer(data)), nil
		}
	}
	data := item.Value
	data = data[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (c *cached) Put(key string, in io.Reader) error {
	// build cache for small files.
	if rr, ok := in.(*bytes.Buffer); ok && rr.Len() > 0 {
		_ = c.hot.Set(&memcache.Item{Key: key, Value: rr.Bytes()})
	}
	return c.ObjectStorage.Put(key, in)
}

func (c *cached) Delete(key string) error {
	_ = c.hot.Delete(key)
	return c.ObjectStorage.Delete(key)
}

func NewCachedStore(cold ObjectStorage, uri string) (ObjectStorage, error) {
	if !strings.Contains(uri, "memcached://") {
		return nil, fmt.Errorf("cached-store invalid uri: %s", uri)
	}

	logger.Infof("cached-store address: %s", uri)
	p := strings.Index(uri, "://")
	if p < 0 {
		return nil, fmt.Errorf("cached-store invalid uri: %s", uri)
	}
	addr := uri[p+3:]
	client := memcache.New(addr)
	fmt.Println(addr)

	return &cached{cold, client}, nil
}
