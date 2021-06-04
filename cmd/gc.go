/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
)

func gcFlags() *cli.Command {
	return &cli.Command{
		Name:      "gc",
		Usage:     "collect any leaked objects",
		ArgsUsage: "REDIS-URL",
		Action:    gc,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "delete",
				Usage: "deleted leaked objects",
			},
			&cli.BoolFlag{
				Name:  "compact",
				Usage: "compact small slices into bigger ones",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number threads to delete leaked objects",
			},
		},
	}
}

type gcProgress struct {
	total       int
	found       int // valid slices
	leaked      int
	leakedBytes int64
}

func showProgress(p *gcProgress) {
	var lastDone []int
	var lastTime []time.Time
	for {
		if p.total == 0 {
			time.Sleep(time.Millisecond * 10)
			continue
		}
		var width int = 55
		a := width * p.found / (p.total + p.leaked)
		b := width * p.leaked / (p.total + p.leaked)
		var bar [80]byte
		for i := 0; i < width; i++ {
			if i < a {
				bar[i] = '='
			} else if i < a+b {
				bar[i] = '-'
			} else {
				bar[i] = ' '
			}
		}
		now := time.Now()
		lastDone = append(lastDone, p.found+p.leaked)
		lastTime = append(lastTime, now)
		for len(lastTime) > 18 { // 5 seconds
			lastDone = lastDone[1:]
			lastTime = lastTime[1:]
		}
		if len(lastTime) > 1 {
			n := len(lastTime) - 1
			d := lastTime[n].Sub(lastTime[0]).Seconds()
			fps := float64(lastDone[n]-lastDone[0]) / d
			fmt.Printf("[%s] % 8d % 2d%% % 4.0f/s \r", string(bar[:]), p.total+p.leaked, (p.found+p.leaked)*100/(p.total+p.leaked), fps)
		}
		time.Sleep(time.Millisecond * 300)
	}
}

func gc(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("REDIS-URL is needed")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout: time.Second * 60,
		PutTimeout: time.Second * 60,
		MaxUpload:  20,
		Prefetch:   0,
		BufferSize: 300,
		CacheDir:   "memory",
		CacheSize:  300,
	}

	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	}))
	if ctx.Bool("compact") {
		var nc, ns, nb int
		var lastLog time.Time
		m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
			slices := args[0].([]meta.Slice)
			chunkid := args[1].(uint64)
			err = vfs.Compact(chunkConf, store, slices, chunkid)
			nc++
			for _, s := range slices {
				ns++
				nb += int(s.Len)
			}
			if time.Since(lastLog) > time.Second && isatty.IsTerminal(os.Stdout.Fd()) {
				fmt.Printf("Compacted %d chunks (%d slices, %d bytes).\r", nc, ns, nb)
				lastLog = time.Now()
			}
			return err
		}))
		logger.Infof("start to compact chunks ...")
		err := m.CompactAll(meta.Background)
		if err != 0 {
			logger.Errorf("compact all chunks: %s", err)
		} else {
			logger.Infof("Compacted %d chunks (%d slices, %d bytes).", nc, ns, nb)
		}
	}

	blob = object.WithPrefix(blob, "chunks/")
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all blocks: %s", err)
	}

	var c = meta.NewContext(0, 0, []uint32{0})
	var slices []meta.Slice
	r := m.ListSlices(c, &slices, ctx.Bool("delete"))
	if r != 0 {
		logger.Fatalf("list all slices: %s", r)
	}
	keys := make(map[uint64]uint32)
	var totalBytes uint64
	for _, s := range slices {
		keys[s.Chunkid] = s.Size
		totalBytes += uint64(s.Size)
	}
	logger.Infof("using %d slices (%d bytes)", len(keys), totalBytes)

	var p = gcProgress{total: len(keys)}
	if isatty.IsTerminal(os.Stdout.Fd()) {
		go showProgress(&p)
	}

	var skipped, skippedBytes int64
	maxMtime := time.Now().Add(time.Hour * -1)

	var leakedObj = make(chan string, 10240)
	var wg sync.WaitGroup
	for i := 0; i < ctx.Int("threads"); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range leakedObj {
				if err := blob.Delete(key); err != nil {
					logger.Warnf("delete %s: %s", key, err)
				}
			}
		}()
	}

	foundLeaked := func(obj object.Object) {
		p.leakedBytes += obj.Size()
		p.leaked++
		if ctx.Bool("delete") {
			leakedObj <- obj.Key()
		}
	}
	for obj := range objs {
		if obj == nil {
			break // failed listing
		}
		if obj.IsDir() {
			continue
		}
		if obj.Mtime().After(maxMtime) || obj.Mtime().Unix() == 0 {
			logger.Debugf("ignore new block: %s %s", obj.Key(), obj.Mtime())
			skippedBytes += obj.Size()
			skipped++
			continue
		}

		logger.Debugf("found block %s", obj.Key())
		parts := strings.Split(obj.Key(), "/")
		if len(parts) != 3 {
			continue
		}
		name := parts[2]
		parts = strings.Split(name, "_")
		if len(parts) != 3 {
			continue
		}
		cid, _ := strconv.Atoi(parts[0])
		size := keys[uint64(cid)]
		if size == 0 {
			logger.Debugf("find leaked object: %s, size: %d", obj.Key(), obj.Size())
			foundLeaked(obj)
			continue
		}
		indx, _ := strconv.Atoi(parts[1])
		csize, _ := strconv.Atoi(parts[2])
		if csize == chunkConf.BlockSize {
			if (indx+1)*csize > int(size) {
				logger.Warnf("size of slice %d is larger than expected: %d > %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else if (indx+1)*csize == int(size) {
				p.found++
			}
		} else {
			if indx*chunkConf.BlockSize+csize != int(size) {
				logger.Warnf("size of slice %d is %d, but expect %d", cid, indx*chunkConf.BlockSize+csize, size)
				foundLeaked(obj)
			} else {
				p.found++
			}
		}
	}
	close(leakedObj)
	wg.Wait()

	if p.leaked > 0 {
		logger.Infof("found %d leaked objects (%d bytes), skipped %d (%d bytes)", p.leaked, p.leakedBytes, skipped, skippedBytes)
		if !ctx.Bool("delete") {
			logger.Infof("Please add `--delete` to clean them")
		}
	} else {
		logger.Infof("scan %d objects, no leaked found", p.found)
	}

	return nil
}
