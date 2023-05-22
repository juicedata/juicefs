package cmd

import (
	"bytes"
	"math/rand"
	"strconv"
	"sync"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdRecover() *cli.Command {
	return &cli.Command{
		Name:      "recover",
		Action:    recover,
		Category:  "ADMIN",
		Usage:     "recover files from trash",
		ArgsUsage: "META HOUR ...",
		Description: `
Rebuild the tree structure for trash files, and move them back to original directories.

Examples:
$ juicefs recover redis://localhost/1 2023-10-10-01`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "move-to-original",
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

func recover(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	for i := 0; i+1 < ctx.NArg(); i++ {
		path := ctx.Args().Get(i + 1)
		err := doRecover(m, path, ctx.Bool("move-to-original"), ctx.Int("threads"))
		if err != 0 {
			logger.Errorf("recover %s: %s", path, err)
		}
	}
	return nil
}

func doRecover(m meta.Meta, path string, move bool, threads int) syscall.Errno {
	logger.Infof("recover files in %s", path)
	ctx := meta.Background
	var parent meta.Ino
	var attr meta.Attr
	err := m.Lookup(ctx, meta.TrashInode, path, &parent, &attr, false)
	if err != 0 {
		return err
	}
	var entries []*meta.Entry
	err = m.Readdir(meta.Background, parent, 0, &entries)
	if err != 0 {
		return err
	}
	entries = entries[2:]
	// to avoid conflict
	rand.Shuffle(len(entries), func(i, j int) {
		entries[i], entries[j] = entries[j], entries[i]
	})

	var parents = make(map[meta.Ino]bool)
	if !move {
		for _, e := range entries {
			if e.Attr.Typ == meta.TypeDirectory {
				parents[e.Inode] = true
			}
		}
	}

	todo := make(chan *meta.Entry, 1000)
	p := utils.NewProgress(false)
	recovered := p.AddCountBar("recovered", int64(len(entries)))
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
				if move || parents[meta.Ino(dst)] {
					err = m.Rename(ctx, parent, string(e.Name), meta.Ino(dst), string(ps[2]), 0, nil, nil)
					if err != 0 {
						logger.Warnf("recover %s: %s", string(e.Name), err)
						failed.Increment()
					} else {
						recovered.Increment()
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
	recovered.Done()
	p.Done()
	logger.Infof("recovered %d files in %s", recovered.Current(), path)
	return 0
}
