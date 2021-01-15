/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"fmt"
	"io"
)

type withPrefix struct {
	os     ObjectStorage
	prefix string
}

// WithPrefix returns a object storage that add a prefix to keys.
func WithPrefix(os ObjectStorage, prefix string) (ObjectStorage, error) {
	return &withPrefix{os, prefix}, nil
}

func (p *withPrefix) String() string {
	return fmt.Sprintf("%s/%s", p.os, p.prefix)
}

func (p *withPrefix) Create() error {
	return p.os.Create()
}

func (p *withPrefix) Get(key string, off, limit int64) (io.ReadCloser, error) {
	return p.os.Get(p.prefix+key, off, limit)
}

func (p *withPrefix) Put(key string, in io.Reader) error {
	return p.os.Put(p.prefix+key, in)
}

func (p *withPrefix) Delete(key string) error {
	return p.os.Delete(p.prefix + key)
}

var _ ObjectStorage = &withPrefix{}
