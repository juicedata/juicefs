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

package vfs

import (
	"compress/gzip"
	"io"
	"os"
	"sort"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
)

// Backup metadata periodically in the object storage
func Backup(m meta.Meta, blob object.ObjectStorage, interval time.Duration) {
	ctx := meta.Background
	key := "lastBackup"
	for {
		utils.SleepWithJitter(interval / 10)
		var value []byte
		if st := m.GetXattr(ctx, 0, key, &value); st != 0 && st != meta.ENOATTR {
			logger.Warnf("getxattr inode 1 key %s: %s", key, st)
			continue
		}
		var last time.Time
		var err error
		if len(value) > 0 {
			last, err = time.Parse(time.RFC3339, string(value))
		}
		if err != nil {
			logger.Warnf("parse time value %s: %s", value, err)
			continue
		}
		if now := time.Now(); now.Sub(last) >= interval {
			if interval <= time.Hour {
				var iused, dummy uint64
				_ = m.StatFS(ctx, &dummy, &dummy, &iused, &dummy)
				if iused > 1e6 {
					logger.Warnf("backup metadata skipped because of too many inodes: %d %s; "+
						"you may increase `--backup-meta` to enable it again", iused, interval)
					continue
				}
			}
			if st := m.SetXattr(ctx, 0, key, []byte(now.Format(time.RFC3339)), meta.XattrCreateOrReplace); st != 0 {
				logger.Warnf("setxattr inode 1 key %s: %s", key, st)
				continue
			}
			go cleanupBackups(blob, now)
			logger.Debugf("backup metadata started")
			if err = backup(m, blob, now); err == nil {
				logger.Infof("backup metadata succeed, used %s", time.Since(now))
			} else {
				logger.Warnf("backup metadata failed: %s", err)
			}
		}
	}
}

func backup(m meta.Meta, blob object.ObjectStorage, now time.Time) error {
	name := "dump-" + now.UTC().Format("2006-01-02-150405") + ".json.gz"
	fp, err := os.CreateTemp("", "juicefs-meta-*")
	if err != nil {
		return err
	}
	defer os.Remove(fp.Name())
	defer fp.Close()
	zw := gzip.NewWriter(fp)
	err = m.DumpMeta(zw, 0, false) // force dump the whole tree
	_ = zw.Close()
	if err != nil {
		return err
	}
	if _, err = fp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return blob.Put("meta/"+name, fp)
}

func cleanupBackups(blob object.ObjectStorage, now time.Time) {
	blob = object.WithPrefix(blob, "meta/")
	ch, err := osync.ListAll(blob, "", "")
	if err != nil {
		logger.Warnf("listAll prefix meta/: %s", err)
		return
	}
	var objs []string
	for o := range ch {
		if o == nil {
			logger.Warnf("list failed, skip cleanup")
			return
		}
		if !o.IsDir() {
			objs = append(objs, o.Key())
		}
	}

	toDel := rotate(objs, now)
	for _, o := range toDel {
		if err = blob.Delete(o); err != nil {
			logger.Warnf("delete object %s: %s", o, err)
		}
	}
}

// Cleanup policy:
// 1. keep all backups within 2 days
// 2. keep one backup each day within 2 weeks
// 3. keep one backup each week within 2 months
// 4. keep one backup each month for those before 2 months
func rotate(objs []string, now time.Time) []string {
	var days = 2
	edge := now.UTC().AddDate(0, 0, -days)
	next := func() {
		if days < 14 {
			days++
			edge = edge.AddDate(0, 0, -1)
		} else if days < 60 {
			days += 7
			edge = edge.AddDate(0, 0, -7)
		} else {
			days += 30
			edge = edge.AddDate(0, 0, -30)
		}
	}

	var toDel, within []string
	sort.Strings(objs)
	for i := len(objs) - 1; i >= 0; i-- {
		if len(objs[i]) != 30 { // len("dump-2006-01-02-150405.json.gz")
			logger.Warnf("bad object for metadata backup %s: length %d", objs[i], len(objs[i]))
			continue
		}
		ts, err := time.Parse("2006-01-02-150405", objs[i][5:22])
		if err != nil {
			logger.Warnf("bad object for metadata backup %s: %s", objs[i], err)
			continue
		}

		if ts.Before(edge) {
			if l := len(within); l > 0 { // keep the earliest one
				toDel = append(toDel, within[:l-1]...)
				within = within[:0]
			}
			for next(); ts.Before(edge); next() {
			}
			within = append(within, objs[i])
		} else if days > 2 {
			within = append(within, objs[i])
		}
	}
	if l := len(within); l > 0 {
		toDel = append(toDel, within[:l-1]...)
	}
	return toDel
}
