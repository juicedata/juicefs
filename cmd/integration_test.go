/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"flag"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/metric"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)


func execCmd(args []string) {

	cli.VersionFlag = &cli.BoolFlag{
		Name: "version", Aliases: []string{"V"},
		Usage: "print only the version",
	}
	app := &cli.App{
		Name:                 "juicefs",
		Usage:                "A POSIX file system built on Redis and object storage.",
		Version:              version.Version(),
		Copyright:            "AGPLv3",
		EnableBashCompletion: true,
		Flags:                globalFlags(),
		Commands: []*cli.Command{
			formatFlags(),
			mountFlags(),
			umountFlags(),
			gatewayFlags(),
			syncFlags(),
			rmrFlags(),
			infoFlags(),
			benchFlags(),
			gcFlags(),
			checkFlags(),
			profileFlags(),
			statsFlags(),
			statusFlags(),
			warmupFlags(),
			dumpFlags(),
			loadFlags(),
		},
	}

	// Called via mount or fstab.
	if strings.HasSuffix(args[0], "/mount.juicefs") {
		if newArgs, err := handleSysMountArgs(); err != nil {
			log.Fatal(err)
		} else {
			os.Args = newArgs
		}
	}

	err := app.Run(reorderOptions(app, args))
	if err != nil {
		log.Fatal(err)
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

func mountSimpleMethod(args []string,flagMap map[string]string) {

	if len(args) < 1 {
		logger.Fatalf("Meta URL and mountpoint are required\n")
	}

	addr := args[0]
	if len(args) < 2 {
		logger.Fatalf("MOUNTPOINT is required\n")
	}
	mp := args[1]

	//get mount flags
	cliCmd := mountFlags()
	fsPtr,err := flagSet("flags",cliCmd.Flags)
	if err != nil {
		logger.Fatalf("flagset error:%s\n",err)
	}
	for key,value := range flagMap{
		setErr := fsPtr.Set(key,value)
		if setErr != nil {
			logger.Fatalf("set flagmap error:%s\n",setErr)
		}
	}
	c := cli.NewContext(nil,fsPtr,nil)


	fi, err := os.Stat(mp)
	if !strings.Contains(mp, ":") && err != nil {
		if err := os.MkdirAll(mp, 0777); err != nil {
			if os.IsExist(err) {
				// a broken mount point, umount it
				if err = doUmount(mp, true); err != nil {
					logger.Fatalf("umount %s: %s", mp, err)
				}
			} else {
				logger.Fatalf("create %s: %s", mp, err)
			}
		}
	} else if err == nil && fi.Size() == 0 {
		// a broken mount point, umount it
		if err = doUmount(mp, true); err != nil {
			logger.Fatalf("umount %s: %s", mp, err)
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
		logger.Fatalf("load setting: %s", err)
	}



	if !c.Bool("writeback") && c.IsSet("upload-delay") {
		logger.Warnf("delayed upload only work in writeback mode")
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
	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
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
		Version:    version.Version(),
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}
	vfs.Init(conf, m, store)

	if c.Bool("background") && os.Getenv("JFS_FOREGROUND") == "" {
		if runtime.GOOS != "windows" {
			d := c.String("cache-dir")
			if d != "memory" && !strings.HasPrefix(d, "/") {
				ad, err := filepath.Abs(d)
				if err != nil {
					logger.Fatalf("cache-dir should be absolute path in daemon mode")
				} else {
					for i, a := range os.Args {
						if a == d || a == "--cache-dir="+d {
							os.Args[i] = a[:len(a)-len(d)] + ad
						}
					}
				}
			}
		}
		sqliteScheme := "sqlite3://"
		if strings.HasPrefix(addr, sqliteScheme) {
			path := addr[len(sqliteScheme):]
			path2, err := filepath.Abs(path)
			if err == nil && path2 != path {
				for i, a := range os.Args {
					if a == addr {
						os.Args[i] = sqliteScheme + path2
					}
				}
			}
		}
		// The default log to syslog is only in daemon mode.
		utils.InitLoggers(!c.Bool("no-syslog"))
		err := makeDaemon(c, conf.Format.Name, conf.Mountpoint)
		if err != nil {
			logger.Fatalf("Failed to make daemon: %s", err)
		}
	} else {
		go checkMountpoint(conf.Format.Name, mp)
	}

	err = m.NewSession()
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}
	installHandler(mp)
	metricsAddr := exposeMetrics(m, c)

	if c.IsSet("consul") {
		metric.RegisterToConsul(c.String("consul"), metricsAddr, mp)
	}

	mount_main(conf, m, store, c)
	closeErr := m.CloseSession()
	if closeErr != nil {
		logger.Fatalf("close session err: %s\n", closeErr)
	}
}


func checkMountpointInTenSeconds(mp string,ch chan int) {
	for i := 0; i < 20; i++ {
		time.Sleep(time.Millisecond * 500)
		st, err := os.Stat(mp)
		if err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Ino == 1 {
				//0 is success
				ch <- 0
				log.Printf("\033[92mOK\033[0m, %s is ready ", mp)
				return
			}
		}
		os.Stdout.WriteString(".")
		os.Stdout.Sync()
	}
	//1 is failure
	ch <- 1
	os.Stdout.WriteString("\n")
	logger.Printf("fail to mount after 10 seconds, please mount in foreground")
}



func setUp(metaUrl string,bucket string,mp string,flagMap map[string]string) int {

	ch := make(chan int)
	formatStr := "juicefs format" + " " + metaUrl + " " + bucket
	formatArgs := strings.Split(formatStr," ")
	execCmd(formatArgs)

	mountStr := metaUrl + " " + mp
	mountArgs := strings.Split(mountStr," ")
	go checkMountpointInTenSeconds(mountArgs[1],ch)
	go mountSimpleMethod(mountArgs,flagMap)

	chInt := <- ch
	return chInt
}



func TestMain(m *testing.M) {
	metaUrl := "sqlite3://abc"
	volume := "vol1"
	mp := "/tmp/jfs"
	var flagMap map[string]string = map[string]string{"enable-xattr":"true"}
	result := setUp(metaUrl,volume,mp,flagMap)
	if result != 0 {
		logger.Fatalln("mount is not completed in ten seconds")
		return
	}
	code := m.Run()
	umountErr := doUmount(mp,true)
	if umountErr != nil {
		logger.Fatalf("umount err: %s\n",umountErr)
	}
	os.Exit(code)
}


func TestIntegration(t *testing.T) {
	makeCmd := exec.Command("make","all","-C","../fstests","-f","Makefile_integration")
	out,err := makeCmd.CombinedOutput()
	if err != nil {
		t.Logf("std out:\n%s\n", string(out))
		t.Fatalf("std err failed with %s\n", err)
	} else {
		t.Logf("std out:\n%s\n", string(out))
	}
}