// +build !notikv

/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package object

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"time"

	"github.com/tikv/client-go/v2/config"
	"github.com/tikv/client-go/v2/rawkv"
)

type tikv struct {
	DefaultObjectStorage
	c    *rawkv.Client
	addr string
}

func (t *tikv) String() string {
	return fmt.Sprintf("tikv://%s/", t.addr)
}

func (t *tikv) Get(key string, off, limit int64) (io.ReadCloser, error) {
	d, err := t.c.Get(context.TODO(), []byte(key))
	if len(d) == 0 {
		err = errors.New("not found")
	}
	if err != nil {
		return nil, err
	}
	data := d[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (t *tikv) Put(key string, in io.Reader) error {
	d, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	return t.c.Put(context.TODO(), []byte(key), d)
}

func (t *tikv) Head(key string) (Object, error) {
	data, err := t.c.Get(context.TODO(), []byte(key))
	return &obj{
		key,
		int64(len(data)),
		time.Now(),
		strings.HasSuffix(key, "/"),
	}, err
}

func (t *tikv) Delete(key string) error {
	return t.c.Delete(context.TODO(), []byte(key))
}

func (t *tikv) List(prefix, marker string, limit int64) ([]Object, error) {
	return nil, errors.New("not supported")
}

func newTiKV(endpoint, accesskey, secretkey string) (ObjectStorage, error) {
	pds := strings.Split(endpoint, ",")
	for i, pd := range pds {
		pd = strings.TrimSpace(pd)
		if !strings.Contains(pd, ":") {
			pd += ":2379"
		}
		pds[i] = pd
	}
	c, err := rawkv.NewClient(context.TODO(), pds, config.DefaultConfig().Security)
	if err != nil {
		return nil, err
	}
	return &tikv{c: c, addr: endpoint}, nil
}

func init() {
	Register("tikv", newTiKV)
}
