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

package cmd

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestFile(path string, size int) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()

	content := []byte(strings.Repeat("a", size/2))
	for i := 0; i < 2; i++ {
		if _, err = file.Write(content); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
	}
	return nil
}

type testDir struct {
	path     string
	fileCnt  int
	fileSize int
}

func initForCompactTest(mountDir string, dirs []testDir) {
	for _, d := range dirs {
		dirPath := filepath.Join(mountDir, d.path)

		err := os.MkdirAll(dirPath, 0755)
		if err != nil {
			panic(err)
		}

		for i := 0; i < d.fileCnt; i++ {
			if err := createTestFile(filepath.Join(dirPath, fmt.Sprintf("%d", i)), d.fileSize); err != nil {
				panic(err)
			}
		}
	}
}

func TestCompact(t *testing.T) {
	var bucket string
	mountTemp(t, &bucket, []string{"--trash-days=0"}, nil)
	defer umountTemp(t)

	dirs := []testDir{
		{
			path:     "d1/d11",
			fileCnt:  10,
			fileSize: 10,
		},
		{
			path:     "d1",
			fileCnt:  20,
			fileSize: 10,
		},
		{
			path:     "d2",
			fileCnt:  5,
			fileSize: 10,
		},
	}
	initForCompactTest(testMountPoint, dirs)
	dataDir := filepath.Join(bucket, testVolume, "chunks")

	beforeCompactFileNum := getFileCount(dataDir)
	// file
	err := Main([]string{"", "compact", fmt.Sprintf("--path=%s", filepath.Join(testMountPoint, "d1", "1")), testMeta})
	assert.Nil(t, err)

	// dir
	for _, d := range dirs {
		err := Main([]string{"", "compact", fmt.Sprintf("--path=%s", filepath.Join(testMountPoint, d.path)), testMeta})
		assert.Nil(t, err)
	}

	afterCompactFileNum := getFileCount(dataDir)
	if beforeCompactFileNum <= afterCompactFileNum {
		t.Fatalf("blocks before gc compact %d <= after %d", beforeCompactFileNum, afterCompactFileNum)
	}
}
