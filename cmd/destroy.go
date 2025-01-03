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

package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/juicedata/juicefs/pkg/meta"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdDestroy() *cli.Command {
	return &cli.Command{
		Name:      "destroy",
		Action:    destroy,
		Category:  "ADMIN",
		Usage:     "Destroy an existing volume",
		ArgsUsage: "META-URL UUID",
		Description: `
Destroy the target volume, removing all objects in the data storage and all entries in its metadata engine.

WARNING: BE CAREFUL! This operation cannot be undone.

Examples:
$ juicefs destroy redis://localhost e94d66a8-2339-4abd-b8d8-6812df737892

Details: https://juicefs.com/docs/community/administration/destroy`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "automatically answer 'yes' to all prompts and run non-interactively",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "skip sanity check and force destroy the volume",
			},
		},
	}
}

func printSessions(ss [][3]string) string {
	header := [3]string{"SID", "HostName", "MountPoint"}
	var max [3]int
	for i := 0; i < 3; i++ {
		max[i] = len(header[i])
	}
	for _, s := range ss {
		for i := 0; i < 3; i++ {
			if l := len(s[i]); l > max[i] {
				max[i] = l
			}
		}
	}

	var ret, b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteByte('+')
		b.WriteString(strings.Repeat("-", max[i]+2))
	}
	b.WriteString("+\n")
	divider := b.String()
	ret.WriteString(divider)

	b.Reset()
	for i := 0; i < 3; i++ {
		b.WriteString(" | ")
		b.WriteString(padding(header[i], max[i], ' '))
	}
	b.WriteString(" |\n")
	ret.WriteString(b.String()[1:])
	ret.WriteString(divider)

	for _, s := range ss {
		b.Reset()
		for i := 0; i < 3; i++ {
			b.WriteString(" | ")
			if spaces := max[i] - len(s[i]); spaces > 0 {
				b.WriteString(strings.Repeat(" ", spaces))
			}
			b.WriteString(s[i])
		}
		b.WriteString(" |\n")
		ret.WriteString(b.String()[1:])
	}
	ret.WriteString(divider)

	return ret.String()
}

func destroy(ctx *cli.Context) error {
	setup(ctx, 2)
	uri := ctx.Args().Get(0)
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}
	removePassword(uri)
	m := meta.NewClient(uri, meta.DefaultConf())

	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	if uuid := ctx.Args().Get(1); uuid != format.UUID {
		logger.Fatalf("UUID %s != expected %s", uuid, format.UUID)
	}
	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("create object storage: %s", err)
	}

	if !ctx.Bool("force") {
		m.CleanStaleSessions(meta.Background())
		sessions, err := m.ListSessions()
		if err != nil {
			logger.Fatalf("list sessions: %s", err)
		}
		if num := len(sessions); num > 0 {
			ss := make([][3]string, num)
			for i, s := range sessions {
				ss[i] = [3]string{strconv.FormatUint(s.Sid, 10), s.HostName, s.MountPoint}
			}
			logger.Fatalf("%d sessions are active, please disconnect them first:\n%s", num, printSessions(ss))
		}
		var totalSpace, availSpace, iused, iavail uint64
		_ = m.StatFS(meta.Background(), meta.RootInode, &totalSpace, &availSpace, &iused, &iavail)

		fmt.Printf(" volume name: %s\n", format.Name)
		fmt.Printf(" volume UUID: %s\n", format.UUID)
		fmt.Printf("data storage: %s\n", blob)
		fmt.Printf("  used bytes: %d\n", totalSpace-availSpace)
		fmt.Printf(" used inodes: %d\n", iused)
		warn("The target volume will be permanently destroyed, including:")
		warn("1. ALL objects in the data storage: %s", blob)
		warn("2. ALL entries in the metadata engine: %s", utils.RemovePassword(uri))
		if !ctx.Bool("yes") && !userConfirmed() {
			logger.Fatalln("Aborted.")
		}
	}

	objs, err := osync.ListAll(blob, "", "", "", true)
	if err != nil {
		logger.Fatalf("list all objects: %s", err)
	}
	progress := utils.NewProgress(false)
	spin := progress.AddCountSpinner("Deleted objects")
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
					spin.Increment()
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
			spin.Increment()
		} else {
			failed++
			logger.Warnf("delete %s: %s", dirs[i], err)
		}
	}
	progress.Done()
	if progress.Quiet {
		logger.Infof("Deleted %d objects", spin.Current())
	}
	if failed > 0 {
		logger.Errorf("%d objects are failed to delete, please do it manually.", failed)
	}

	if err = m.Reset(); err != nil {
		logger.Fatalf("reset meta: %s", err)
	}

	logger.Infof("The volume has been destroyed! You may need to delete cache directory manually.")
	return nil
}
