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

package juicefs

import (
	"io"
	"io/fs"
	"os"
	"syscall"
)

// isOK also accepts syscall.Errno(0), which pkg/fs returns on success.
func isOK(err error) bool {
	return err == nil || err == syscall.Errno(0)
}

// pathError wraps err as os does, except io.EOF.
func pathError(op, name string, err error) error {
	if isOK(err) {
		return nil
	}
	if err == io.EOF {
		return err
	}
	return &fs.PathError{Op: op, Path: name, Err: err}
}

func linkError(op, oldname, newname string, err error) error {
	if isOK(err) {
		return nil
	}
	return &os.LinkError{Op: op, Old: oldname, New: newname, Err: err}
}
