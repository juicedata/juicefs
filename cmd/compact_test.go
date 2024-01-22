/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func createTestFile(path string, size int, partCnt int) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()

	content := []byte(strings.Repeat("a", size/partCnt))
	for i := 0; i < partCnt; i++ {
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
	filePart int
}

func initForCompactTest(mountDir string, dirs map[string]testDir) {
	for _, d := range dirs {
		dirPath := filepath.Join(mountDir, d.path)

		err := os.MkdirAll(dirPath, 0755)
		if err != nil {
			panic(err)
		}

		for i := 0; i < d.fileCnt; i++ {
			if err := createTestFile(filepath.Join(dirPath, fmt.Sprintf("%d", i)), d.fileSize, d.filePart); err != nil {
				panic(err)
			}
		}
	}
}

func TestCompact(t *testing.T) {
	var bucket string
	mountTemp(t, &bucket, []string{"--trash-days=0"}, nil)
	defer umountTemp(t)

	dirs := map[string]testDir{
		"d1/d11": {
			path:     "d1/d11",
			fileCnt:  10,
			fileSize: 10,
			filePart: 2,
		},
		"d1": {
			path:     "d1",
			fileCnt:  20,
			fileSize: 10,
			filePart: 5,
		},
		"d2": {
			path:     "d2",
			fileCnt:  5,
			fileSize: 20,
			filePart: 4,
		},
	}
	initForCompactTest(testMountPoint, dirs)
	dataDir := filepath.Join(bucket, testVolume, "chunks")

	sumChunks := 0
	for _, d := range dirs {
		sumChunks += d.fileCnt * d.filePart
	}

	chunkCnt := getFileCount(dataDir)
	assert.Equal(t, sumChunks, chunkCnt)

	orderedDirs := []string{"d1/d11", "d1", "d2"}
	for _, path := range orderedDirs {
		d := dirs[path]

		err := Main([]string{"", "compact", filepath.Join(testMountPoint, d.path)})
		assert.Nil(t, err)

		chunkCnt = getFileCount(dataDir)
		sumChunks -= d.fileCnt * (d.filePart - 1)
		assert.Equal(t, sumChunks, chunkCnt)
	}
}
