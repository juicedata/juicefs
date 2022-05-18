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

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
)

func cmdBenchForObj() *cli.Command {
	return &cli.Command{
		Name:      "benchforobj",
		Action:    benchForObj,
		Category:  "TOOL",
		Usage:     "Run benchmark on a storage",
		ArgsUsage: "Bucket URL",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "storage",
				Value: "file",
				Usage: "object storage type (e.g. s3, gcs, oss, cos)",
			},
			&cli.StringFlag{
				Name:  "access-key",
				Usage: "access key for object storage (env ACCESS_KEY)",
			},
			&cli.StringFlag{
				Name:  "secret-key",
				Usage: "secret key for object storage (env SECRET_KEY)",
			},
			&cli.UintFlag{
				Name:  "file-size",
				Value: 4,
				Usage: "size of each small file in MiB",
			},
			&cli.UintFlag{
				Name:  "file-count",
				Value: 20,
				Usage: "total number of files",
			},
			&cli.UintFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   1,
				Usage:   "number of concurrent threads",
			},
		},
	}
}

var nspt, pass, failed = "not support", "pass", "failed"

func benchForObj(ctx *cli.Context) error {
	setup(ctx, 1)
	blobOrigin, err := object.CreateStorage(strings.ToLower(ctx.String("storage")), ctx.Args().First(), ctx.String("access-key"), ctx.String("secret-key"))
	if err != nil {
		logger.Fatalf("create storage failed: %v", err)
	}

	tmpdir := fmt.Sprintf("__juicefs_benchmark_for_obj_%d__/", time.Now().UnixNano())
	blob := object.WithPrefix(blobOrigin, tmpdir)
	defer func() {
		_ = blobOrigin.Delete(tmpdir)
	}()
	FSize := int(ctx.Uint("file-size")) << 20
	threads := int(ctx.Uint("threads"))
	FCount := int(ctx.Uint("file-count"))
	tty := isatty.IsTerminal(os.Stdout.Fd())
	progress := utils.NewProgress(!tty, false)
	if tty {
		nspt = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, YELLOW, "not support", RESET_SEQ)
		pass = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, GREEN, "pass", RESET_SEQ)
		failed = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, RED, "failed", RESET_SEQ)
	}
	fmt.Println("Start Functional Testing...")

	var result [][]string
	result = append(result, []string{"ITEM", "RESULT"})
	functionalTesting(blob, &result)
	printResult(result, tty)
	fmt.Println("\nStart Performance Testing ...")

	result = result[:0]
	result = append(result, []string{"ITEM", "VALUE", "COST"})

	{
		multiUploadFunc := func(key string, content [][]byte) error {
			total := len(content)
			bar := progress.AddCountBar("multi-upload", int64(total))
			defer bar.Done()
			upload, err := blob.CreateMultipartUpload(key)
			if err != nil {
				return err
			}
			parts := make([]*object.Part, total)
			pool := make(chan struct{}, threads)
			var wg sync.WaitGroup
			for i := 1; i <= total; i++ {
				pool <- struct{}{}
				wg.Add(1)
				num := i
				go func() {
					defer func() {
						<-pool
						wg.Done()
						bar.Increment()
					}()
					parts[num-1], err = blob.UploadPart(key, upload.UploadID, num, content[num-1])
					if err != nil {
						logger.Fatalf("multipart upload error: %v", err)
					}
				}()
			}
			wg.Wait()
			return blob.CompleteUpload(key, upload.UploadID, parts)
		}

		var uploadResult = []string{"multi-upload", nspt, nspt}
		fname := fmt.Sprintf("__multi_upload__test__%d__", time.Now().UnixNano())
		if err := blob.CompleteUpload("test", "fakeUploadId", []*object.Part{}); err != utils.ENOTSUP {
			total := 20
			partSize := 5 << 20 // 5M
			part := make([]byte, partSize)
			rand.Read(part)
			content := make([][]byte, total)
			for i := 0; i < total; i++ {
				content[i] = part
			}
			start := time.Now()
			if err := multiUploadFunc(fname, content); err != nil {
				logger.Fatalf("multipart upload error: %v", err)
			}
			cost := time.Since(start).Seconds()
			uploadResult[1], uploadResult[2] = colorize("multi-upload", float64(total*partSize>>20)/cost, cost, 2, tty)
			uploadResult[1] += " MiB/s"
			uploadResult[2] += " s/file"
		}
		result = append(result, uploadResult)
		if err = blob.Delete(fname); err != nil {
			logger.Fatalf("delete %s error: %v", fname, err)
		}
	}

	apis := []apiInfo{
		{
			name:   "put",
			fcount: FCount,
			title:  "upload object",
			getResult: func(cost float64) []string {
				line := []string{"upload object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("put", float64(FSize>>20*FCount)/cost, cost/float64(FCount), 2, tty)
					line[1] += " MiB/s"
					line[2] += " s/file"
				}
				return line
			},
		}, {
			name:   "get",
			fcount: FCount,
			title:  "download object",
			getResult: func(cost float64) []string {
				line := []string{"download object", failed, nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("get", float64(FSize>>20*FCount)/cost, cost/float64(FCount), 2, tty)
					line[1] += " MiB/s"
					line[2] += " s/file"
				}
				return line
			},
		}, {
			name:   "list",
			title:  "list operate",
			fcount: 100,
			getResult: func(cost float64) []string {
				line := []string{"list operate", failed, nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("list", 100/cost, cost*10, 2, tty)
					line[1] += " op/s"
					line[2] += " ms/op"
				}
				return line
			},
		}, {
			name:   "head",
			fcount: FCount,
			title:  "head object",
			getResult: func(cost float64) []string {
				line := []string{"head object", failed, nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("head", float64(FCount)/cost, cost*1000/float64(FCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "chtimes",
			fcount: FCount,
			title:  "chtimes file",
			getResult: func(cost float64) []string {
				line := []string{"chtimes file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chtimes", float64(FCount)/cost, cost*1000/float64(FCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "chmod",
			fcount: FCount,
			title:  "chmod file",
			getResult: func(cost float64) []string {
				line := []string{"chmod file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chmod", float64(FCount)/cost, cost*1000/float64(FCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "chown",
			fcount: FCount,
			title:  "chown file",
			getResult: func(cost float64) []string {
				line := []string{"chown file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chown", float64(FCount)/cost, cost*1000/float64(FCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "delete",
			fcount: FCount,
			title:  "delete object",
			getResult: func(cost float64) []string {
				line := []string{"delete object", failed, nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("delete", float64(FCount)/cost, cost*1000/float64(FCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		},
	}

	bm := &benchMarkObj{
		blob:        blob,
		progressBar: progress,
		threads:     threads,
		content:     make([]byte, FSize),
	}
	rand.Read(bm.content)

	for _, api := range apis {
		result = append(result, bm.run(api))
	}
	progress.Done()
	fmt.Printf("Benchmark finished! FileSize: %d MiB, FileCount: %d, NumThreads: %d\n", ctx.Uint("file-size"), ctx.Uint("file-count"), ctx.Uint("threads"))

	// adjust the print order
	result[1], result[2] = result[2], result[1]
	result[6], result[9] = result[9], result[6]
	printResult(result, tty)
	return nil

}

var resultRangeForObj = map[string][4]float64{
	"put":          {100, 150, 7, 14},
	"multi-upload": {20, 40, 25, 40},
	"get":          {200, 250, 5, 10},
	"list":         {15, 25, 50, 70},
	"head":         {500, 1000, 2, 4},
	"delete":       {500, 1000, 2, 4},
	"chmod":        {500, 1000, 2, 4},
	"chown":        {500, 1000, 2, 4},
	"chtimes":      {500, 1000, 2, 4},
}

func colorize(item string, value, cost float64, prec int, isatty bool) (string, string) {
	svalue := strconv.FormatFloat(value, 'f', prec, 64)
	scost := strconv.FormatFloat(cost, 'f', 2, 64)
	if isatty {
		r, ok := resultRangeForObj[item]
		if !ok {
			logger.Fatalf("Invalid item: %s", item)
		}
		var color int
		if value > r[1] { // max
			color = GREEN
		} else if value > r[0] { // min
			color = YELLOW
		} else {
			color = RED
		}
		svalue = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, color, svalue, RESET_SEQ)
		if cost < r[2] { // min
			color = GREEN
		} else if cost < r[3] { // max
			color = YELLOW
		} else {
			color = RED
		}
		scost = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, color, scost, RESET_SEQ)
	}
	return svalue, scost
}

type apiInfo struct {
	name      string
	title     string
	fcount    int
	getResult func(cost float64) []string
}

type benchMarkObj struct {
	progressBar *utils.Progress
	blob        object.ObjectStorage
	threads     int
	content     []byte
}

func (bm *benchMarkObj) run(api apiInfo) []string {
	if api.name == "chown" || api.name == "chmod" || api.name == "chtimes" {
		if err := bm.chmod("not_exists"); err == utils.ENOTSUP {
			return api.getResult(-1)
		}
	}
	var fn func(key string) error
	switch api.name {
	case "put":
		fn = bm.put
	case "get":
		fn = bm.get
	case "delete":
		fn = bm.delete
	case "head":
		fn = bm.head
	case "list":
		fn = bm.list
	case "chown":
		fn = bm.chown
	case "chmod":
		fn = bm.chmod
	case "chtimes":
		fn = bm.chtimes
	}

	var wg sync.WaitGroup
	start := time.Now()
	pool := make(chan struct{}, bm.threads)
	count := api.fcount
	if api.fcount != 0 {
		count = api.fcount
	}
	bar := bm.progressBar.AddCountBar(api.title, int64(count))
	for i := 0; i < count; i++ {
		pool <- struct{}{}
		wg.Add(1)
		key := i
		go func() {
			defer func() {
				<-pool
				wg.Done()
			}()
			if err := fn(strconv.Itoa(key)); err != nil {
				logger.Fatalf("%s test error %v", api.name, err)
			}
			bar.Increment()
		}()
	}
	wg.Wait()
	bar.Done()
	return api.getResult(time.Since(start).Seconds())
}

func (bm *benchMarkObj) put(key string) error {
	return bm.blob.Put(key, bytes.NewReader(bm.content))
}

func (bm *benchMarkObj) get(key string) error {
	r, err := bm.blob.Get(key, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(io.Discard, r)
	return err
}

func (bm *benchMarkObj) delete(key string) error {
	return bm.blob.Delete(key)
}

func (bm *benchMarkObj) head(key string) error {
	_, err := bm.blob.Head(key)
	return err
}

func (bm *benchMarkObj) list(key string) error {
	result, err := osync.ListAll(bm.blob, "", "")
	for range result {
	}
	return err
}

func (bm *benchMarkObj) chown(key string) error {
	return bm.blob.(object.FileSystem).Chown(key, "", "")
}

func (bm *benchMarkObj) chmod(key string) error {
	return bm.blob.(object.FileSystem).Chmod(key, 0755)
}

func (bm *benchMarkObj) chtimes(key string) error {
	return bm.blob.(object.FileSystem).Chtimes(key, time.Now())
}

func listAll(s object.ObjectStorage, prefix, marker string, limit int64) ([]object.Object, error) {
	r, err := s.List(prefix, marker, limit)
	if err == nil {
		return r, nil
	}
	ch, err := s.ListAll(prefix, marker)
	if err == nil {
		objs := make([]object.Object, 0)
		for obj := range ch {
			if len(objs) < int(limit) {
				objs = append(objs, obj)
			}
		}
		return objs, nil
	}
	return nil, err
}

func functionalTesting(blob object.ObjectStorage, result *[][]string) {
	runCase := func(title string, fn func(blob object.ObjectStorage) error) {
		r := pass
		if err := fn(blob); err == utils.ENOTSUP {
			r = nspt
		} else if err != nil {
			r = failed
			logger.Debugf("%s", err)
		}
		*result = append(*result, []string{title, r})
	}

	get := func(s object.ObjectStorage, k string, off, limit int64) (string, error) {
		r, err := s.Get(k, off, limit)
		if err != nil {
			return "", err
		}
		data, err := ioutil.ReadAll(r)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	key := "put_test_file"

	runCase("put and get test", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key)
		if s, err := get(blob, key, 0, -1); err != nil && s != string(br) {
			return fmt.Errorf("get object failed: %s", err)
		}
		return nil
	})

	runCase("put empty test", func(blob object.ObjectStorage) error {
		// Copy empty objects
		defer blob.Delete("empty_test_file")
		if err := blob.Put("empty_test_file", bytes.NewReader([]byte{})); err != nil {
			return fmt.Errorf("PUT empty object failed: %s", err.Error())
		}

		// Copy `/` suffixed object
		defer blob.Delete("slash_test_file/")
		if err := blob.Put("slash_test_file/", bytes.NewReader([]byte{})); err != nil {
			return fmt.Errorf("PUT `/` suffixed object failed: %s", err.Error())
		}
		return nil
	})

	runCase("get not exists object test", func(blob object.ObjectStorage) error {
		if _, err := blob.Get("not_exists_file", 0, -1); err == nil {
			return fmt.Errorf("get not exists object should failed: %s", err)
		}
		return nil
	})

	runCase("random read test", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key)
		if d, e := get(blob, key, 0, -1); d != "hello" {
			return fmt.Errorf("expect hello, but got %v, error:%s", d, e)
		}
		if d, e := get(blob, key, 2, 3); d != "llo" {
			return fmt.Errorf("expect llo, but got %v, error:%s", d, e)
		}
		if d, e := get(blob, key, 2, 2); d != "ll" {
			return fmt.Errorf("expect ll, but got %v, error:%s", d, e)
		}
		if d, e := get(blob, key, 4, 2); d != "o" {
			return fmt.Errorf("out-of-range get: 'o', but got %v, error:%s", len(d), e)
		}
		return nil
	})

	runCase("head test", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key)
		if h, err := blob.Head(key); err != nil {
			return fmt.Errorf("check exists failed: %s", err)
		} else {
			if h.Key() != key {
				return fmt.Errorf("expected get key is test but get: %s", h.Key())
			}
		}
		return nil
	})

	runCase("delete test", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		if err := blob.Delete(key); err != nil {
			return fmt.Errorf("delete failed: %s", err)
		}
		if _, err := blob.Head(key); err == nil {
			return fmt.Errorf("expect err is not nil")
		}

		if err := blob.Delete(key); err != nil {
			return fmt.Errorf("delete non exists: %v", err)
		}
		return nil
	})

	runCase("list test", func(blob object.ObjectStorage) error {
		isFileSystem := true
		fi, ok := blob.(object.FileSystem)
		if ok {
			if err := fi.Chmod("not_exists_file", 0755); err == utils.ENOTSUP {
				isFileSystem = false
			}
		}
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key)
		if isFileSystem {
			objs, err := listAll(blob, "", "", 2)
			if err == nil {
				if len(objs) != 2 {
					return fmt.Errorf("list should return 2 keys, but got %d", len(objs))
				}
				if objs[0].Key() != "" {
					return fmt.Errorf("first key should be empty string, but got %s", objs[0].Key())
				}
				if objs[0].Size() != 0 {
					return fmt.Errorf("first object size should be 0, but got %d", objs[0].Size())
				}
				if objs[1].Key() != key {
					return fmt.Errorf("first key should be test, but got %s", objs[1].Key())
				}
				if !strings.Contains(blob.String(), "encrypted") && objs[1].Size() != 5 {
					return fmt.Errorf("size of first key shold be 5, but got %v", objs[1].Size())
				}
				now := time.Now()
				if objs[1].Mtime().Before(now.Add(-30*time.Second)) || objs[1].Mtime().After(now.Add(time.Second*30)) {
					return fmt.Errorf("mtime of key should be within 10 seconds, but got %s", objs[1].Mtime().Sub(now))
				}
			} else {
				return fmt.Errorf("list failed: %s", err.Error())
			}

			objs, err = listAll(blob, "", "test2", 1)
			if err != nil {
				return fmt.Errorf("list failed: %s", err.Error())
			} else if len(objs) != 0 {
				return fmt.Errorf("list should not return anything, but got %d", len(objs))
			}
		} else {
			objs, err2 := listAll(blob, "", "", 1)
			if err2 == nil {
				if len(objs) != 1 {
					return fmt.Errorf("list should return 1 keys, but got %d", len(objs))
				}
				if objs[0].Key() != key {
					return fmt.Errorf("first key should be test, but got %s", objs[0].Key())
				}
				if !strings.Contains(blob.String(), "encrypted") && objs[0].Size() != 5 {
					return fmt.Errorf("size of first key shold be 5, but got %v", objs[0].Size())
				}
				now := time.Now()
				if objs[0].Mtime().Before(now.Add(-30*time.Second)) || objs[0].Mtime().After(now.Add(time.Second*30)) {
					return fmt.Errorf("mtime of key should be within 10 seconds, but got %s", objs[0].Mtime().Sub(now))
				}
			} else {
				return fmt.Errorf("list failed: %s", err2.Error())
			}

			objs, err2 = listAll(blob, "", "test2", 1)
			if err2 != nil {
				return fmt.Errorf("list failed: %s", err2.Error())
			} else if len(objs) != 0 {
				return fmt.Errorf("list should not return anything, but got %d", len(objs))
			}
		}
		return nil
	})

	runCase("multi test", func(blob object.ObjectStorage) error {
		key := "multi_test_file"
		if err := blob.CompleteUpload(key, "notExistsUploadId", []*object.Part{}); err != utils.ENOTSUP {
			defer blob.Delete(key) //nolint:errcheck
			if uploader, err := blob.CreateMultipartUpload(key); err == nil {
				partSize := uploader.MinPartSize
				uploadID := uploader.UploadID
				defer blob.AbortUpload(key, uploadID)

				part1, err := blob.UploadPart(key, uploadID, 1, make([]byte, partSize))
				if err != nil {
					return fmt.Errorf("UploadPart 1 failed: %s", err)
				}
				part2Size := 1 << 20
				_, err = blob.UploadPart(key, uploadID, 2, make([]byte, part2Size))
				if err != nil {
					return fmt.Errorf("UploadPart 2 failed: %s", err)
				}
				part2Size = 2 << 20
				part2, err := blob.UploadPart(key, uploadID, 2, make([]byte, part2Size))
				if err != nil {
					return fmt.Errorf("UploadPart 2 failed: %s", err)
				}

				if err := blob.CompleteUpload(key, uploadID, []*object.Part{part1, part2}); err != nil {
					return fmt.Errorf("CompleteMultipart failed: %s", err.Error())
				}
				if in, err := blob.Get(key, 0, -1); err != nil {
					return fmt.Errorf("%s not exists", key)
				} else if d, err := ioutil.ReadAll(in); err != nil {
					return fmt.Errorf("fail to read %s", key)
				} else if len(d) != partSize+part2Size {
					return fmt.Errorf("size of %s file: %d != %d", key, len(d), partSize+part2Size)
				}
				return nil
			}
		}
		return utils.ENOTSUP
	})
}
