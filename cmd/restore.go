package cmd

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdRestore() *cli.Command {
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
	setup0(ctx, 2, 0)
	if runtime.GOOS == "windows" && !utils.IsWinAdminOrElevatedPrivilege() {
		return fmt.Errorf("restore command requires Administrator or elevated privilege on Windows")
	}
	if os.Getuid() != 0 && runtime.GOOS != "windows" {
		return fmt.Errorf("only root can restore files from trash")
	}
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), nil)
	_, err := m.Load(true)
	if err != nil {
		return err
	}
	for i := 1; i < ctx.NArg(); i++ {
		hour := ctx.Args().Get(i)
		doRestore(m, hour, ctx.Bool("put-back"), ctx.Int("threads"))
	}
	return nil
}

func doRestore(m meta.Meta, hour string, putBack bool, threads int) {
	if err := m.NewSession(false); err != nil {
		logger.Warningf("running without sessions because fail to new session: %s", err)
	} else {
		defer func() {
			_ = m.CloseSession()
		}()
	}
	logger.Infof("restore files in %q ...", hour)
	ctx := meta.Background()
	var parent meta.Ino
	var attr meta.Attr
	err := m.Lookup(ctx, meta.TrashInode, hour, &parent, &attr, false)
	if err != 0 {
		logger.Errorf("lookup %q: %s", hour, err)
		return
	}
	var entries []*meta.Entry
	err = m.Readdir(ctx, parent, 0, &entries)
	if err != 0 {
		logger.Errorf("list %q: %s", hour, err)
		return
	}
	if len(entries) >= 2 {
		entries = entries[2:]
	}
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
	successLabel := "rebuilt"
	if putBack {
		successLabel = "restored"
	}
	done := p.AddCountBar(successLabel, int64(len(entries)))
	var skipped *utils.Bar
	if !putBack {
		skipped = p.AddCountSpinner("need-put-back")
	}
	conflict := p.AddCountSpinner("conflicts")
	failed := p.AddCountSpinner("failed")
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range todo {
				ps := bytes.SplitN(e.Name, []byte("-"), 3)
				if len(ps) != 3 {
					logger.Warnf("restore %s: invalid trash entry", string(e.Name))
					failed.Increment()
					continue
				}
				dst, parseErr := strconv.Atoi(string(ps[0]))
				if parseErr != nil {
					logger.Warnf("restore %s: %v", string(e.Name), parseErr)
					failed.Increment()
					continue
				}
				dstParent := meta.Ino(dst)
				dstName := string(ps[2])
				shouldRestore := putBack || parents[dstParent]
				if shouldRestore {
					st := m.Rename(ctx, parent, string(e.Name), dstParent, dstName, meta.RenameNoReplace|meta.RenameRestore, nil, nil)
					switch st {
					case 0:
						done.Increment()
					case syscall.EEXIST:
						logger.Warnf("restore %s: target already exists", string(e.Name))
						conflict.Increment()
					default:
						logger.Warnf("restore %s: %s", string(e.Name), st)
						failed.Increment()
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
	conflict.Done()
	if skipped != nil {
		skipped.Done()
	}
	done.Done()
	p.Done()
}
