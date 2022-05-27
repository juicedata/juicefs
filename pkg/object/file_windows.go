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

package object

import (
	"errors"
	"io/fs"
	"os"

	"github.com/juicedata/juicefs/pkg/utils"
)

func getOwnerGroup(info os.FileInfo) (string, string) {
	return "", ""
}

func lookupUser(name string) int {
	return 0
}

func lookupGroup(name string) int {
	return 0
}

func (d *filestore) Head(key string) (Object, error) {
	p := d.path(key)

	fi, err := os.Stat(p)
	if err != nil {
		// todo: check not exists error value
		if e, ok := err.(*fs.PathError); ok && errors.Is(e.Err, windows.ERROR_FILE_NOT_FOUND) {
			err = utils.ENOTEXISTS
		}
		return nil, err
	}
	size := fi.Size()
	if fi.IsDir() {
		size = 0
	}
	return &obj{
		key,
		size,
		fi.ModTime(),
		fi.IsDir(),
	}, nil
}
