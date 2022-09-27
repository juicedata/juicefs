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

package sync

import (
	"bytes"
	"io"
	"math/rand"
	"os"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
)

func TestDownload(t *testing.T) {
	key := "testDownload"
	a, _ := object.CreateStorage("file", "/tmp/download/", "", "", "")
	t.Cleanup(func() {
		os.RemoveAll("/tmp/download/")
	})
	type config struct {
		blockSize  int64
		concurrent int
		fsize      int64
	}
	type tcase struct {
		config
		tfunc func(t *testing.T, pr *parallelDownloader, content []byte)
	}

	tcases := []tcase{
		{config: config{fsize: 1110, concurrent: 4, blockSize: 300}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(res, content) {
				t.Fatalf("get wrong content by io.ReadAll")
			}
		}},

		{config: config{fsize: 97340326, concurrent: 4, blockSize: 5 << 20}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(res, content) {
				t.Fatalf("get wrong content by io.ReadAll")
			}
		}},

		{config: config{fsize: 1110, concurrent: 5, blockSize: 300}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(res, content) {
				t.Fatalf("get wrong content by io.ReadAll")
			}
		}},

		{config: config{fsize: 1, concurrent: 5, blockSize: 10}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res := make([]byte, 1)
			n, err := pr.Read(res)
			if err != nil || n != 1 || res[0] != content[0] {
				t.Fatalf("read 1 byte should succeed")
			}
			n, err = pr.Read(res)
			if err != io.EOF || n != 0 {
				t.Fatalf("err should be io.EOF or n should equal 0")
			}
		}},

		{config: config{fsize: 2, concurrent: 5, blockSize: 10}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res := make([]byte, 1)
			n, err := pr.Read(res)
			if err != nil || n != 1 || res[0] != content[0] {
				t.Fatalf("read 1 byte should succeed")
			}
			n, err = pr.Read(res)
			if err != nil || n != 1 || res[0] != content[1] {
				t.Fatalf("read 1 byte should succeed")
			}
			n, err = pr.Read(res)
			if err != io.EOF || n != 0 {
				t.Fatalf("err should be io.EOF or n should equal 0")
			}
		}},

		{config: config{fsize: 2, concurrent: 1, blockSize: 10}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res := make([]byte, 1)
			n, err := pr.Read(res)

			if err != nil || n != 1 || res[0] != content[0] {
				t.Fatalf("read 1 byte should succeed")
			}
			n, err = pr.Read(res)
			if err != nil || n != 1 || res[0] != content[1] {
				t.Fatalf("read 1 byte should succeed")
			}
			n, err = pr.Read(res)
			if err != io.EOF || n != 0 {
				t.Fatalf("err should be io.EOF or n should equal 0")
			}
		}},

		{config: config{fsize: 1000, concurrent: 3, blockSize: 5}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res := make([]byte, 20)
			n, err := io.ReadFull(pr, res)

			if err != nil || n != 20 || res[0] != content[0] {
				t.Fatalf("read 20 byte should succeed, but got %d, %s", n, err)
			}
			n, err = io.ReadFull(pr, res)
			if err != nil || n != 20 || res[0] != content[20] {
				t.Fatalf("read 20 byte should succeed, but got %d, %s", n, err)
			}
			_ = a.Delete(key)
			n, err = io.ReadFull(pr, res)
			n, err = io.ReadFull(pr, res)
			if !os.IsNotExist(err) {
				t.Fatalf("err should be ErrNotExist, but got %d, %s", n, err)
			}
		}},

		{config: config{fsize: 0, concurrent: 5, blockSize: 10}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res := make([]byte, 1)
			n, err := pr.Read(res)
			if err != io.EOF || n != 0 {
				t.Fatalf("err should be io.EOF or n should equal 0")
			}
		}},

		{config: config{fsize: 100, concurrent: 5, blockSize: 10}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			res := make([]byte, 1)
			pr.key = "notExist"
			n, err := pr.Read(res)
			if !os.IsNotExist(err) || n != 0 {
				t.Fatalf("err should be ErrNotExist or n should equal 0")
			}
		}},
	}

	for _, c := range tcases {
		content := make([]byte, c.config.fsize)
		rand.Read(content)
		_ = a.Put(key, bytes.NewReader(content))
		c.tfunc(t, newParallelDownloader(a, key, c.config.fsize, c.blockSize, make(chan int, c.concurrent)), content)
	}
}
