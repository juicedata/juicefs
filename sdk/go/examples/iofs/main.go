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

// Iofs walks a directory of a volume through the io/fs interfaces. The
// directory is the first argument, in the form of fs.ValidPath ("." is the
// root of the volume); it defaults to go-sdk-demo.
package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"

	juicefs "github.com/juicedata/juicefs/sdk/go"
)

func main() {
	name, metaURL := os.Getenv("JFS_VOLUME"), os.Getenv("JFS_META_URL")
	if name == "" || metaURL == "" {
		log.Fatal("set JFS_VOLUME and JFS_META_URL")
	}
	dir := "go-sdk-demo"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	c, err := juicefs.New(name, metaURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	err = run(c.FS(), dir)
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Fatal(err)
	}
}

// run only uses the io/fs interfaces, so it works with any fs.FS, such as
// os.DirFS.
func run(fsys fs.FS, dir string) error {
	var files, dirs int
	var size int64
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs++
		} else {
			files++
			size += info.Size()
		}
		fmt.Printf("%v %10d %s\n", info.Mode(), info.Size(), p)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("%d directories, %d files, %d bytes\n", dirs, files, size)

	matches, err := fs.Glob(fsys, path.Join(dir, "*", "*.txt"))
	if err != nil {
		return err
	}
	fmt.Printf("*/*.txt under %s: %q\n", dir, matches)
	return nil
}
