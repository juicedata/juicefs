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

package object

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// safePath returns the cleaned root+key for file-system based storages, rejecting
// keys that lexically resolve outside of root. The boundary is root itself if it
// ends with "/", otherwise the parent directory of root. Paths are always
// slash-separated, an empty root means the (relative) root of the storage, and
// the suffix "/" of directories is preserved.
func safePath(root, key string) (string, error) {
	p := root + key
	if p == "" {
		return "", nil
	}
	boundary := root
	if !strings.HasSuffix(boundary, dirSuffix) {
		boundary = path.Dir(boundary)
	}
	cleaned := path.Clean(p)
	if !isWithin(path.Clean(boundary), cleaned) {
		return "", fmt.Errorf("object key %q escapes storage root %q", key, root)
	}
	if base := path.Base(p); strings.HasSuffix(p, dirSuffix) || base == "." || base == ".." {
		if !strings.HasSuffix(cleaned, dirSuffix) {
			cleaned += dirSuffix
		}
	}
	return cleaned, nil
}

// safeRawPath is like safePath but returns root+key unchanged, for storages
// that should see the original path (e.g. HDFS NameNode rejects unclean paths).
func safeRawPath(root, key string) (string, error) {
	if _, err := safePath(root, key); err != nil {
		return "", err
	}
	return root + key, nil
}

// backslashSeparated is implemented by storages that also treat '\' as a path
// separator, e.g. SMB and local file systems on Windows.
type backslashSeparated interface {
	backslashSeparated()
}

// safeBackslashPath is like safePath but also treats '\' as a path separator.
func safeBackslashPath(root, key string) (string, error) {
	return safePath(strings.ReplaceAll(root, `\`, "/"), strings.ReplaceAll(key, `\`, "/"))
}

// safeLocalPath is like safePath but for local paths, it follows the rules of
// the local OS (e.g. both '/' and '\' are separators and UNC paths on Windows)
// and returns the cleaned OS-native path.
func safeLocalPath(root, key string) (string, error) {
	var p string
	if strings.HasSuffix(root, dirSuffix) {
		p = filepath.Join(root, key)
	} else {
		p = filepath.Clean(root + key)
	}
	boundary := root
	if !strings.HasSuffix(boundary, dirSuffix) {
		boundary = filepath.Dir(boundary)
	}
	rel, err := filepath.Rel(boundary, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("object key %q escapes storage root %q", key, root)
	}
	return p, nil
}

func isWithin(boundary, p string) bool {
	switch boundary {
	case "/":
		return strings.HasPrefix(p, "/")
	case ".":
		return p != ".." && !strings.HasPrefix(p, "../")
	}
	return p == boundary || strings.HasPrefix(p, boundary+"/")
}
