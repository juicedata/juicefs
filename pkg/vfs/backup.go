/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
)

// Backup metadata periodically in the object storage
func Backup(blob object.ObjectStorage, interval time.Duration) {
	ctx := meta.Background
	key := "lastBackup"
	for {
		time.Sleep(interval / 20)
		var value []byte
		if st := m.GetXattr(ctx, 1, key, &value); st != 0 && st != meta.ENOATTR {
			logger.Warnf("getxattr inode 1 key %s: %s", key, st)
			continue
		}
		var last time.Time
		var err error
		if len(value) == 17 { // len("2006-01-02-150405")
			last, err = time.Parse("2006-01-02-150405", string(value))
		}
		if err != nil {
			logger.Warnf("parse time value %s: %s", value, err)
			continue
		}
		if now := time.Now().UTC(); now.Sub(last) >= interval {
			val := now.Format("2006-01-02-150405")
			if st := m.SetXattr(ctx, 1, key, []byte(val), meta.XattrCreateOrReplace); st != 0 {
				logger.Warnf("setxattr inode 1 key %s: %s", key, st)
				continue
			}
			go cleanupBackups(blob)
			logger.Infof("backup metadata started")
			if err = backup(blob, val); err == nil {
				logger.Info("backup metadata succeed")
			} else {
				logger.Warnf("backup metadata failed: %s", err)
			}
		}
	}
}

func backup(blob object.ObjectStorage, name string) error {
	name = "dump-" + name + ".json.gz"
	fpath := "/tmp/juicefs-meta-" + name
	fp, err := os.OpenFile(fpath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0444)
	if err != nil {
		return err
	}
	defer os.Remove(fpath)
	defer fp.Close()
	zw := gzip.NewWriter(fp)
	err = m.DumpMeta(zw)
	zw.Close()
	if err != nil {
		return err
	}
	if _, err = fp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return blob.Put("meta/"+name, fp)
}

func cleanupBackups(blob object.ObjectStorage) {
	objChan, err := blob.ListAll("meta/", "")
	if err != nil {
		logger.Warnf("listAll prefix meta/: %s", err)
		return
	}
	var objs []string
	for o := range objChan {
		objs = append(objs, o.Key())
	}
	sort.Strings(objs)

	// Cleanup policy:
	// 1. keep all backups within 2 days
	// 2. keep one backup each day within 2 weeks
	// 3. keep one backup each week within 2 months
	// 4. keep one backup each month for those before 2 months
	var days = 2
	edge := time.Now().UTC().AddDate(0, 0, -days)
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

	for i := len(objs) - 1; i >= 0; i-- {
		if len(objs[i]) != 35 { // len("meta/dump-2006-01-02-150405.json.gz")
			logger.Warnf("bad object for metadata backup: %s", objs[i])
			continue
		}
		ts, err := time.Parse("2006-01-02-150405", objs[i][10:27])
		if err != nil {
			logger.Warnf("bad object for metadata backup: %s", objs[i])
			continue
		}

		if ts.Before(edge) {
			for next(); ts.Before(edge); next() {
			}
		} else if days > 2 {
			if err = blob.Delete(objs[i]); err != nil {
				logger.Warnf("delete object %s: %s", objs[i], err)
			}
		}
	}
}
