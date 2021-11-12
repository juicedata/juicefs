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

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
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
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,
		CacheSize: 1024,
		CacheDir:  "memory",
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
	store := chunk.NewCachedStore(blob, chunkConf)

	conf := &vfs.Config{
		Meta:       metaConf,
		Format:     format,
		Version:    "Juicefs",
		Mountpoint: mp,
		Chunk:      &chunkConf,
	}
	vfs.Init(conf, m, store)

	go checkMountpointInTenSeconds(mp, nil)

	err = m.NewSession()
	if err != nil {
		log.Fatalf("new session: %s", err)
	}

	conf.AttrTimeout = time.Second
	conf.EntryTimeout = time.Second
	conf.DirEntryTimeout = time.Second
	serverErr := fuse.Serve(conf, "", true)
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
