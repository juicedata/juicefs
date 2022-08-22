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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdObjbench() *cli.Command {
	return &cli.Command{
		Name:      "objbench",
		Action:    objbench,
		Category:  "TOOL",
		Usage:     "Run benchmarks on an object storage",
		ArgsUsage: "BUCKET",
		Description: `
Run basic benchmarks on the target object storage to test if it works as expected.

Examples:
# Run benchmarks on S3
$ ACCESS_KEY=myAccessKey SECRET_KEY=mySecretKey juicefs objbench --storage s3  https://mybucket.s3.us-east-2.amazonaws.com -p 6

Details: https://juicefs.com/docs/community/performance_evaluation_guide#juicefs-objbench`,
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
			&cli.StringFlag{
				Name:  "session-token",
				Usage: "session token for object storage",
			},
			&cli.UintFlag{
				Name:  "block-size",
				Value: 4096,
				Usage: "size of each IO block in KiB",
			},
			&cli.UintFlag{
				Name:  "big-object-size",
				Value: 1024,
				Usage: "size of each big object in MiB",
			},
			&cli.UintFlag{
				Name:  "small-object-size",
				Value: 128,
				Usage: "size of each small object in KiB",
			},
			&cli.UintFlag{
				Name:  "small-objects",
				Value: 100,
				Usage: "number of small object",
			},
			&cli.BoolFlag{
				Name:  "skip-functional-tests",
				Usage: "skip functional tests",
			},
			&cli.UintFlag{
				Name:    "threads",
				Aliases: []string{"p"},
				Value:   4,
				Usage:   "number of concurrent threads",
			},
		},
	}
}

var (
	nspt    = "not support"
	pass    = "pass"
	skipped = "skipped"
	failed  = "failed"
)

func objbench(ctx *cli.Context) error {
	setup(ctx, 1)
	for _, name := range []string{"big-object-size", "small-object-size", "block-size", "small-objects", "threads"} {
		if ctx.Uint(name) == 0 {
			logger.Fatalf("%s should not be set to zero", name)
		}
	}
	ak, sk, token := ctx.String("access-key"), ctx.String("secret-key"), ctx.String("session-token")
	if ak == "" {
		ak = os.Getenv("ACCESS_KEY")
	}
	if sk == "" {
		sk = os.Getenv("SECRET_KEY")
	}
	if token == "" {
		token = os.Getenv("SESSION_TOKEN")
	}
	blobOrigin, err := object.CreateStorage(strings.ToLower(ctx.String("storage")), ctx.Args().First(), ak, sk, token)
	if err != nil {
		logger.Fatalf("create storage failed: %v", err)
	}

	prefix := fmt.Sprintf("__juicefs_benchmark_%d__/", time.Now().UnixNano())
	blob := object.WithPrefix(blobOrigin, prefix)
	defer func() {
		_ = blobOrigin.Delete(prefix)
	}()
	bSize := int(ctx.Uint("block-size")) << 10
	fsize := int(ctx.Uint("big-object-size")) << 20
	smallBSize := int(ctx.Uint("small-object-size")) << 10
	bCount := int(math.Ceil(float64(fsize) / float64(bSize)))
	sCount := int(ctx.Uint("small-objects"))
	threads := int(ctx.Uint("threads"))
	colorful := utils.SupportANSIColor(os.Stdout.Fd())
	progress := utils.NewProgress(false, false)
	if colorful {
		nspt = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, YELLOW, nspt, RESET_SEQ)
		skipped = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, YELLOW, skipped, RESET_SEQ)
		pass = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, GREEN, pass, RESET_SEQ)
		failed = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, RED, failed, RESET_SEQ)
	}

	if !ctx.IsSet("skip-functional-tests") {
		var result [][]string
		result = append(result, []string{"CATEGORY", "TEST", "RESULT"})
		fmt.Println("Start Functional Testing ...")
		functionalTesting(blob, &result, colorful)
		printResult(result, -1, colorful)
		fmt.Println()
	}
	fmt.Println("Start Performance Testing ...")
	var pResult [][]string
	pResult = append(pResult, []string{"ITEM", "VALUE", "COST"})

	apis := []apiInfo{
		{
			name:  "smallput",
			count: sCount,
			title: "put small objects",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("smallput", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:  "smallget",
			count: sCount,
			title: "get small objects",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("smallget", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:     "put",
			count:    bCount,
			title:    "upload objects",
			startKey: sCount,
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("put", float64(bSize>>20*bCount)/cost, cost*1000/float64(bCount), 2, colorful)
					line[1] += " MiB/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:     "get",
			count:    bCount,
			title:    "download objects",
			startKey: sCount,
			after: func(blob object.ObjectStorage) {
				for i := sCount; i < sCount+bCount; i++ {
					_ = blob.Delete(strconv.Itoa(i))
				}
			},
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("get", float64(bSize>>20*bCount)/cost, cost*1000/float64(bCount), 2, colorful)
					line[1] += " MiB/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:  "list",
			title: "list objects",
			count: 100,
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("list", float64(sCount)*100/cost, cost*10, 2, colorful)
					line[1] += " objects/s"
					line[2] += " ms/op"
				}
				return line
			},
		}, {
			name:  "head",
			count: sCount,
			title: "head objects",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("head", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:  "chtimes",
			count: sCount,
			title: "update mtime",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chtimes", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:  "chmod",
			count: sCount,
			title: "change permissions",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chmod", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:  "chown",
			count: sCount,
			title: "change owner/group",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("chown", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		}, {
			name:  "delete",
			count: sCount,
			title: "delete objects",
			getResult: func(cost float64) []string {
				line := []string{"", nspt, nspt}
				if cost > 0 {
					line[1], line[2] = colorize("delete", float64(sCount)/cost, cost*1000/float64(sCount), 1, colorful)
					line[1] += " objects/s"
					line[2] += " ms/object"
				}
				return line
			},
		},
	}

	bm := &benchMarkObj{
		blob:        blob,
		progressBar: progress,
		threads:     threads,
		seed:        make([]byte, bSize),
		smallSeed:   make([]byte, smallBSize),
	}
	rand.Read(bm.seed)
	rand.Read(bm.smallSeed)

	for _, api := range apis {
		pResult = append(pResult, bm.run(api))
	}
	progress.Done()

	for i := bCount; i < bCount*2; i++ {
		_ = bm.delete(strconv.Itoa(i), 0)
	}

	fmt.Printf("Benchmark finished! block-size: %d KiB, big-object-size: %d MiB, small-object-size: %d KiB, small-objects: %d, NumThreads: %d\n",
		ctx.Uint("block-size"), ctx.Uint("big-object-size"), ctx.Uint("small-object-size"), sCount, threads)

	// adjust the print order
	pResult[1], pResult[3] = pResult[3], pResult[1]
	pResult[2], pResult[4] = pResult[4], pResult[2]
	pResult[7], pResult[10] = pResult[10], pResult[7]
	printResult(pResult, -1, colorful)
	return nil
}

var resultRangeForObj = map[string][4]float64{
	"put":          {100, 150, 50, 150},
	"get":          {100, 150, 50, 150},
	"smallput":     {10, 30, 30, 100},
	"smallget":     {10, 30, 30, 100},
	"multi-upload": {100, 150, 20, 50},
	"list":         {1000, 10000, 100, 200},
	"head":         {10, 30, 30, 100},
	"delete":       {10, 30, 30, 100},
	"chmod":        {10, 30, 30, 100},
	"chown":        {10, 30, 30, 100},
	"chtimes":      {10, 30, 30, 100},
}

func colorize(item string, value, cost float64, prec int, colorful bool) (string, string) {
	svalue := strconv.FormatFloat(value, 'f', prec, 64)
	scost := strconv.FormatFloat(cost, 'f', 2, 64)
	if colorful {
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
	progressBar     *utils.Progress
	blob            object.ObjectStorage
	threads         int
	seed, smallSeed []byte
}

func (bm *benchMarkObj) run(api apiInfo) []string {
	if api.name == "chown" || api.name == "chmod" || api.name == "chtimes" {
		if err := bm.chmod("not_exists", 0); err == utils.ENOTSUP {
			line := api.getResult(-1)
			line[0] = api.title
			return line
		}
		if api.name == "chown" && strings.HasPrefix(bm.blob.String(), "file://") && os.Getuid() != 0 {
			logger.Warnf("chown test should be run by root")
			return []string{api.title, skipped, skipped}
		}
	}
	var fn func(key string, startKey int) error
	switch api.name {
	case "put":
		fn = bm.put
	case "get":
		fn = bm.get
	case "smallput":
		fn = bm.smallPut
	case "smallget":
		fn = bm.smallGet
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
	pool := make(chan struct{}, bm.threads)
	count := api.count
	if api.count != 0 {
		count = api.count
	}
	bar := bm.progressBar.AddCountBar(api.title, int64(count))
	var err error
	start := time.Now()
	for i := api.startKey; i < api.startKey+count; i++ {
		pool <- struct{}{}
		wg.Add(1)
		go func(key int) {
			defer func() {
				<-pool
				wg.Done()
			}()
			if e := fn(strconv.Itoa(key), api.startKey); e != nil {
				err = e
			}
			bar.Increment()
		}(i)
	}
	wg.Wait()
	bar.Done()
	line := api.getResult(time.Since(start).Seconds())
	if api.after != nil {
		api.after(bm.blob)
	}
	if err != nil {
		logger.Errorf("%s test failed: %s", api.name, err)
		return []string{api.title, failed, failed}
	}
	line[0] = api.title
	return line
}

func getMockData(seed []byte, idx int) []byte {
	size := len(seed)
	if size == 0 {
		return nil
	}
	content := make([]byte, size)
	if idx == 0 {
		content = seed
	} else {
		i := idx % size
		copy(content[:size-i], seed[i:size])
		copy(content[size-i:size], seed[:i])
	}
	return content
}

func (bm *benchMarkObj) put(key string, startKey int) error {
	idx, _ := strconv.Atoi(key)
	return bm.blob.Put(key, bytes.NewReader(getMockData(bm.seed, idx-startKey)))
}

func (bm *benchMarkObj) smallPut(key string, startKey int) error {
	idx, _ := strconv.Atoi(key)
	return bm.blob.Put(key, bytes.NewReader(getMockData(bm.smallSeed, idx)))
}

func getAndCheckN(blob object.ObjectStorage, key string, seed []byte, getOrgIdx func(idx int) int) error {
	idx, _ := strconv.Atoi(key)
	r, err := blob.Get(key, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	orgIdx := getOrgIdx(idx)
	checkN := 10
	l := len(seed)
	if l < checkN {
		checkN = l
	}
	if len(content) != len(seed) || !bytes.Equal(content[:checkN], getMockData(seed, orgIdx)[:checkN]) {
		return fmt.Errorf("the downloaded content is incorrect")
	}
	return nil
}

func (bm *benchMarkObj) get(key string, startKey int) error {
	return getAndCheckN(bm.blob, key, bm.seed, func(idx int) int {
		return idx - startKey
	})
}

func (bm *benchMarkObj) smallGet(key string, startKey int) error {
	return getAndCheckN(bm.blob, key, bm.smallSeed, func(idx int) int {
		return idx
	})
}

func (bm *benchMarkObj) delete(key string, startKey int) error {
	return bm.blob.Delete(key)
}

func (bm *benchMarkObj) head(key string, startKey int) error {
	_, err := bm.blob.Head(key)
	return err
}

func (bm *benchMarkObj) list(key string, startKey int) error {
	result, err := osync.ListAll(bm.blob, "", "")
	for range result {
	}
	return err
}

func (bm *benchMarkObj) chown(key string, startKey int) error {
	return bm.blob.(object.FileSystem).Chown(key, "nobody", "nogroup")
}

func (bm *benchMarkObj) chmod(key string, startKey int) error {
	return bm.blob.(object.FileSystem).Chmod(key, 0755)
}

func (bm *benchMarkObj) chtimes(key string, startKey int) error {
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

var syncTests = map[string]bool{
	"put a big object":    true,
	"put an empty object": true,
	"multipart upload":    true,
}

func functionalTesting(blob object.ObjectStorage, result *[][]string, colorful bool) {
	runCase := func(title string, fn func(blob object.ObjectStorage) error) {
		r := pass
		if err := fn(blob); err == utils.ENOTSUP {
			r = nspt
		} else if err != nil {
			r = err.Error()
			if colorful {
				r = fmt.Sprintf("%s%dm%s%s", COLOR_SEQ, RED, r, RESET_SEQ)
			}
			logger.Debug(err.Error())
		}

		category := "basic"
		if syncTests[title] || strings.HasPrefix(title, "change") {
			category = "sync"
		}

		if colorful {
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

	funFSCase := func(name string, fn func() error) {
		runCase(name, func(blob object.ObjectStorage) error {
			if !isFileSystem {
				return utils.ENOTSUP
			}
			br := []byte("hello")
			if err := blob.Put(key, bytes.NewReader(br)); err != nil {
				return fmt.Errorf("put object failed: %s", err)
			}
			defer blob.Delete(key) //nolint:errcheck
			return fn()
		})
	}

	runCase("create a bucket", func(blob object.ObjectStorage) error {
		created := true
		if err := blob.Put(key, bytes.NewReader(nil)); err != nil {
			created = false
		}
		defer blob.Delete(key) //nolint:errcheck

		if !created {
			if err := blob.Create(); err != nil {
				return fmt.Errorf("can't create bucket: %s", err)
			}
		}
		if err := blob.Create(); err != nil {
			return fmt.Errorf("creating a bucket that already exists returns an error")
		}
		return nil
	})

	runCase("put an object", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		return nil
	})

	runCase("get an object", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		if s, err := get(blob, key, 0, -1); err != nil && s != string(br) {
			return fmt.Errorf("get object failed: %s", err)
		}
		return nil
	})

	runCase("get non-exist", func(blob object.ObjectStorage) error {
		if _, err := blob.Get("not_exists_file", 0, -1); err == nil {
			return fmt.Errorf("get not exists object should failed: %s", err)
		}
		return nil
	})

	runCase("get partial object", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		if d, e := get(blob, key, 0, -1); d != "hello" {
			return fmt.Errorf("expect hello, but got %v, error: %s", d, e)
		}
		if d, e := get(blob, key, 2, -1); d != "llo" {
			logger.Errorf("expect llo, but got %v, error: %s", d, e)
		}
		if d, e := get(blob, key, 2, 3); d != "llo" {
			return fmt.Errorf("expect llo, but got %v, error: %s", d, e)
		}
		if d, e := get(blob, key, 2, 2); d != "ll" {
			return fmt.Errorf("expect ll, but got %v, error: %s", d, e)
		}
		if d, e := get(blob, key, 4, 2); d != "o" {
			return fmt.Errorf("out-of-range get: 'o', but got %v, error: %s", len(d), e)
		}
		if d, e := get(blob, key, 6, 2); d != "" {
			return fmt.Errorf("out-of-range get: '', but got %v, error: %s", len(d), e)
		}
		return nil
	})

	runCase("head an object", func(blob object.ObjectStorage) error {
		br := []byte("hello")
		if err := blob.Put(key, bytes.NewReader(br)); err != nil {
			return fmt.Errorf("put object failed: %s", err)
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

	runCase("delete an object", func(blob object.ObjectStorage) error {
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

	runCase("list objects", func(blob object.ObjectStorage) error {
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
				if objs[1].Size() != 5 {
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
				if objs[0].Size() != 5 {
					return fmt.Errorf("size of first key shold be 5, but got %v", objs[0].Size())
				}
				now := time.Now()
				if objs[0].Mtime().Before(now.Add(-30*time.Second)) || objs[0].Mtime().After(now.Add(time.Second*30)) {
					return fmt.Errorf("mtime of key should be within 10 seconds, but got %s", objs[0].Mtime().Sub(now))
				}
			} else {
				return fmt.Errorf("list failed: %s", err2)
			}

			objs, err2 = listAll(blob, "", "test2", 1)
			if err2 != nil {
				return fmt.Errorf("list failed: %s", err2)
			} else if len(objs) != 0 {
				return fmt.Errorf("list should not return anything, but got %d", len(objs))
			}
		}
		keyTotal := 100
		var sortedKeys []string
		for i := 0; i < keyTotal; i++ {
			k := fmt.Sprintf("hashKey%d", i)
			sortedKeys = append(sortedKeys, k)
			if err := blob.Put(fmt.Sprintf("hashKey%d", i), bytes.NewReader(br)); err != nil {
				return fmt.Errorf("put object failed: %s", err.Error())
			}
		}
		sort.Strings(sortedKeys)
		defer func() {
			for i := 0; i < keyTotal; i++ {
				_ = blob.Delete(fmt.Sprintf("hashKey%d", i))
			}
		}()

		if objs, err := listAll(blob, "hashKey", "", int64(keyTotal)); err != nil {
			return fmt.Errorf("list failed: %s", err)
		} else {
			for i := 0; i < keyTotal; i++ {
				if objs[i].Key() != sortedKeys[i] {
					return fmt.Errorf("the result for list is incorrect")
				}
			}
		}
		return nil
	})

	runCase("put a big object", func(blob object.ObjectStorage) error {
		fsize := 256 << 20
		buffL := 4 << 20
		buff := make([]byte, buffL)
		rand.Read(buff)
		count := int(math.Floor(float64(fsize) / float64(buffL)))
		content := make([]byte, fsize)
		for i := 0; i < count; i++ {
			copy(content[i*buffL:(i+1)*buffL], buff)
		}
		if err := blob.Put(key, bytes.NewReader(content)); err != nil {
			return fmt.Errorf("put big object failed: %s", err)
		}
		defer blob.Delete(key) //nolint:errcheck
		return nil
	})

	runCase("put an empty object", func(blob object.ObjectStorage) error {
		// Copy empty objects
		defer blob.Delete("empty_test_file") //nolint:errcheck
		if err := blob.Put("empty_test_file", bytes.NewReader([]byte{})); err != nil {
			return fmt.Errorf("put empty object failed: %s", err)
		}

		// Copy `/` suffixed object
		defer blob.Delete("slash_test_file/") //nolint:errcheck
		if err := blob.Put("slash_test_file/", bytes.NewReader([]byte{})); err != nil {
			return fmt.Errorf("put `/` suffixed object failed: %s", err)
		}
		return nil
	})

	runCase("multipart upload", func(blob object.ObjectStorage) error {
		key := "multi_test_file"
		if err := blob.CompleteUpload(key, "notExistsUploadId", []*object.Part{}); err != utils.ENOTSUP {
			defer blob.Delete(key) //nolint:errcheck
			partSize := 5 << 20
			total := 10
			seed := make([]byte, partSize)
			rand.Read(seed)
			upload, err := blob.CreateMultipartUpload(key)
			if err != nil {
				return fmt.Errorf("create multipart upload failed: %s", err)
			}
			parts := make([]*object.Part, total)
			content := make([][]byte, total)
			for i := 0; i < total; i++ {
				content[i] = getMockData(seed, i)
			}
			pool := make(chan struct{}, 4)
			var wg sync.WaitGroup
			for i := 1; i <= total; i++ {
				pool <- struct{}{}
				wg.Add(1)
				num := i
				go func() {
					defer func() {
						<-pool
						wg.Done()
					}()
					parts[num-1], err = blob.UploadPart(key, upload.UploadID, num, content[num-1])
					if err != nil {
						logger.Fatalf("multipart upload error: %v", err)
					}
				}()
			}
			wg.Wait()
			// overwrite part
			if parts[total-1], err = blob.UploadPart(key, upload.UploadID, total, getMockData(seed, total)); err != nil {
				logger.Fatalf("multipart upload error: %v", err)
			}
			if err = blob.CompleteUpload(key, upload.UploadID, parts); err != nil {
				logger.Fatalf("failed to complete multipart upload: %v", err)
			}
			r, err := blob.Get(key, 0, -1)
			if err != nil {
				logger.Fatalf("failed to get multipart upload file: %v", err)
			}
			cnt, err := io.ReadAll(r)
			if err != nil {
				logger.Fatalf("failed to get multipart upload file: %v", err)
			}
			content[total-1] = getMockData(seed, total)
			if !bytes.Equal(cnt, bytes.Join(content, nil)) {
				logger.Fatal("the content of the multipart upload file is incorrect")
			}
			return nil
		}
		return utils.ENOTSUP
	})

	funFSCase("change owner/group", func() error {
		if strings.HasPrefix(blob.String(), "file://") && os.Getuid() != 0 {
			return errors.New("root required")
		}
		if err := fi.Chown(key, "nobody", "nogroup"); err != nil {
			return fmt.Errorf("failed to chown object %s", err)
		}
		if objInfo, err := blob.Head(key); err != nil {
			return fmt.Errorf("failed to head object %s", err)
		} else if info, ok := objInfo.(object.File); ok {
			if info.Owner() != "nobody" {
				return fmt.Errorf("expect owner nobody but got %s", info.Owner())
			}
			if info.Group() != "nogroup" {
				return fmt.Errorf("expect group nogroup but got %s", info.Group())
			}
		}
		return nil
	})

	funFSCase("change permission", func() error {
		if err := fi.Chmod(key, 0777); err != nil {
			return err
		}
		if objInfo, err := blob.Head(key); err != nil {
			return fmt.Errorf("failed to head object %s", err)
		} else if info, ok := objInfo.(object.File); ok {
			if info.Mode()&0xFFF != 0777 {
				return fmt.Errorf("expect mode %o but got %o", 0777, info.Mode())
			}
		}
		return nil
	})

	funFSCase("change mtime", func() error {
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
