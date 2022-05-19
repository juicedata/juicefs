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

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math"
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
		Name:      "objbench",
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
				Name:  "block-size",
				Value: 4096,
				Usage: "size of each IO block in KiB",
			},
			&cli.UintFlag{
				Name:  "data-object-size",
				Value: 1024,
				Usage: "size of test data in MiB",
			},
			&cli.UintFlag{
				Name:  "small-object-size",
				Value: 128,
				Usage: "size of each small object in KiB",
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

var nspt, pass = "not support", "pass"

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
	bSize := int(ctx.Uint("block-size")) << 10
	fsize := int(ctx.Uint("data-object-size")) << 20
	smallBSize := int(ctx.Uint("small-object-size")) << 10
	bCount := int(math.Ceil(float64(fsize) / float64(bSize)))
	threads := int(ctx.Uint("threads"))
	tty := isatty.IsTerminal(os.Stdout.Fd())
	progress := utils.NewProgress(!tty, false)
	if tty {
		nspt = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, YELLOW, "not support", RESET_SEQ)
		pass = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, GREEN, "pass", RESET_SEQ)
	}
	fmt.Println("Start Functional Testing ...")

	var result [][]string
	result = append(result, []string{"CATEGORY", "TEST", "RESULT"})
	functionalTesting(blob, fsize, &result, tty)
	printResult(result, tty)
	fmt.Println("\nStart Performance Testing ...")
	var pResult [][]string
	pResult = append(pResult, []string{"ITEM", "VALUE", "COST"})

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
			total := bCount
			part := make([]byte, 5<<20) // minio minimum requirement 5m
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
			uploadResult[1], uploadResult[2] = colorize("multi-upload", float64(total*5)/cost, cost, 2, tty)
			uploadResult[1] += " MiB/s"
			uploadResult[2] += " s/file"
		}
		pResult = append(pResult, uploadResult)
		if err = blob.Delete(fname); err != nil {
			logger.Fatalf("delete %s error: %v", fname, err)
		}
	}

	apis := []apiInfo{
		{
			name:  "put",
			count: bCount,
			title: "upload object",
			getResult: func(cost float64) []string {
				line := []string{"upload object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("put", float64(bSize>>20*bCount)/cost, cost/float64(bCount), 2, tty)
					line[1] += " MiB/s"
					line[2] += " s/file"
				}
				return line
			},
		}, {
			name:  "get",
			count: bCount,
			title: "download object",
			getResult: func(cost float64) []string {
				line := []string{"download object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("get", float64(bSize>>20*bCount)/cost, cost/float64(bCount), 2, tty)
					line[1] += " MiB/s"
					line[2] += " s/file"
				}
				return line
			},
		}, {
			name:     "smallput",
			count:    bCount,
			title:    "put small object",
			startKey: bCount,
			getResult: func(cost float64) []string {
				line := []string{"put small file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("smallput", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:  "smallget",
			count: bCount,
			title: "get small file",
			after: func(blob object.ObjectStorage) {
				for i := bCount; i < bCount*2; i++ {
					_ = blob.Delete(strconv.Itoa(i))
				}
			},
			getResult: func(cost float64) []string {
				line := []string{"get small object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("smallget", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:  "list",
			title: "list operate",
			count: 100,
			getResult: func(cost float64) []string {
				line := []string{"list operate", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("list", float64(bCount)*100/cost, cost*10, 2, tty)
					line[1] += " files/s"
					line[2] += " ms/op"
				}
				return line
			},
		}, {
			name:  "head",
			count: bCount,
			title: "head object",
			getResult: func(cost float64) []string {
				line := []string{"head object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("head", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:  "chtimes",
			count: bCount,
			title: "chtimes file",
			getResult: func(cost float64) []string {
				line := []string{"chtimes file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chtimes", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:  "chmod",
			count: bCount,
			title: "chmod file",
			getResult: func(cost float64) []string {
				line := []string{"chmod file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chmod", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:  "chown",
			count: bCount,
			title: "chown file",
			getResult: func(cost float64) []string {
				line := []string{"chown file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chown", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:  "delete",
			count: bCount,
			title: "delete object",
			getResult: func(cost float64) []string {
				line := []string{"delete object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("delete", float64(bCount)/cost, cost*1000/float64(bCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		},
	}

	bm := &benchMarkObj{
		blob:         blob,
		progressBar:  progress,
		threads:      threads,
		content:      make([]byte, bSize),
		smallContent: make([]byte, smallBSize),
	}
	rand.Read(bm.content)
	rand.Read(bm.smallContent)

	for _, api := range apis {
		pResult = append(pResult, bm.run(api))
	}
	progress.Done()

	for i := bCount; i < bCount*2; i++ {
		_ = bm.delete(strconv.Itoa(i))
	}

	fmt.Printf("Benchmark finished! block-size: %d KiB, data-object-size: %d MiB, small-object-size: %d KiB, NumThreads: %d\n", ctx.Uint("block-size"), ctx.Uint("data-object-size"), ctx.Uint("small-object-size"), ctx.Uint("threads"))

	// adjust the print order
	pResult[1], pResult[2] = pResult[2], pResult[1]
	pResult[8], pResult[11] = pResult[11], pResult[8]
	printResult(pResult, tty)
	return nil

}

var resultRangeForObj = map[string][4]float64{
	"put":          {100, 150, 0.02, 0.04},
	"get":          {100, 150, 0.02, 0.04},
	"smallput":     {100, 150, 7, 14},
	"smallget":     {100, 150, 7, 14},
	"multi-upload": {100, 150, 0.02, 0.04},
	"list":         {20000, 30000, 5, 8},
	"head":         {20000, 30000, 0.02, 0.04},
	"delete":       {8000, 10000, 0.08, 0.1},
	"chmod":        {20000, 30000, 0.04, 0.08},
	"chown":        {20000, 30000, 0.04, 0.08},
	"chtimes":      {20000, 30000, 0.04, 0.08},
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
	count     int
	startKey  int
	getResult func(cost float64) []string
	after     func(blob object.ObjectStorage)
}

type benchMarkObj struct {
	progressBar           *utils.Progress
	blob                  object.ObjectStorage
	threads               int
	content, smallContent []byte
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
	case "smallget", "get":
		fn = bm.get
	case "smallput":
		fn = bm.smallPut
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
	count := api.count
	if api.count != 0 {
		count = api.count
	}
	bar := bm.progressBar.AddCountBar(api.title, int64(count))
	for i := api.startKey; i < api.startKey+count; i++ {
		pool <- struct{}{}
		wg.Add(1)
		key := i
		go func() {
			defer func() {
				<-pool
				wg.Done()
			}()
			if err := fn(strconv.Itoa(key)); err != nil {
				logger.Fatalf("%s test failed %s", api.name, err)
			}
			bar.Increment()
		}()
	}
	wg.Wait()
	bar.Done()
	if api.after != nil {
		api.after(bm.blob)
	}
	return api.getResult(time.Since(start).Seconds())
}

func (bm *benchMarkObj) put(key string) error {
	return bm.blob.Put(key, bytes.NewReader(bm.content))
}

func (bm *benchMarkObj) smallPut(key string) error {
	return bm.blob.Put(key, bytes.NewReader(bm.smallContent))
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

func functionalTesting(blob object.ObjectStorage, fsize int, result *[][]string, tty bool) {
	runCase := func(title string, fn func(blob object.ObjectStorage) error) {
		r := pass
		if err := fn(blob); err == utils.ENOTSUP {
			r = nspt
		} else if err != nil {
			r = fmt.Sprintf("failed: %s", err)
			if tty {
				r = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, RED, r, RESET_SEQ)
			}
			logger.Debug(err.Error())
		}

		category := "basic"
		if title == "list" || title == "multipartUpload" || strings.HasPrefix(title, "ch") {
			category = "sync"
		}

		if tty {
			title = fmt.Sprintf("%s%sm%s%s", COLOR_SEQ, DEFAULT, title, RESET_SEQ)
		}

		*result = append(*result, []string{category, title, r})
	}
	isFileSystem := true
	fi, ok := blob.(object.FileSystem)
	if ok {
		if err := fi.Chmod("not_exists_file", 0755); err == utils.ENOTSUP {
			isFileSystem = false
		}
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

	runCase("create bucket", func(blob object.ObjectStorage) error {
		created := true
		if err := blob.Put(key, bytes.NewReader(nil)); err != nil {
			created = false
		}
		defer blob.Delete(key) //nolint:errcheck

		if !created {
			if err := blob.Create(); err != nil {
				return fmt.Errorf("can't create bucket %s", err)
			}
		}
		if err := blob.Create(); err != nil {
			return fmt.Errorf("creating a bucket that already exists returns an error")
		}
		return nil
	})

	runCase("put", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		return nil
	})

	runCase("put big", func(blob object.ObjectStorage) error {
		fmt.Println("Testing upload a large object ...")
		buffL := 1 << 20
		buff := make([]byte, buffL)
		rand.Read(buff)
		count := int(math.Floor(float64(fsize) / float64(buffL)))
		content := make([]byte, fsize)
		for i := 0; i < count; i++ {
			copy(content[i*buffL:(i+1)*buffL], buff)
		}
		if err := blob.Put(key, bytes.NewReader(content)); err != nil {
			return fmt.Errorf("put big object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		return nil
	})

	runCase("put empty", func(blob object.ObjectStorage) error {
		// Copy empty objects
		defer blob.Delete("empty_test_file") //nolint:errcheck
		if err := blob.Put("empty_test_file", bytes.NewReader([]byte{})); err != nil {
			return fmt.Errorf("put empty object failed %s", err)
		}

		// Copy `/` suffixed object
		defer blob.Delete("slash_test_file/") //nolint:errcheck
		if err := blob.Put("slash_test_file/", bytes.NewReader([]byte{})); err != nil {
			return fmt.Errorf("put `/` suffixed object failed %s", err)
		}
		return nil
	})

	runCase("get", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		if s, err := get(blob, key, 0, -1); err != nil && s != string(br) {
			return fmt.Errorf("get object failed %s", err)
		}
		return nil
	})

	runCase("get non-exist", func(blob object.ObjectStorage) error {
		if _, err := blob.Get("not_exists_file", 0, -1); err == nil {
			return fmt.Errorf("get not exists object should failed %s", err)
		}
		return nil
	})

	runCase("random get", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
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

	runCase("head", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		if h, err := blob.Head(key); err != nil {
			return fmt.Errorf("failed to head object %s", err)
		} else {
			if h.Key() != key {
				return fmt.Errorf("expected get key is test but get %s", h.Key())
			}
		}
		return nil
	})

	runCase("delete", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		if err := blob.Delete(key); err != nil {
			return fmt.Errorf("delete failed %s", err)
		}
		if _, err := blob.Head(key); err == nil {
			return fmt.Errorf("expect err is not nil")
		}

		if err := blob.Delete(key); err != nil {
			return fmt.Errorf("delete not exists %v", err)
		}
		return nil
	})

	runCase("delete non-exist", func(blob object.ObjectStorage) error {
		if err := blob.Delete(key); err != nil {
			return fmt.Errorf("deleting a non-existent object returns an error %v", err)
		}
		return nil
	})

	runCase("list", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
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
				return fmt.Errorf("list failed: %s", err)
			}

			objs, err = listAll(blob, "", "test2", 1)
			if err != nil {
				return fmt.Errorf("list failed: %s", err)
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
				return fmt.Errorf("list failed %s", err2)
			}

			objs, err2 = listAll(blob, "", "test2", 1)
			if err2 != nil {
				return fmt.Errorf("list failed %s", err2)
			} else if len(objs) != 0 {
				return fmt.Errorf("list should not return anything, but got %d", len(objs))
			}
		}
		return nil
	})

	runCase("multipartUpload", func(blob object.ObjectStorage) error {
		key := "multi_test_file"
		if err := blob.CompleteUpload(key, "notExistsUploadId", []*object.Part{}); err != utils.ENOTSUP {
			defer blob.Delete(key) //nolint:errcheck
			if uploader, err := blob.CreateMultipartUpload(key); err == nil {
				partSize := uploader.MinPartSize
				uploadID := uploader.UploadID
				defer blob.AbortUpload(key, uploadID)

				part1, err := blob.UploadPart(key, uploadID, 1, make([]byte, partSize))
				if err != nil {
					return fmt.Errorf("uploadPart 1 failed %s", err)
				}
				part2Size := 1 << 20
				_, err = blob.UploadPart(key, uploadID, 2, make([]byte, part2Size))
				if err != nil {
					return fmt.Errorf("uploadPart 2 failed %s", err)
				}
				part2Size = 2 << 20
				part2, err := blob.UploadPart(key, uploadID, 2, make([]byte, part2Size))
				if err != nil {
					return fmt.Errorf("uploadPart 2 failed %s", err)
				}

				if err := blob.CompleteUpload(key, uploadID, []*object.Part{part1, part2}); err != nil {
					return fmt.Errorf("completeMultipart failed %s", err)
				}
				if in, err := blob.Get(key, 0, -1); err != nil {
					return fmt.Errorf("failed to download file,key=%s", key)
				} else if d, err := ioutil.ReadAll(in); err != nil {
					return fmt.Errorf("failed to read downloaded content key=%s", key)
				} else if len(d) != partSize+part2Size {
					return fmt.Errorf("size of %s file: %d != %d", key, len(d), partSize+part2Size)
				}
				return nil
			}
		}
		return utils.ENOTSUP
	})

	runCase("chown", func(blob object.ObjectStorage) error {
		if !isFileSystem {
			return utils.ENOTSUP
		}
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		if err := fi.Chown(key, "", ""); err != nil {
			return fmt.Errorf("failed to chown object %s", err)
		}
		return nil
	})

	runCase("chmod", func(blob object.ObjectStorage) error {
		if !isFileSystem {
			return utils.ENOTSUP
		}
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		if err := fi.Chmod(key, 0777); err != nil {
			return fmt.Errorf("failed to change file permissions %s", err)
		}
		return nil
	})

	runCase("chtimes", func(blob object.ObjectStorage) error {
		if !isFileSystem {
			return utils.ENOTSUP
		}
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck

		mtime := time.Now().Add(-10 * time.Minute)
		if err := fi.Chtimes(key, mtime); err != nil {
			return fmt.Errorf("failed to chtimes %s", err)
		}

		if objInfo, err := blob.Head(key); err != nil {
			return fmt.Errorf("failed to head object %s", err)
		} else {
			if objInfo.Mtime().Before(mtime.Add(-2*time.Second)) || objInfo.Mtime().After(mtime.Add(2*time.Second)) {
				return fmt.Errorf("mtime deviation is too large, the actual mtime is %s but got %s", mtime.Format(time.RFC3339), objInfo.Mtime().Format(time.RFC3339))
			}
		}
		return nil
	})
}
