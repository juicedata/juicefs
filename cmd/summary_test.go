/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/stretchr/testify/assert"
)

func TestRenderSummaryTree(t *testing.T) {
	fileTree := &meta.TreeSummary{
		Type:     meta.TypeFile,
		Size:     4096,
		Files:    1,
		Duration: 1500 * time.Microsecond,
	}
	assert.Equal(t, [][]string{
		{"PATH", "SIZE", "DIRS", "FILES", "COST"},
		{"file.txt", "4.0 KiB", "0", "1", "1ms"},
	}, renderSummaryTree(fileTree, "/mnt/jfs/dir/file.txt", false))
	assert.Equal(t, [][]string{
		{"PATH", "SIZE", "DIRS", "FILES", "COST"},
		{"file.txt", "4096", "0", "1", "1"},
	}, renderSummaryTree(fileTree, "/mnt/jfs/dir/file.txt", true))
	assert.Empty(t, fileTree.Path)

	dirTree := &meta.TreeSummary{
		Type:  meta.TypeDirectory,
		Size:  8192,
		Dirs:  1,
		Files: 1,
		Children: []*meta.TreeSummary{
			{Path: "file.txt", Type: meta.TypeFile, Size: 4096, Files: 1},
		},
	}
	assert.Equal(t, [][]string{
		{"PATH", "SIZE", "DIRS", "FILES", "COST"},
		{"/", "8192", "1", "1", "0"},
		{"file.txt", "4096", "0", "1", "0"},
	}, renderSummaryTree(dirTree, "/mnt/jfs/dir", true))
}
