package cmd

import (
	"bytes"
	"math/rand"
	"strconv"
	"sync"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdRecover() *cli.Command {
	return &cli.Command{
		Name:      "restore",
		Action:    restore,
		Category:  "ADMIN",
		Usage:     "restore files from trash",
		ArgsUsage: "META HOUR ...",
		Description: `
Rebuild the tree structure for trash files, and put them back to original directories.

Examples:
$ juicefs restore redis://localhost/1 2023-05-10-01`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "put-back",
				Usage: "move the recovered files into original directory",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number of threads",
			},
		},
	}
}

func restore(ctx *cli.Context) error {
	setup(ctx, 2)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	for i := 1; i < ctx.NArg(); i++ {
		hour := ctx.Args().Get(i)
		doRestore(m, hour, ctx.Bool("put-back"), ctx.Int("threads"))
	}
	return nil
}

func doRestore(m meta.Meta, hour string, putBack bool, threads int) {
	logger.Infof("restore files in %s ...", hour)
	ctx := meta.Background
	var parent meta.Ino
	var attr meta.Attr
	err := m.Lookup(ctx, meta.TrashInode, hour, &parent, &attr, false)
	if err != 0 {
		logger.Errorf("lookup %s: %s", hour, err)
		return
	}
	var entries []*meta.Entry
	err = m.Readdir(meta.Background, parent, 0, &entries)
	if err != 0 {
		logger.Errorf("list %s: %s", hour, err)
		return
	}
	entries = entries[2:]
	// to avoid conflict
	rand.Shuffle(len(entries), func(i, j int) {
		entries[i], entries[j] = entries[j], entries[i]
	})

	var parents = make(map[meta.Ino]bool)
	if !putBack {
		for _, e := range entries {
			if e.Attr.Typ == meta.TypeDirectory {
				parents[e.Inode] = true
			}
		}
	}

	todo := make(chan *meta.Entry, 1000)
	p := utils.NewProgress(false)
	restored := p.AddCountBar("restored", int64(len(entries)))
	skipped := p.AddCountSpinner("skipped")
	failed := p.AddCountSpinner("failed")
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range todo {
				ps := bytes.SplitN(e.Name, []byte("-"), 3)
				dst, _ := strconv.Atoi(string(ps[0]))
				if putBack || parents[meta.Ino(dst)] {
					err = m.Rename(ctx, parent, string(e.Name), meta.Ino(dst), string(ps[2]), 0, nil, nil)
					if err != 0 {
						logger.Warnf("restore %s: %s", string(e.Name), err)
						failed.Increment()
					} else {
						restored.Increment()
					}
				} else {
					skipped.Increment()
				}
			}
		}()
	}

	for _, e := range entries {
		todo <- e
	}
	close(todo)
	wg.Wait()
	failed.Done()
	skipped.Done()
	restored.Done()
	p.Done()
	logger.Infof("restored %d files in %s", restored.Current(), hour)
}
