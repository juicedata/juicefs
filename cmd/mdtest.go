package cmd

import (
	"fmt"
	"math/rand"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/mattn/go-isatty"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/metric"
	"github.com/urfave/cli/v2"
)

var ctx = meta.Background

func createDir(jfs *fs.FileSystem, root string, d int, width int) error {
	if err := jfs.Mkdir(ctx, root, 0755); err != 0 {
		return fmt.Errorf("Mkdir %s: %s", root, err)
	}
	if d > 0 {
		for i := 0; i < width; i++ {
			dn := filepath.Join(root, fmt.Sprintf("mdtest_tree.%d", i))
			if err := createDir(jfs, dn, d-1, width); err != nil {
				return err
			}
		}
	}
	return nil
}

func createFile(jfs *fs.FileSystem, bar *utils.Bar, np int, root string, d int, width, files, bytes int) error {
	m := jfs.Meta()
	for i := 0; i < files; i++ {
		fn := filepath.Join(root, fmt.Sprintf("file.mdtest.%d.%d", np, i))
		f, err := jfs.Create(ctx, fn, 0644)
		if err != 0 {
			return fmt.Errorf("create %s: %s", fn, err)
		}
		if bytes > 0 {
			for indx := 0; indx*meta.ChunkSize < bytes; indx++ {
				var id uint64
				if st := m.NewSlice(ctx, &id); st != 0 {
					return fmt.Errorf("writechunk %s: %s", fn, st)
				}
				size := meta.ChunkSize
				if bytes < (indx+1)*meta.ChunkSize {
					size = bytes - indx*meta.ChunkSize
				}
				if st := m.Write(ctx, f.Inode(), uint32(indx), 0, meta.Slice{Id: id, Size: uint32(size), Len: uint32(size)}); st != 0 {
					return fmt.Errorf("writeend %s: %s", fn, st)
				}
			}
		}
		f.Close(ctx)
		bar.Increment()
	}
	if d > 0 {
		dirs := make([]int, width)
		for i := 0; i < width; i++ {
			dirs[i] = i
		}
		rand.Shuffle(width, func(i, j int) {
			dirs[i], dirs[j] = dirs[j], dirs[i]
		})
		for i := range dirs {
			dn := filepath.Join(root, fmt.Sprintf("mdtest_tree.%d", dirs[i]))
			if err := createFile(jfs, bar, np, dn, d-1, width, files, bytes); err != nil {
				return err
			}
		}
	}
	return nil
}

func runTest(jfs *fs.FileSystem, rootDir string, np, width, depth, files, bytes int) {
	dirs := 1
	w := width
	z := depth
	for z > 0 {
		dirs += w
		w = w * width
		z--
	}
	var total = dirs * np * files
	progress := utils.NewProgress(!isatty.IsTerminal(os.Stdout.Fd()), false)
	bar := progress.AddCountBar("create file", int64(total))
	logger.Infof("Create %d files in %d dirs", total, dirs)

	ctx = meta.NewContext(1, uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	start := time.Now()
	if err := jfs.Mkdir(ctx, rootDir, 0755); err != 0 {
		logger.Errorf("mkdir %s: %s", rootDir, err)
	}
	root := filepath.Join(rootDir, "test-dir.0-0")
	if err := jfs.Mkdir(ctx, root, 0755); err != 0 {
		logger.Fatalf("Mkdir %s: %s", root, err)
	}
	root = filepath.Join(root, "mdtest_tree.0")
	if err := createDir(jfs, root, depth, width); err != nil {
		logger.Fatalf("initialize: %s", err)
	}
	t1 := time.Since(start)
	logger.Infof("Created %d dirs in %s (%d dirs/s)", dirs, t1, int(float64(dirs)/t1.Seconds()))

	rand.Seed(time.Now().Unix())
	var g sync.WaitGroup
	for i := 0; i < np; i++ {
		g.Add(1)
		go func(np int) {
			if err := createFile(jfs, bar, np, root, depth, width, files, bytes); err != nil {
				logger.Errorf("Create: %s", err)
			}
			g.Done()
		}(i)
	}
	g.Wait()
	progress.Done()
	used := time.Since(start) - t1
	logger.Infof("Created %d files in %s (%d files/s)", total, used, int(float64(total)/used.Seconds()))
}

func cmdMdtest() *cli.Command {
	selfFlags := []cli.Flag{
		&cli.IntFlag{
			Name:  "threads",
			Value: 1,
			Usage: "number of threads",
		},
		&cli.IntFlag{
			Name:  "dirs",
			Value: 3,
			Usage: "number of subdir",
		},
		&cli.IntFlag{
			Name:  "depth",
			Value: 2,
			Usage: "levels of tree",
		},
		&cli.IntFlag{
			Name:  "files",
			Value: 10,
			Usage: "number of files",
		},
		&cli.IntFlag{
			Name:  "write",
			Value: 0,
			Usage: "number of bytes",
		},
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "path for JuiceFS access log",
		},
	}
	compoundFlags := [][]cli.Flag{
		clientFlags(),
		cacheFlags(0),
		shareInfoFlags(),
		selfFlags,
	}

	return &cli.Command{
		Name:      "mdtest",
		Action:    mdtest,
		Category:  "TOOL",
		Hidden:    true,
		Usage:     "run test on meta engines",
		ArgsUsage: "META-URL PATH",
		Description: `
Examples:
$ juicefs mdtest redis://localhost /test1`,
		Flags: expandFlags(compoundFlags),
	}
}

func initForMdtest(c *cli.Context, mp string, metaUrl string) *fs.FileSystem {
	metaConf := getMetaConf(c, mp, c.Bool("read-only"))
	m := meta.NewClient(metaUrl, metaConf)
	format, err := m.Load(true)
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	registerer, registry := wrapRegister(mp, format.Name)

	blob, err := NewReloadableStorage(format, func() (*meta.Format, error) {
		return getFormat(c, m)
	})
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	chunkConf := getChunkConf(c, format)
	store := chunk.NewCachedStore(blob, *chunkConf, registerer)
	registerMetaMsg(m, store, chunkConf)

	err = m.NewSession()
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}

	conf := getVfsConf(c, metaConf, format, chunkConf)
	conf.AccessLog = c.String("access-log")
	conf.AttrTimeout = time.Millisecond * time.Duration(c.Float64("attr-cache")*1000)
	conf.EntryTimeout = time.Millisecond * time.Duration(c.Float64("entry-cache")*1000)
	conf.DirEntryTimeout = time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000)

	metricsAddr := exposeMetrics(c, m, registerer, registry)
	if c.IsSet("consul") {
		metric.RegisterToConsul(c.String("consul"), metricsAddr, conf.Meta.MountPoint)
	}
	jfs, err := fs.NewFileSystem(conf, m, store)
	if err != nil {
		logger.Fatalf("initialize failed: %s", err)
	}
	jfs.InitMetrics(registerer)
	return jfs
}

func mdtest(c *cli.Context) error {
	setup(c, 2)
	metaUrl := c.Args().Get(0)
	rootDir := c.Args().Get(1)
	removePassword(metaUrl)
	jfs := initForMdtest(c, "mdtest", metaUrl)
	runTest(jfs, rootDir, c.Int("threads"), c.Int("dirs"), c.Int("depth"), c.Int("files"), c.Int("write"))
	return jfs.Meta().CloseSession()
}
