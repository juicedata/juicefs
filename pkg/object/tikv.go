//go:build !notikv
// +build !notikv

/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"net/url"
	"os"
	"strings"
	"time"

	plog "github.com/pingcap/log"
	"github.com/sirupsen/logrus"
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

func (t *tikv) Get(key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	d, err := t.c.Get(context.TODO(), []byte(key))
	if len(d) == 0 {
		err = os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if off > int64(len(d)) {
		off = int64(len(d))
	}
	data := d[off:]
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}
	return io.NopCloser(bytes.NewBuffer(data)), nil
}

func (t *tikv) Put(key string, in io.Reader, getters ...AttrGetter) error {
	d, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return t.c.Put(context.TODO(), []byte(key), d)
}

func (t *tikv) Head(key string) (Object, error) {
	data, err := t.c.Get(context.TODO(), []byte(key))
	if err == nil && data == nil {
		return nil, os.ErrNotExist
	}
	return &obj{
		key,
		int64(len(data)),
		time.Now(),
		strings.HasSuffix(key, "/"),
		"",
	}, err
}

func (t *tikv) Delete(key string, getters ...AttrGetter) error {
	return t.c.Delete(context.TODO(), []byte(key))
}

func (t *tikv) List(prefix, marker, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if delimiter != "" {
		return nil, false, "", notSupported
	}
	if marker == "" {
		marker = prefix
	}
	if limit > int64(rawkv.MaxRawKVScanLimit) {
		limit = int64(rawkv.MaxRawKVScanLimit)
	}
	// TODO: key only
	keys, vs, err := t.c.Scan(context.TODO(), []byte(marker), nil, int(limit))
	if err != nil {
		return nil, false, "", err
	}
	var objs = make([]Object, len(keys))
	mtime := time.Now()
	for i, k := range keys {
		// FIXME: mtime
		objs[i] = &obj{string(k), int64(len(vs[i])), mtime, strings.HasSuffix(string(k), "/"), ""}
	}
	return generateListResult(objs, limit)
}

func newTiKV(endpoint, accesskey, secretkey, token string) (ObjectStorage, error) {
	var plvl string // TiKV (PingCap) uses uber-zap logging, make it less verbose
	switch logger.Level {
	case logrus.TraceLevel:
		plvl = "debug"
	case logrus.DebugLevel:
		plvl = "info"
	case logrus.InfoLevel, logrus.WarnLevel:
		plvl = "warn"
	case logrus.ErrorLevel:
		plvl = "error"
	default:
		plvl = "dpanic"
	}
	l, prop, _ := plog.InitLogger(&plog.Config{Level: plvl})
	plog.ReplaceGlobals(l, prop)

	if !strings.HasPrefix(endpoint, "tikv://") {
		endpoint = "tikv://" + endpoint
	}
	tUrl, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	pds := strings.Split(tUrl.Host, ",")
	for i, pd := range pds {
		pd = strings.TrimSpace(pd)
		if !strings.Contains(pd, ":") {
			pd += ":2379"
		}
		pds[i] = pd
	}

	q := tUrl.Query()
	c, err := rawkv.NewClient(context.TODO(), pds, config.NewSecurity(
		q.Get("ca"),
		q.Get("cert"),
		q.Get("key"),
		strings.Split(q.Get("verify-cn"), ",")))

	if err != nil {
		return nil, err
	}
	return &tikv{c: c, addr: tUrl.Host}, nil
}

func init() {
	Register("tikv", newTiKV)
}
