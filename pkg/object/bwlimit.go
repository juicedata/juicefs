/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
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
	"io/ioutil"

	"github.com/juju/ratelimit"
)

type limitedReader struct {
	io.ReadCloser
	r *ratelimit.Bucket
}

func (l *limitedReader) Read(buf []byte) (int, error) {
	n, err := l.ReadCloser.Read(buf)
	if l.r != nil {
		l.r.Wait(int64(n))
	}
	return n, err
}

// Seek call the Seek in underlying reader.
func (l *limitedReader) Seek(offset int64, whence int) (int64, error) {
	if s, ok := l.ReadCloser.(io.Seeker); ok {
		return s.Seek(offset, whence)
	}
	return 0, fmt.Errorf("%v does not support Seek()", l.ReadCloser)
}

type bwlimit struct {
	ObjectStorage
	upLimit   *ratelimit.Bucket
	downLimit *ratelimit.Bucket
}

func NewLimited(o ObjectStorage, up, down int64) ObjectStorage {
	bw := &bwlimit{o, nil, nil}
	if up > 0 {
		// there are overheads coming from HTTP/TCP/IP
		bw.upLimit = ratelimit.NewBucketWithRate(float64(up)*0.85, up)
	}
	if down > 0 {
		bw.downLimit = ratelimit.NewBucketWithRate(float64(down)*0.85, down)
	}
	return bw
}

func (p *bwlimit) Get(key string, off, limit int64) (io.ReadCloser, error) {
	r, err := p.ObjectStorage.Get(key, off, limit)
	return &limitedReader{r, p.downLimit}, err
}

func (p *bwlimit) Put(key string, in io.Reader) error {
	in = &limitedReader{ioutil.NopCloser(in), p.upLimit}
	return p.ObjectStorage.Put(key, in)
}
