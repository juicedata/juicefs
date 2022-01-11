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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
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
