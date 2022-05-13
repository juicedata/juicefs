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
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"

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
				Name:  "big-file-size",
				Value: 1024,
				Usage: "size of each big file in MiB",
			},
			&cli.UintFlag{
				Name:  "small-file-size",
				Value: 128,
				Usage: "size of each small file in KiB",
			},
			&cli.UintFlag{
				Name:  "small-file-count",
				Value: 1000,
				Usage: "number of small files per thread",
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
	bigFSize := int(ctx.Uint("big-file-size")) << 20
	smlFSize := int(ctx.Uint("small-file-size")) << 10
	threads := int(ctx.Uint("threads"))
	smlFCount := int(ctx.Uint("small-file-count"))
	tty := isatty.IsTerminal(os.Stdout.Fd())
	progress := utils.NewProgress(!tty, false)
	nspt := "not support"
	if tty {
		nspt = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, RED, "not support", RESET_SEQ)
	}
	var result [][3]string
	logger.Println("prepare data...")
	smlContent = make([][]byte, smlFCount)
	for i := 0; i < smlFCount; i++ {
		smlContent[i] = make([]byte, smlFSize)
		rand.Read(smlContent[i])
	}

	{
		buf := make([]byte, int64(bigFSize))
		rand.Read(buf)
		bar := progress.Progress.AddBar(1,
			mpb.PrependDecorators(
				decor.Name("put big object: ", decor.WCSyncWidth),
				decor.CountersNoUnit("%d / %d"),
			),
			mpb.AppendDecorators(
				decor.OnComplete(decor.Spinner(nil, decor.WCSyncSpace), "done"),
			),
		)
		now := time.Now()
		if err = blob.Put("big-object", bytes.NewReader(buf)); err != nil {
			logger.Fatalf("put big-object error: %v", err)
		}
		bar.Increment()
		cost := time.Since(now).Seconds()
		var bigPResult = [3]string{"put big object", nspt, nspt}
		bigPResult[1], bigPResult[2] = colorize("bigput", float64(bigFSize>>20)/cost, cost, 2, tty)
		bigPResult[1] += " MiB/s"
		bigPResult[2] += " s/file"
		result = append(result, bigPResult)
	}

	{
		bar := progress.AddIoSpeedBar("get big object", int64(bigFSize))
		r, err := blob.Get("big-object", 0, -1)
		if err != nil {
			logger.Fatalf("get big object error: %v", err)
		}
		proxyReader := bar.ProxyReader(r)
		defer proxyReader.Close()
		now := time.Now()
		if _, err = io.Copy(io.Discard, proxyReader); err != nil {
			logger.Fatalf("get big object error: %v", err)
		}
		cost := time.Since(now).Seconds()
		var bigPResult = [3]string{"get big object", nspt, nspt}
		bigPResult[1], bigPResult[2] = colorize("bigget", float64(bigFSize>>20)/cost, cost, 2, tty)
		bigPResult[1] += " MiB/s"
		bigPResult[2] += " s/file"
		result = append(result, bigPResult)
	}

	if err = blob.Delete("big-object"); err != nil {
		logger.Fatalf("delete big-object error: %v", err)
	}

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

		var uploadResult = [3]string{"multi-upload", nspt, nspt}
		fname := fmt.Sprintf("__multi_upload__test__%d__", time.Now().UnixNano())
		if err := blob.CompleteUpload("test", "fakeUploadId", []*object.Part{}); err != utils.ENOTSUP {
			total := 100
			partSize := 5 << 20 // 5M
			content := make([][]byte, total)
			for i := 0; i < total; i++ {
				content[i] = make([]byte, partSize)
				rand.Read(content[i])
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
			name:   "smallput",
			fcount: smlFCount,
			title:  "put small object",
			getResult: func(cost float64) [3]string {
				line := [3]string{"put small file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("smallput", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "smallget",
			fcount: smlFCount,
			title:  "get small file",
			getResult: func(cost float64) [3]string {
				line := [3]string{"get small object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("smallget", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "list",
			title:  "list object",
			fcount: 100,
			getResult: func(cost float64) [3]string {
				line := [3]string{"list object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("list", 100/cost, cost*10, 2, tty)
					line[1] += " op/s"
					line[2] += " ms/op"
				}
				return line
			},
		}, {
			name:   "head",
			fcount: smlFCount,
			title:  "head object",
			getResult: func(cost float64) [3]string {
				line := [3]string{"head object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("head", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "chtimes",
			fcount: smlFCount,
			title:  "chtimes file",
			getResult: func(cost float64) [3]string {
				line := [3]string{"chtimes file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chtimes", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "chmod",
			fcount: smlFCount,
			title:  "chmod file",
			getResult: func(cost float64) [3]string {
				line := [3]string{"chmod file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chmod", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "chown",
			fcount: smlFCount,
			title:  "chown file",
			getResult: func(cost float64) [3]string {
				line := [3]string{"chown file", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chown", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		}, {
			name:   "delete",
			fcount: smlFCount,
			title:  "delete file",
			getResult: func(cost float64) [3]string {
				line := [3]string{"delete object", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("delete", float64(smlFCount)/cost, cost*1000/float64(smlFCount), 1, tty)
					line[1] += " files/s"
					line[2] += " ms/file"
				}
				return line
			},
		},
	}

	smallObj := &benchMarkObj{
		blob:        blob,
		progressBar: progress,
		threads:     threads,
	}

	for _, api := range apis {
		result = append(result, smallObj.run(api))
	}
	progress.Done()
	fmt.Println("Benchmark finished!")
	fmt.Printf("BigFileSize: %d MiB, SmallFileSize: %d KiB, SmallFileCount: %d, NumThreads: %d\n", ctx.Uint("big-file-size"), ctx.Uint("small-file-size"), ctx.Uint("small-file-count"), ctx.Uint("threads"))
	result[7], result[10] = result[10], result[7]
	printResult(result, tty)
	return nil

}

var resultRangeForObj = map[string][4]float64{
	"bigput":       {20, 30, 25, 40},
	"bigget":       {40, 50, 20, 35},
	"multi-upload": {20, 40, 25, 40},
	"smallput":     {100, 150, 7, 14},
	"smallget":     {200, 250, 5, 10},
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
	getResult func(cost float64) [3]string
}

type benchMarkObj struct {
	progressBar *utils.Progress
	blob        object.ObjectStorage
	threads     int
}

func (bc *benchMarkObj) run(api apiInfo) [3]string {
	if api.name == "chown" || api.name == "chmod" || api.name == "chtimes" {
		if err := bc.chmod("not_exists"); err == utils.ENOTSUP {
			return api.getResult(-1)
		}
	}
	var fn func(key string) error
	switch api.name {
	case "smallput":
		fn = bc.put
	case "smallget":
		fn = bc.get
	case "delete":
		fn = bc.delete
	case "head":
		fn = bc.head
	case "list":
		fn = bc.list
	case "chown":
		fn = bc.chown
	case "chmod":
		fn = bc.chmod
	case "chtimes":
		fn = bc.chtimes
	}

	var wg sync.WaitGroup
	start := time.Now()
	pool := make(chan struct{}, bc.threads)
	count := api.fcount
	if api.fcount != 0 {
		count = api.fcount
	}
	bar := bc.progressBar.AddCountBar(api.title, int64(count))
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

var smlContent [][]byte

func (bc *benchMarkObj) put(key string) error {
	idx, _ := strconv.Atoi(key)
	return bc.blob.Put(key, bytes.NewReader(smlContent[idx]))
}

func (bc *benchMarkObj) get(key string) error {
	r, err := bc.blob.Get(key, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(io.Discard, r)
	return err
}

func (bc *benchMarkObj) delete(key string) error {
	return bc.blob.Delete(key)
}

func (bc *benchMarkObj) head(key string) error {
	_, err := bc.blob.Head(key)
	return err
}

func (bc *benchMarkObj) list(key string) error {
	_, err := osync.ListAll(bc.blob, "", "")
	return err
}

func (bc *benchMarkObj) chown(key string) error {
	return bc.blob.(object.FileSystem).Chown(key, "", "")
}

func (bc *benchMarkObj) chmod(key string) error {
	return bc.blob.(object.FileSystem).Chmod(key, 0755)
}

func (bc *benchMarkObj) chtimes(key string) error {
	return bc.blob.(object.FileSystem).Chtimes(key, time.Now())
}
