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

// Admin uses the interfaces specific to JuiceFS on /go-sdk-demo/admin of a
// volume: extended attributes, usage, quota, cache warmup and status.
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	juicefs "github.com/juicedata/juicefs/sdk/go"
)

const dir = "/go-sdk-demo/admin"

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
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Fatal(err)
	}
}

func run(c *juicefs.Client) error {
	if err := c.MkdirAll(dir+"/sub", 0755); err != nil {
		return err
	}
	file := dir + "/data.txt"
	if err := c.WriteFile(file, []byte(strings.Repeat("JuiceFS\n", 1024)), 0644); err != nil {
		return err
	}
	if err := c.WriteFile(dir+"/sub/small.txt", []byte("small\n"), 0644); err != nil {
		return err
	}

	// Extended attributes.
	if err := c.Setxattr(file, "user.owner", []byte("go-sdk"), 0); err != nil {
		return err
	}
	value, err := c.Getxattr(file, "user.owner")
	if err != nil {
		return err
	}
	attrs, err := c.Listxattr(file)
	if err != nil {
		return err
	}
	fmt.Printf("xattr: user.owner=%q, all=%q\n", value, attrs)
	if err := c.Removexattr(file, "user.owner"); err != nil {
		return err
	}

	// Usage of the directory, as juicefs info and juicefs summary.
	info, err := c.Info(dir, &juicefs.InfoOptions{Recursive: true})
	if err != nil {
		return err
	}
	fmt.Printf("info: length=%d size=%d files=%d dirs=%d\n", info.Length, info.Size, info.Files, info.Dirs)
	tree, err := c.Summary(dir, 1, 10)
	if err != nil {
		return err
	}
	fmt.Printf("summary: %s size=%d\n", tree.Path, tree.Size)
	for _, child := range tree.Children {
		fmt.Printf("  %-12v %-16s size=%d files=%d dirs=%d\n", child.Type, child.Path, child.Size, child.Files, child.Dirs)
	}

	// Quota of the directory. Quotas are managed as root, without permission
	// checks.
	if err := c.SetQuota(dir, juicefs.QuotaOptions{Capacity: 1 << 30, Inodes: 1000}); err != nil {
		return err
	}
	q, err := c.GetQuota(dir)
	if err != nil {
		return err
	}
	fmt.Printf("quota: space %d/%d, inodes %d/%d\n", q.UsedSpace, q.MaxSpace, q.UsedInodes, q.MaxInodes)
	if err := c.DelQuota(dir); err != nil {
		return err
	}

	// Cache warmup, then a check of the cache.
	r, err := c.Warmup([]string{dir}, nil)
	if err != nil {
		return err
	}
	fmt.Printf("warmup: files=%d slices=%d bytes=%d\n", r.FileCount, r.SliceCount, r.TotalBytes)
	r, err = c.Warmup([]string{dir}, &juicefs.WarmupOptions{Check: true})
	if err != nil {
		return err
	}
	fmt.Printf("cache: missing=%d bytes, cached=%v\n", r.MissBytes, r.Locations)

	// Status of the volume and of this session.
	st, err := c.Status(false)
	if err != nil {
		return err
	}
	s := st.Setting
	fmt.Printf("volume: name=%s storage=%s bucket=%s block=%d trash-days=%d used=%d bytes, %d inodes\n",
		s.Name, s.Storage, s.Bucket, s.BlockSize, s.TrashDays, st.Stat.UsedSpace, st.Stat.UsedInodes)
	for _, ss := range st.Sessions {
		if ss.ProcessID != os.Getpid() {
			continue
		}
		session, err := c.SessionInfo(ss.Sid)
		if err != nil {
			return err
		}
		fmt.Printf("session %d: host=%s version=%s open deleted files=%d flocks=%d plocks=%d\n",
			session.Sid, session.HostName, session.Version, len(session.Sustained), len(session.Flocks), len(session.Plocks))
	}
	return nil
}
