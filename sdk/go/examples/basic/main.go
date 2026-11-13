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

// Basic writes, reads and lists files in /go-sdk-demo/basic of a volume.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"

	juicefs "github.com/juicedata/juicefs/sdk/go"
)

const dir = "/go-sdk-demo/basic"

func main() {
	name, metaURL := os.Getenv("JFS_VOLUME"), os.Getenv("JFS_META_URL")
	if name == "" || metaURL == "" {
		log.Fatal("set JFS_VOLUME and JFS_META_URL")
	}
	c, err := juicefs.New(name, metaURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	err = run(c)
	// Close flushes the data and ends the session.
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Fatal(err)
	}
}

func run(c *juicefs.Client) error {
	if err := c.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := c.WriteFile(dir+"/hello.txt", []byte("hello, JuiceFS\n"), 0644); err != nil {
		return err
	}

	f, err := c.OpenFile(dir+"/hello.txt", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("from the Go SDK\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	f, err = c.Open(dir + "/hello.txt")
	if err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return err
	}
	fmt.Printf("content:\n%s", data)

	fi, err := c.Stat(dir + "/hello.txt")
	if err != nil {
		return err
	}
	st := fi.Sys().(*juicefs.Stat)
	fmt.Printf("stat: size=%d mode=%v mtime=%s inode=%d uid=%d gid=%d\n",
		fi.Size(), fi.Mode(), fi.ModTime().Format("2006-01-02 15:04:05"), st.Inode, st.Uid, st.Gid)

	if err := c.Rename(dir+"/hello.txt", dir+"/hello2.txt"); err != nil {
		return err
	}
	if _, err := c.Stat(dir + "/hello.txt"); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("hello.txt still exists: %v", err)
	}

	entries, err := c.ReadDir(dir)
	if err != nil {
		return err
	}
	fmt.Printf("entries of %s:\n", dir)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}
		fmt.Printf("  %v %8d %s\n", info.Mode(), info.Size(), e.Name())
	}
	return nil
}
