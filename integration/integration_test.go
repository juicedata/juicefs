/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package integration

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"
	"github.com/juicedata/juicefs/pkg/version"
	mcli "github.com/minio/cli"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/auth"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/vfs"
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

func formatSimpleMethod(url, name string) {
	m := meta.NewClient(url, &meta.Config{Retries: 2})
	format := meta.Format{
		Name:      name,
		UUID:      uuid.New().String(),
		Storage:   "file",
		Bucket:    getDefaultBucketName() + "/",
		AccessKey: "",
		SecretKey: "",
		Shards:    0,
		BlockSize: 4096,
	}
	err := m.Init(format, true)
	if err != nil {
		log.Fatalf("format: %s", err)
	}
	log.Printf("Volume is formatted as %+v", format)
}

func mountSimpleMethod(url, mp string) {

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

	metaConf := &meta.Config{
		Retries:    10,
		Strict:     true,
		MountPoint: mp,
	}
	m := meta.NewClient(url, metaConf)
	format, err := m.Load()
	if err != nil {
		log.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize:  format.BlockSize * 1024,
		Compress:   format.Compression,
		MaxUpload:  20,
		BufferSize: 300 << 20,
		CacheSize:  1024,
		CacheDir:   "memory",
	}

	blob, err := createSimpleStorage(format)
	if err != nil {
		log.Fatalf("object storage: %s", err)
	}
	log.Printf("Data use %s", blob)
	store := chunk.NewCachedStore(blob, chunkConf)

	m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	})

	conf := &vfs.Config{
		Meta:       metaConf,
		Format:     format,
		Version:    "Juicefs",
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}

	go checkMountpointInTenSeconds(mp, nil)

	err = m.NewSession()
	if err != nil {
		log.Fatalf("new session: %s", err)
	}

	conf.AttrTimeout = time.Second
	conf.EntryTimeout = time.Second
	conf.DirEntryTimeout = time.Second
	v := vfs.NewVFS(conf, m, store)
	serverErr := fuse.Serve(v, "", true)
	if serverErr != nil {
		log.Fatalf("fuse server err: %s\n", serverErr)
	}
	closeErr := m.CloseSession()
	if closeErr != nil {
		log.Fatalf("close session err: %s\n", closeErr)
	}
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

func checkMountpointInTenSeconds(mp string, ch chan int) {
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

func setUp(metaUrl, name, mp string) int {
	ch := make(chan int)
	formatSimpleMethod(metaUrl, name)
	go checkMountpointInTenSeconds(mp, ch)
	go mountSimpleMethod(metaUrl, mp)
	chInt := <-ch
	return chInt
}

func TestMain(m *testing.M) {
	metaUrl := "sqlite3://tmpsql"
	mp := "/tmp/jfs"
	result := setUp(metaUrl, "pics", mp)
	if result != 0 {
		log.Fatalln("mount is not completed in ten seconds")
		return
	}
	go func() {
		err := setUpGateway()
		if err != nil {
			log.Fatalf("set up gateway failed: %v", err)
		}
	}()

	code := m.Run()
	umountErr := doSimpleUmount(mp, true)
	if umountErr != nil {
		log.Fatalf("umount err: %s\n", umountErr)
	}
	os.Exit(code)
}

func TestIntegration(t *testing.T) {
	makeCmd := exec.Command("make")
	out, err := makeCmd.CombinedOutput()
	if err != nil {
		t.Logf("std out:\n%s\n", string(out))
		t.Fatalf("std err failed with %s\n", err)
	} else {
		t.Logf("std out:\n%s\n", string(out))
	}
}

var gw *GateWay
var metaUrl = "redis://localhost:6379/11"

func setUpGateway() error {
	formatSimpleMethod(metaUrl, "gateway-test")
	address := "0.0.0.0:9008"
	gw = &GateWay{}
	args := []string{"gateway", "--address", address, "--anonymous"}
	app := &mcli.App{
		Action: gateway2,
		Flags: []mcli.Flag{
			mcli.StringFlag{
				Name:  "address",
				Value: ":9000",
				Usage: "bind to a specific ADDRESS:PORT, ADDRESS can be an IP or hostname",
			},
			mcli.BoolFlag{
				Name:  "anonymous",
				Usage: "hide sensitive information from logging",
			},
			mcli.BoolFlag{
				Name:  "json",
				Usage: "output server logs and startup information in json format",
			},
			mcli.BoolFlag{
				Name:  "quiet",
				Usage: "disable MinIO startup information",
			},
		},
	}
	return app.Run(args)
}

func gateway2(ctx *mcli.Context) error {
	minio.StartGateway(ctx, gw)
	return nil
}

type GateWay struct{}

func (g *GateWay) Name() string {
	return "JuiceFS"
}

func (g *GateWay) Production() bool {
	return true
}

func (g *GateWay) NewGatewayLayer(creds auth.Credentials) (minio.ObjectLayer, error) {

	m := meta.NewClient(metaUrl, &meta.Config{
		Retries: 10,
		Strict:  true,
	})

	format, err := m.Load()
	if err != nil {
		log.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize:  format.BlockSize * 1024,
		Compress:   format.Compression,
		MaxUpload:  20,
		BufferSize: 300 << 20,
		CacheSize:  1024,
		CacheDir:   "memory",
	}

	blob, err := createSimpleStorage(format)
	if err != nil {
		log.Fatalf("object storage: %s", err)
	}

	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	})
	m.OnMsg(meta.CompactChunk, func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	})
	err = m.NewSession()
	if err != nil {
		log.Fatalf("new session: %s", err)
	}

	conf := &vfs.Config{
		Meta: &meta.Config{
			Retries: 10,
		},
		Format:          format,
		Version:         version.Version(),
		AttrTimeout:     time.Second,
		EntryTimeout:    time.Second,
		DirEntryTimeout: time.Second,
		Chunk:           &chunkConf,
	}
	return jfsgateway.NewJFSGateway(conf, m, store, true, true)
}
