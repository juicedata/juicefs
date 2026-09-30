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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

func TestRotate(t *testing.T) {
	format := func(ts time.Time) string {
		return "dump-" + ts.UTC().Format("2006-01-02-150405") + ".json.gz"
	}

	now := time.Now()
	objs := make([]string, 0, 25)
	for cursor, i := now.AddDate(0, 0, -100), 0; i <= 200; i++ { // one backup for every half day
		objs = append(objs, format(cursor))
		toDel := rotate(objs, cursor)
		for _, d := range toDel {
			for j, k := range objs {
				if k == d {
					objs = append(objs[:j], objs[j+1:]...)
					break
				}
			}
		}
		cursor = cursor.Add(time.Duration(12) * time.Hour)
	}

	expect := make([]string, 0, 25)
	expect = append(expect, format(now.AddDate(0, 0, -100)))
	for days := 65; days > 14; days -= 7 {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for days := 13; days > 2; days-- {
		expect = append(expect, format(now.AddDate(0, 0, -days)))
	}
	for i := 4; i >= 0; i-- {
		expect = append(expect, format(now.Add(time.Duration(-i*12)*time.Hour)))
	}

	if len(objs) != len(expect) {
		t.Fatalf("length of objs %d != length of expect %d", len(objs), len(expect))
	}
	for i, o := range objs {
		if o != expect[i] {
			t.Fatalf("obj %s != expect %s", o, expect[i])
		}
	}
}

func TestBackup(t *testing.T) {
	v, blob := createTestVFS(nil, "")
	go Backup(v.Meta, blob, time.Millisecond*100, false)
	time.Sleep(time.Millisecond * 100)

	blob = object.WithPrefix(blob, "meta/")
	kc, _ := object.ListAll(context.TODO(), blob, "", "", true, false)
	var keys []string
	for obj := range kc {
		keys = append(keys, obj.Key())
	}
	if len(keys) < 1 {
		t.Fatalf("there should be at least 1 backup file")
	}
}

func TestUsedInodesWithSubdir(t *testing.T) {
	v, _ := createTestVFS(nil, "")
	m := v.Meta
	ctx := meta.Background()
	var inode meta.Ino
	var attr meta.Attr
	if st := m.Mkdir(ctx, meta.RootInode, "sub", 0755, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("mkdir sub: %s", st)
	}
	for i := 0; i < 5; i++ {
		if st := m.Create(ctx, meta.RootInode, fmt.Sprintf("f%d", i), 0644, 0, 0, &inode, &attr); st != 0 {
			t.Fatalf("create f%d: %s", i, st)
		}
	}
	quotas := map[string]*meta.Quota{"/sub": {MaxSpace: 1 << 30, MaxInodes: -1}}
	if err := m.HandleQuota(ctx, meta.QuotaSet, "/sub", meta.DirQuotaType, quotas, false, false, false); err != nil {
		t.Fatalf("set quota: %s", err)
	}
	// quotas are loaded into the client cache with a new session
	if err := m.NewSession(false); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()

	want := usedInodes(ctx, m)
	if st := m.Chroot(ctx, "sub"); st != 0 {
		t.Fatalf("chroot sub: %s", st)
	}
	var dummy, subUsed uint64
	_ = m.StatFS(ctx, meta.RootInode, &dummy, &dummy, &subUsed, &dummy)
	if subUsed >= want {
		t.Fatalf("used inodes of the subdir quota = %d, want less than %d (the whole volume)", subUsed, want)
	}
	if got := usedInodes(ctx, m); got != want {
		t.Fatalf("used inodes with subdir = %d, want %d (the whole volume)", got, want)
	}
}
