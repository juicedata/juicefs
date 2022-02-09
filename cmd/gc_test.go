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
	"io/ioutil"
	"os"
	"path"
	"strings"
	"testing"
	"time"
)

func writeLeakedData(dataDir string) {
	var templateContent = "aaaaaaaabbbbbbbb"
	var writeContent strings.Builder
	for i := 0; i < 64*1024; i++ {
		writeContent.Write([]byte(templateContent))
	}
	ioutil.WriteFile(dataDir+"chunks/0/0/"+"123456789_0_1048576", []byte(writeContent.String()), 0644)
}

func checkLeakedData(dataDir string) bool {
	_, err := os.Stat(dataDir + "chunks/0/0/" + "123456789_0_1048576")
	if err != nil {
		return true
	}
	return false
}

func removeAllFiles(dataDir string) {
	_, err := os.Stat(dataDir)
	if err == nil {
		files, err := ioutil.ReadDir(dataDir)
		if err == nil {
			for _, f := range files {
				os.RemoveAll(path.Join([]string{dataDir, f.Name()}...))
			}
		}
	}
}

func writeSmallBlocks(mountDir string) error {
	file, err := os.OpenFile(
		mountDir+"/"+"test.txt",
		os.O_WRONLY|os.O_TRUNC|os.O_CREATE,
		0666,
	)
	if err != nil {
		return err
	}
	defer file.Close()
	var templateContent = "aaaaaaaabbbbbbbb"
	var writeContent strings.Builder
	for i := 0; i < 256; i++ {
		writeContent.Write([]byte(templateContent))
	}
	for k := 0; k < 64; k++ {
		_, err := file.Write([]byte(writeContent.String()))
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		if syncErr != nil {
			return syncErr
		}
	}

	return nil
}

func getFileCount(dir string) int {
	files, _ := ioutil.ReadDir(dir)
	count := 0
	for _, f := range files {
		if f.IsDir() {
			count += getFileCount(dir + "/" + f.Name())
		} else {
			count++
		}
	}

	return count
}

func TestGc(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	dataDir := "/tmp/testMountDir/test/"
	removeAllFiles(dataDir + "chunks/")
	defer ResetRedis(metaUrl)
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	defer func(mountpoint string) {
		err := UmountTmp(mountpoint)
		if err != nil {
			t.Fatalf("umount failed: %v", err)
		}
	}(mountpoint)

	err := writeSmallBlocks(mountpoint)
	if err != nil {
		t.Fatalf("write small blocks failed,err is %v", err)
	}
	beforeCompactFileNum := getFileCount(dataDir + "chunks/")
	gcArgs := []string{
		"",
		"gc",
		"--compact",
		metaUrl,
	}
	err = Main(gcArgs)
	if err != nil {
		t.Fatalf("gc failed: %v", err)
	}
	afterCompactFileNum := getFileCount(dataDir + "chunks/")
	t.Logf("beforeCompactFileNum is %d,afterCompactFileNum is %d", beforeCompactFileNum, afterCompactFileNum)
	if beforeCompactFileNum <= afterCompactFileNum {
		t.Fatalf("gc compact failed")
	}

	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("%s/f%d.txt", mountpoint, i)
		err := ioutil.WriteFile(filename, []byte("test"), 0644)
		if err != nil {
			t.Fatalf("mount failed: %v", err)
		}
	}

	strEnvSkippedTime := os.Getenv("JFS_GC_SKIPPEDTIME")
	t.Logf("JFS_GC_SKIPPEDTIME is %s", strEnvSkippedTime)

	writeLeakedData(dataDir)
	time.Sleep(time.Duration(3) * time.Second)

	gcArgs = []string{
		"",
		"gc",
		"--delete",
		metaUrl,
	}
	err = Main(gcArgs)
	if err != nil {
		t.Fatalf("gc failed: %v", err)
	}

	bNotExist := checkLeakedData(dataDir)
	if bNotExist == false {
		t.Fatalf("gc delete failed,leaked data was not deleted")
	}

	gcArgs = []string{
		"",
		"gc",
		metaUrl,
	}
	err = Main(gcArgs)
	if err != nil {
		t.Fatalf("gc failed: %v", err)
	}
}
