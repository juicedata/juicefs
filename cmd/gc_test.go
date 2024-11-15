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

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/stretchr/testify/require"
)

func writeSmallBlocks(mountDir string) error {
	file, err := os.OpenFile(
		filepath.Join(mountDir, "test.txt"),
		os.O_WRONLY|os.O_TRUNC|os.O_CREATE,
		0666,
	)
	if err != nil {
		return err
	}
	defer file.Close()

	content := []byte(strings.Repeat("aaaaaaaabbbbbbbb", 256))
	for k := 0; k < 64; k++ {
		if _, err = file.Write(content); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
	}

	return nil
}

func getFileCount(dir string) int {
	files, _ := os.ReadDir(dir)
	count := 0
	for _, f := range files {
		if f.IsDir() {
			count += getFileCount(filepath.Join(dir, f.Name()))
		} else {
			count++
		}
	}

	return count
}

func TestGc(t *testing.T) {
	var bucket string
	mountTemp(t, &bucket, []string{"--trash-days=0", "--hash-prefix"}, nil)
	defer umountTemp(t)

	if err := writeSmallBlocks(testMountPoint); err != nil {
		t.Fatalf("write small blocks failed: %s", err)
	}
	dataDir := filepath.Join(bucket, testVolume, "chunks")
	beforeCompactFileNum := getFileCount(dataDir)
	if err := Main([]string{"", "gc", "--compact", testMeta}); err != nil {
		t.Fatalf("gc compact failed: %s", err)
	}
	afterCompactFileNum := getFileCount(dataDir)
	if beforeCompactFileNum <= afterCompactFileNum {
		t.Fatalf("blocks before gc compact %d <= after %d", beforeCompactFileNum, afterCompactFileNum)
	}

	for i := 0; i < 10; i++ {
		filename := fmt.Sprintf("%s/f%d.txt", testMountPoint, i)
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("write file failed: %s", err)
		}
	}

	os.Setenv("JFS_GC_SKIPPEDTIME", "0")
	defer os.Unsetenv("JFS_GC_SKIPPEDTIME")
	t.Logf("JFS_GC_SKIPPEDTIME is %s", os.Getenv("JFS_GC_SKIPPEDTIME"))

	leaked := filepath.Join(dataDir, "0", "0", "123456789_0_1048576")
	os.WriteFile(leaked, []byte(strings.Repeat("aaaaaaaabbbbbbbb", 64*1024)), 0644)
	time.Sleep(time.Second * 3)

	if err := Main([]string{"", "gc", "--delete", testMeta}); err != nil {
		t.Fatalf("gc delete failed: %s", err)
	}

	require.False(t, utils.Exists(leaked))

	if err := Main([]string{"", "gc", testMeta}); err != nil {
		t.Fatalf("gc failed: %s", err)
	}
}
