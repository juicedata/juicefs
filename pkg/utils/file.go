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

package utils

import (
	"fmt"
	"path/filepath"
)

func GetInode(path string) (uint64, error) {
	dpath, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("abs of %s error: %w", path, err)
	}
	inode, err := GetFileInode(dpath)
	if err != nil {
		return 0, fmt.Errorf("lookup inode for %s error: %w", path, err)
	}

	return inode, nil
}
