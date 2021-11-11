package integration

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func getDefaultBucketName() string {
	var defaultBucket string
	switch runtime.GOOS {
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("%v", err)
		}
		defaultBucket = path.Join(homeDir, ".juicefs", "local")
	case "windows":
		defaultBucket = path.Join("C:/jfs/local")
	default:
		defaultBucket = "/var/jfs"
	}
	return defaultBucket
}


func getObjectSize(s int) int {
	const min, max = 64, 16 << 10
	var bits uint
	for s > 1 {
		bits++
		s >>= 1
	}
	s = s << bits
	if s < min {
		s = min
	} else if s > max {
		s = max
	}
	return s
}


func createSimpleStorage(format *meta.Format) (object.ObjectStorage, error) {
	object.UserAgent = "JuiceFS"
	var blob object.ObjectStorage
	var err error
	blob, err = object.CreateStorage(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey)
	if err != nil {
		return nil, err
	}
	blob = object.WithPrefix(blob, format.Name+"/")
	return blob, nil
}


func formatSimpleMethod(args []string) error {
	if len(args) < 1 {
		log.Fatalf("Meta URL and name are required")
	}
	m := meta.NewClient(args[0], &meta.Config{Retries: 2})

	if len(args) < 2 {
		log.Fatalf("Please give it a name")
	}
	name := args[1]
	validName := regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,61}[a-z0-9]$`)
	if !validName.MatchString(name) {
		log.Fatalf("invalid name: %s, only alphabet, number and - are allowed, and the length should be 3 to 63 characters.", name)
	}

	format := meta.Format{
		Name:        name,
		UUID:        uuid.New().String(),
		Storage:     "file",
		Bucket:      getDefaultBucketName(),
		AccessKey:   "",
		SecretKey:   "",
		Shards:      0,
		BlockSize:   getObjectSize(4096),
	}

	if format.AccessKey == "" && os.Getenv("ACCESS_KEY") != "" {
		format.AccessKey = os.Getenv("ACCESS_KEY")
		os.Unsetenv("ACCESS_KEY")
	}
	if format.SecretKey == "" && os.Getenv("SECRET_KEY") != "" {
		format.SecretKey = os.Getenv("SECRET_KEY")
		os.Unsetenv("SECRET_KEY")
	}

	if format.Storage == "file" && !strings.HasSuffix(format.Bucket, "/") {
		format.Bucket += "/"
	}

	blob, err := createSimpleStorage(&format)
	if err != nil {
		log.Fatalf("object storage: %s", err)
	}
	log.Printf("Data use %s", blob)

	err = m.Init(format, true)
	if err != nil {
		log.Fatalf("format: %s", err)
	}
	format.RemoveSecret()
	log.Printf("Volume is formatted as %+v", format)
	return nil
}


func doSimpleUmount(mp string, force bool) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if force {
			cmd = exec.Command("diskutil", "umount", "force", mp)
		} else {
			cmd = exec.Command("diskutil", "umount", mp)
		}
	case "linux":
		if _, err := exec.LookPath("fusermount"); err == nil {
			if force {
				cmd = exec.Command("fusermount", "-uz", mp)
			} else {
				cmd = exec.Command("fusermount", "-u", mp)
			}
		} else {
			if force {
				cmd = exec.Command("umount", "-l", mp)
			} else {
				cmd = exec.Command("umount", mp)
			}
		}
	case "windows":
		if !force {
			_ = os.Mkdir(filepath.Join(mp, ".UMOUNTIT"), 0755)
			return nil
		} else {
			cmd = exec.Command("taskkill", "/IM", "juicefs.exe", "/F")
		}
	default:
		return fmt.Errorf("OS %s is not supported", runtime.GOOS)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Print(string(out))
	}
	return err
}



func getMountFlags(mount cli.ActionFunc) *cli.Command {
	cmd := &cli.Command{
		Name:      "mount",
		Usage:     "mount a volume",
		ArgsUsage: "META-URL MOUNTPOINT",
		Action:    mount,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "metrics",
				Value: "127.0.0.1:9567",
				Usage: "address to export metrics",
			},
			&cli.StringFlag{
				Name:  "consul",
				Value: "127.0.0.1:8500",
				Usage: "consul address to register",
			},
			&cli.BoolFlag{
				Name:  "no-usage-report",
				Usage: "do not send usage report",
			},
		},
	}
	cmd.Flags = append(cmd.Flags, otherMountFlags()...)
	cmd.Flags = append(cmd.Flags, getClientFlags()...)
	return cmd
}



func otherMountFlags() []cli.Flag {
	var defaultLogDir = "/var/log"
	switch runtime.GOOS {
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("%v", err)
			return nil
		}
		defaultLogDir = path.Join(homeDir, ".juicefs")
	}
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    "d",
			Aliases: []string{"background"},
			Usage:   "run in background",
		},
		&cli.BoolFlag{
			Name:  "no-syslog",
			Usage: "disable syslog",
		},
		&cli.StringFlag{
			Name:  "log",
			Value: path.Join(defaultLogDir, "juicefs.log"),
			Usage: "path of log file when running in background",
		},
		&cli.StringFlag{
			Name:  "o",
			Usage: "other FUSE options",
		},
		&cli.Float64Flag{
			Name:  "attr-cache",
			Value: 1.0,
			Usage: "attributes cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "entry-cache",
			Value: 1.0,
			Usage: "file entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "dir-entry-cache",
			Value: 1.0,
			Usage: "dir entry cache timeout in seconds",
		},
		&cli.BoolFlag{
			Name:  "enable-xattr",
			Usage: "enable extended attributes (xattr)",
		},
	}
}

func getClientFlags() []cli.Flag {
	var defaultCacheDir = "/var/jfsCache"
	switch runtime.GOOS {
	case "darwin":
		fallthrough
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("%v", err)
			return nil
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	}
	return []cli.Flag{
		&cli.IntFlag{
			Name:  "get-timeout",
			Value: 60,
			Usage: "the max number of seconds to download an object",
		},
		&cli.IntFlag{
			Name:  "put-timeout",
			Value: 60,
			Usage: "the max number of seconds to upload an object",
		},
		&cli.IntFlag{
			Name:  "io-retries",
			Value: 30,
			Usage: "number of retries after network failure",
		},
		&cli.IntFlag{
			Name:  "max-uploads",
			Value: 20,
			Usage: "number of connections to upload",
		},
		&cli.IntFlag{
			Name:  "max-deletes",
			Value: 2,
			Usage: "number of threads to delete objects",
		},
		&cli.IntFlag{
			Name:  "buffer-size",
			Value: 300,
			Usage: "total read/write buffering in MB",
		},
		&cli.Int64Flag{
			Name:  "upload-limit",
			Value: 0,
			Usage: "bandwidth limit for upload in Mbps",
		},
		&cli.Int64Flag{
			Name:  "download-limit",
			Value: 0,
			Usage: "bandwidth limit for download in Mbps",
		},

		&cli.IntFlag{
			Name:  "prefetch",
			Value: 1,
			Usage: "prefetch N blocks in parallel",
		},
		&cli.BoolFlag{
			Name:  "writeback",
			Usage: "upload objects in background",
		},
		&cli.DurationFlag{
			Name:  "upload-delay",
			Usage: "delayed duration for uploading objects (\"s\", \"m\", \"h\")",
		},
		&cli.StringFlag{
			Name:  "cache-dir",
			Value: defaultCacheDir,
			Usage: "directory paths of local cache, use colon to separate multiple paths",
		},
		&cli.IntFlag{
			Name:  "cache-size",
			Value: 1 << 10,
			Usage: "size of cached objects in MiB",
		},
		&cli.Float64Flag{
			Name:  "free-space-ratio",
			Value: 0.1,
			Usage: "min free space (ratio)",
		},
		&cli.BoolFlag{
			Name:  "cache-partial-only",
			Usage: "cache only random/small read",
		},

		&cli.BoolFlag{
			Name:  "read-only",
			Usage: "allow lookup/read operations only",
		},
		&cli.Float64Flag{
			Name:  "open-cache",
			Value: 0.0,
			Usage: "open files cache timeout in seconds (0 means disable this feature)",
		},
		&cli.StringFlag{
			Name:  "subdir",
			Usage: "mount a sub-directory as root",
		},
	}
}


func flagSet(name string, flags []cli.Flag) (*flag.FlagSet, error) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)

	for _, f := range flags {
		if err := f.Apply(set); err != nil {
			return nil, err
		}
	}
	set.SetOutput(ioutil.Discard)
	return set, nil
}


func checkMountpointInTenSeconds(mp string,ch chan int) {
	for i := 0; i < 20; i++ {
		time.Sleep(time.Millisecond * 500)
		st, err := os.Stat(mp)
		if err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == 1 {
				//0 is success
				if ch != nil {
					ch <- 0
				}
				log.Printf("\033[92mOK\033[0m, %s is ready ", mp)
				return
			}
		}
		os.Stdout.WriteString(".")
		os.Stdout.Sync()
	}
	//1 is failure
	if ch != nil {
		ch <- 1
	}
	os.Stdout.WriteString("\n")
	log.Printf("fail to mount after 10 seconds, please mount in foreground")
}


func setUp(metaUrl string,bucket string,mp string,flagMap map[string]string) int {

	ch := make(chan int)
	formatStr := metaUrl + " " + bucket
	formatArgs := strings.Split(formatStr," ")
	formatSimpleMethod(formatArgs)

	mountStr := metaUrl + " " + mp
	mountArgs := strings.Split(mountStr," ")
	go checkMountpointInTenSeconds(mountArgs[1],ch)
	go mountSimpleMethod(mountArgs,flagMap)

	chInt := <- ch
	return chInt
}



func TestMain(m *testing.M) {
	metaUrl := "sqlite3://tmpsql"
	volume := "pics"
	mp := "/tmp/jfs"
	var flagMap map[string]string = map[string]string{"enable-xattr":"true"}
	result := setUp(metaUrl,volume,mp,flagMap)
	if result != 0 {
		log.Fatalln("mount is not completed in ten seconds")
		return
	}
	code := m.Run()
	umountErr := doSimpleUmount(mp,true)
	if umountErr != nil {
		log.Fatalf("umount err: %s\n",umountErr)
	}
	os.Exit(code)
}


func TestIntegration(t *testing.T) {
	makeCmd := exec.Command("make")
	out,err := makeCmd.CombinedOutput()
	if err != nil {
		t.Logf("std out:\n%s\n", string(out))
		t.Fatalf("std err failed with %s\n", err)
	} else {
		t.Logf("std out:\n%s\n", string(out))
	}
}


func mountSimpleMethod(args []string,flagMap map[string]string) {

	if len(args) < 1 {
		log.Fatalf("Meta URL and mountpoint are required\n")
	}

	addr := args[0]
	if len(args) < 2 {
		log.Fatalf("MOUNTPOINT is required\n")
	}
	mp := args[1]

	//get mount flags
	cliCmd := getMountFlags(nil)
	fsPtr,err := flagSet("flags",cliCmd.Flags)
	if err != nil {
		log.Fatalf("flagset error:%s\n",err)
	}
	for key,value := range flagMap{
		setErr := fsPtr.Set(key,value)
		if setErr != nil {
			log.Fatalf("set flagmap error:%s\n",setErr)
		}
	}
	c := cli.NewContext(nil,fsPtr,nil)


	fi, err := os.Stat(mp)
	if !strings.Contains(mp, ":") && err != nil {
		if err := os.MkdirAll(mp, 0777); err != nil {
			if os.IsExist(err) {
				// a broken mount point, umount it
				if err = doSimpleUmount(mp, true); err != nil {
					log.Fatalf("umount %s: %s", mp, err)
				}
			} else {
				log.Fatalf("create %s: %s", mp, err)
			}
		}
	} else if err == nil && fi.Size() == 0 {
		// a broken mount point, umount it
		if err = doSimpleUmount(mp, true); err != nil {
			log.Fatalf("umount %s: %s", mp, err)
		}
	}

	var readOnly = c.Bool("read-only")
	for _, o := range strings.Split(c.String("o"), ",") {
		if o == "ro" {
			readOnly = true
		}
	}
	metaConf := &meta.Config{
		Retries:     10,
		Strict:      true,
		CaseInsensi: strings.HasSuffix(mp, ":") && runtime.GOOS == "windows",
		ReadOnly:    readOnly,
		OpenCache:   time.Duration(c.Float64("open-cache") * 1e9),
		MountPoint:  mp,
		Subdir:      c.String("subdir"),
		MaxDeletes:  c.Int("max-deletes"),
	}
	m := meta.NewClient(addr, metaConf)
	format, err := m.Load()
	if err != nil {
		log.Fatalf("load setting: %s", err)
	}

	if !c.Bool("writeback") && c.IsSet("upload-delay") {
		log.Println("delayed upload only work in writeback mode")
	}

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout:  time.Second * time.Duration(c.Int("get-timeout")),
		PutTimeout:  time.Second * time.Duration(c.Int("put-timeout")),
		MaxUpload:   c.Int("max-uploads"),
		Writeback:   c.Bool("writeback"),
		UploadDelay: c.Duration("upload-delay"),
		Prefetch:    c.Int("prefetch"),
		BufferSize:  c.Int("buffer-size") << 20,

		CacheDir:       c.String("cache-dir"),
		CacheSize:      int64(c.Int("cache-size")),
		FreeSpace:      float32(c.Float64("free-space-ratio")),
		CacheMode:      os.FileMode(0600),
		CacheFullBlock: !c.Bool("cache-partial-only"),
		AutoCreate:     true,
	}

	if chunkConf.CacheDir != "memory" {
		ds := utils.SplitDir(chunkConf.CacheDir)
		for i := range ds {
			ds[i] = filepath.Join(ds[i], format.UUID)
		}
		chunkConf.CacheDir = strings.Join(ds, string(os.PathListSeparator))
	}
	blob, err := createSimpleStorage(format)
	if err != nil {
		log.Fatalf("object storage: %s", err)
	}
	log.Printf("Data use %s", blob)
	blob = object.NewLimited(blob, c.Int64("upload-limit")*1e6/8, c.Int64("download-limit")*1e6/8)
	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	}))
	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	}))
	conf := &vfs.Config{
		Meta:       metaConf,
		Format:     format,
		Version:    "Juicefs",
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}
	vfs.Init(conf, m, store)

	go checkMountpointInTenSeconds(mp,nil)

	err = m.NewSession()
	if err != nil {
		log.Fatalf("new session: %s", err)
	}

	mount_main(conf, m, store, c)
	closeErr := m.CloseSession()
	if closeErr != nil {
		log.Fatalf("close session err: %s\n", closeErr)
	}
}


func mount_main(conf *vfs.Config, m meta.Meta, store chunk.ChunkStore, c *cli.Context) {
	if os.Getuid() == 0 && os.Getpid() != 1 {
		disableUpdatedb()
	}

	conf.AttrTimeout = time.Millisecond * time.Duration(c.Float64("attr-cache")*1000)
	conf.EntryTimeout = time.Millisecond * time.Duration(c.Float64("entry-cache")*1000)
	conf.DirEntryTimeout = time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000)
	log.Printf("Mounting volume %s at %s ...", conf.Format.Name, conf.Mountpoint)
	err := fuse.Serve(conf, c.String("o"), c.Bool("enable-xattr"))
	if err != nil {
		log.Fatalf("fuse: %s", err)
	}
}



func disableUpdatedb() {
	path := "/etc/updatedb.conf"
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return
	}
	fstype := "fuse.juicefs"
	if bytes.Contains(data, []byte(fstype)) {
		return
	}
	// assume that fuse.sshfs is already in PRUNEFS
	knownFS := "fuse.sshfs"
	p1 := bytes.Index(data, []byte("PRUNEFS"))
	p2 := bytes.Index(data, []byte(knownFS))
	if p1 > 0 && p2 > p1 {
		var nd []byte
		nd = append(nd, data[:p2]...)
		nd = append(nd, fstype...)
		nd = append(nd, ' ')
		nd = append(nd, data[p2:]...)
		err = ioutil.WriteFile(path, nd, 0644)
		if err != nil {
			log.Printf("update %s: %s", path, err)
		} else {
			log.Printf("Add %s into PRUNEFS of %s", fstype, path)
		}
	}
}