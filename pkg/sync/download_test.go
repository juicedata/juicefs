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
	"os"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
)

func TestDownload(t *testing.T) {
	key := "testDownload"
	a, _ := object.CreateStorage("file", "/tmp/download/", "", "", "")
	t.Cleanup(func() {
		os.RemoveAll("/tmp/download/")
	})
	type config struct {
		concurrent int
		fsize      int64
	}
	type tcase struct {
		config
		tfunc func(t *testing.T, pr *parallelDownloader, content []byte)
	}

	tcases := []tcase{
		{config: config{fsize: downloadBufSize*3 + 100, concurrent: 4}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			res, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(res, content) {
				t.Fatalf("get wrong content by io.ReadAll")
			}
		}},

		{config: config{fsize: 97340326, concurrent: 4}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			res, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(res, content) {
				t.Fatalf("get wrong content by io.ReadAll")
			}
		}},

		{config: config{fsize: downloadBufSize*3 + 100, concurrent: 5}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			res, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(res, content) {
				t.Fatalf("get wrong content by io.ReadAll")
			}
		}},

		{config: config{fsize: 1, concurrent: 5}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			res := make([]byte, 1)
			n, err := pr.Read(res)
			if err != nil || n != 1 || res[0] != content[0] {
				t.Fatalf("read 1 byte should succeed")
			}
			n, err = pr.Read(res)
			if err != io.EOF || n != 0 {
				t.Fatalf("err should be io.EOF or n should equal 0, but got %s %d", err, n)
			}
		}},

		{config: config{fsize: 2, concurrent: 5}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
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
				t.Fatalf("err should be io.EOF or n should equal 0, but got %s %d", err, n)
			}
		}},

		{config: config{fsize: 2, concurrent: 1}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
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
				t.Fatalf("err should be io.EOF or n should equal 0, but got %s %d", err, n)
			}
		}},

		{config: config{fsize: downloadBufSize * 20, concurrent: 3}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			resSize := 4 * downloadBufSize
			res := make([]byte, 4*downloadBufSize)
			n, err := io.ReadFull(pr, res)

			if err != nil || n != resSize || res[0] != content[0] {
				t.Fatalf("read %v byte should succeed, but got %d, %s", resSize, n, err)
			}
			n, err = io.ReadFull(pr, res)
			if err != nil || n != resSize || res[0] != content[resSize] {
				t.Fatalf("read %v byte should succeed, but got %d, %s", resSize, n, err)
			}
			_ = a.Delete(key)
			n, err = io.ReadFull(pr, res)
			n, err = io.ReadFull(pr, res)
			if !os.IsNotExist(err) {
				t.Fatalf("err should be ErrNotExist, but got %d, %s", n, err)
			}
		}},

		{config: config{fsize: 0, concurrent: 5}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			res := make([]byte, 1)
			n, err := pr.Read(res)
			if err != io.EOF || n != 0 {
				t.Fatalf("err should be io.EOF or n should equal 0, but got %s %d", err, n)
			}
		}},

		{config: config{fsize: 10 * downloadBufSize, concurrent: 5}, tfunc: func(t *testing.T, pr *parallelDownloader, content []byte) {
			defer pr.Close()
			res := make([]byte, 1)
			pr.key = "notExist"
			n, err := pr.Read(res)
			if !os.IsNotExist(err) || n != 0 {
				t.Fatalf("err should be ErrNotExist or n should equal 0, but got %s %d", err, n)
			}
		}},
	}

	for _, c := range tcases {
		content := make([]byte, c.config.fsize)
		utils.RandRead(content)
		_ = a.Put(key, bytes.NewReader(content))
		c.tfunc(t, newParallelDownloader(a, key, c.config.fsize, downloadBufSize, make(chan int, c.concurrent)), content)
	}
}
