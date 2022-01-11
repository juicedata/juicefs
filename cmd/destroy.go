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

package main

import (
	"fmt"
	"sort"
	"sync"

	"github.com/juicedata/juicefs/pkg/meta"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func destroy(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 2 {
		return fmt.Errorf("META-URL and UUID are required")
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})

	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	if uuid := ctx.Args().Get(1); uuid != format.UUID {
		logger.Fatalf("UUID %s != expected %s", uuid, format.UUID)
	}

	if !ctx.Bool("force") {
		m.CleanStaleSessions()
		sessions, err := m.ListSessions()
		if err != nil {
			logger.Fatalf("list sessions: %s", err)
		}
		if num := len(sessions); num > 0 {
			logger.Fatalf("%d sessions are active, please disconnect them first", num)
		}
		var totalSpace, availSpace, iused, iavail uint64
		_ = m.StatFS(meta.Background, &totalSpace, &availSpace, &iused, &iavail)

		fmt.Printf(" volume name: %s\n", format.Name)
		fmt.Printf(" volume UUID: %s\n", format.UUID)
		fmt.Printf("data storage: %s://%s\n", format.Storage, format.Bucket)
		fmt.Printf("  used bytes: %d\n", totalSpace-availSpace)
		fmt.Printf(" used inodes: %d\n", iused)
		warn("The target volume will be destoried permanently, including:")
		warn("1. objects in the data storage")
		warn("2. entries in the metadata engine")
		if !userConfirmed() {
			logger.Fatalln("Aborted.")
		}
	}

	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("create object storage: %s", err)
	}
	objs, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Fatalf("list all objects: %s", err)
	}
	progress, bar := utils.NewProgressCounter("deleting objects: ")
	var failed int
	var dirs []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for obj := range objs {
				if obj == nil {
					break // failed listing
				}
				if obj.IsDir() {
					mu.Lock()
					dirs = append(dirs, obj.Key())
					mu.Unlock()
					continue
				}
				if err := blob.Delete(obj.Key()); err == nil {
					bar.Increment()
				} else {
					failed++
					logger.Warnf("delete %s: %s", obj.Key(), err)
				}
			}
		}()
	}
	wg.Wait()
	sort.Strings(dirs)
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := blob.Delete(dirs[i]); err == nil {
			bar.Increment()
		} else {
			failed++
			logger.Warnf("delete %s: %s", dirs[i], err)
		}
	}
	bar.SetTotal(0, true)
	progress.Wait()
	if failed > 0 {
		fmt.Printf("%d objects are failed to delete, please do it manually.\n", failed)
	}

	if err = m.Reset(); err != nil {
		logger.Fatalf("reset meta: %s", err)
	}

	fmt.Println("The volume has been destroyed! You may need to delete cache directory manually.")
	return nil
}

func destroyFlags() *cli.Command {
	return &cli.Command{
		Name:      "destroy",
		Usage:     "destroy an existing volume",
		ArgsUsage: "META-URL UUID",
		Action:    destroy,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "force",
				Usage: "skip sanity check and force destroy the volume",
			},
		},
	}
}
