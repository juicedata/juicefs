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

//
//mutate:disable
//nolint:errcheck
package meta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

func testConfig() *Config {
	conf := DefaultConf()
	conf.DirStatFlushPeriod = 100 * time.Millisecond
	return conf
}

func testFormat() *Format {
	return &Format{Name: "test", DirStats: true}
}

func TestRedisClient(t *testing.T) {
	m, err := newRedisMeta("redis", "127.0.0.1:6379/10", testConfig())
	if err != nil || m.Name() != "redis" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestKeyDB(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	// 127.0.0.1:6378 enable flash, 127.0.0.1:6377 disable flash
	for _, addr := range []string{"127.0.0.1:6378/10", "127.0.0.1:6377/10"} {
		m, err := newRedisMeta("redis", addr, testConfig())
		if err != nil || m.Name() != "redis" {
			t.Fatalf("create meta: %s", err)
		}
		if r, ok := m.(*redisMeta); ok {
			rawInfo, err := r.rdb.Info(Background()).Result()
			if err != nil {
				t.Fatalf("parse info: %s", err)
			}
			var storageProvider, maxMemoryPolicy string
			for _, l := range strings.Split(strings.TrimSpace(rawInfo), "\n") {
				l = strings.TrimSpace(l)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				kvPair := strings.SplitN(l, ":", 2)
				if len(kvPair) < 2 {
					continue
				}
				key, val := kvPair[0], kvPair[1]
				switch key {
				case "maxmemory_policy":
					maxMemoryPolicy = val
				case "storage_provider":
					storageProvider = val
				}
			}
			if storageProvider == "none" && maxMemoryPolicy != "noeviction" {
				t.Fatalf("maxmemory_policy should be noeviction")
			}
			if storageProvider == "flash" && maxMemoryPolicy == "noeviction" {
				t.Fatalf("maxmemory_policy should not be noeviction")
			}
		} else {
			t.Fatalf("should be redisMeta")
		}
	}
}

func TestRedisCluster(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	m, err := newRedisMeta("redis", "127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003/2", testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func testMeta(t *testing.T, m Meta) {
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}

	testMetaClient(t, m)
	testTruncateAndDelete(t, m)
	testTrash(t, m)
	testParents(t, m)
	testRemove(t, m)
	testResolve(t, m)
	testStickyBit(t, m)
	testLocks(t, m)
	testListLocks(t, m)
	testConcurrentWrite(t, m)
	testCompaction(t, m, false)
	time.Sleep(time.Second)
	testCompaction(t, m, true)
	testCopyFileRange(t, m)
	testCloseSession(t, m)
	testConcurrentDir(t, m)
	testAttrFlags(t, m)
	testQuota(t, m)
	testAtime(t, m)
	testAccess(t, m)
	base := m.getBase()
	base.conf.OpenCache = time.Second
	base.of.expire = time.Second
	testOpenCache(t, m)
	base.conf.CaseInsensi = true
	testCaseIncensi(t, m)
	testCheckAndRepair(t, m)
	testDirStat(t, m)
	testClone(t, m)
	testACL(t, m)
	base.conf.ReadOnly = true
	testReadOnly(t, m)
}

func testAccess(t *testing.T, m Meta) {
	if err := m.Init(testFormat(), false); err != nil {
		t.Fatalf("init error: %s", err)
	}

	defer m.getBase().aclCache.Clear()

	var testNode Ino = 2
	ctx := NewContext(1, 1, []uint32{2})
	attr := &Attr{
		Mode:       0541,
		Uid:        0,
		Gid:        0,
		AccessACL:  1,
		DefaultACL: 0,
		Full:       true,
	}

	r1 := &aclAPI.Rule{
		Owner: 5,
		Group: 4,
		Mask:  2,
		Other: 1,
		NamedUsers: aclAPI.Entries{
			{
				Id:   1,
				Perm: 6,
			},
		},
		NamedGroups: aclAPI.Entries{
			{
				Id:   2,
				Perm: 6,
			},
		},
	}
	m.getBase().aclCache.Put(1, r1)

	// case: match owner, skip named entries
	st := m.Access(ctx, testNode, MODE_MASK_R|MODE_MASK_W, attr)
	assert.Equal(t, syscall.EACCES, st)

	// case: match named grouped entry, but group perm & mask failed
	ctx = NewContext(1, 2, []uint32{2})
	st = m.Access(ctx, testNode, MODE_MASK_R|MODE_MASK_W, attr)
	assert.Equal(t, syscall.EACCES, st)

	// case: same as above, make mask to pass test
	r2 := &aclAPI.Rule{}
	*r2 = *r1
	r2.Mask = 7
	m.getBase().aclCache.Put(2, r2)
	attr.AccessACL = 2

	ctx = NewContext(1, 2, []uint32{2})
	st = m.Access(ctx, testNode, MODE_MASK_R|MODE_MASK_W, attr)
	assert.Equal(t, syscall.Errno(0), st)
}

func testACL(t *testing.T, m Meta) {
	format := testFormat()
	format.EnableACL = true

	if err := m.Init(format, false); err != nil {
		t.Fatalf("test acl failed: %s", err)
	}

	defer m.getBase().aclCache.Clear()

	ctx := Background()
	testDir := "test_dir"
	var testDirIno Ino
	attr1 := &Attr{}

	if st := m.Mkdir(ctx, RootInode, testDir, 0644, 0, 0, &testDirIno, attr1); st != 0 {
		t.Fatalf("create %s: %s", testDir, st)
	}
	defer m.Rmdir(ctx, RootInode, testDir)

	rule := &aclAPI.Rule{
		Owner: 7,
		Group: 7,
		Mask:  7,
		Other: 7,
		NamedUsers: []aclAPI.Entry{
			{
				Id:   1001,
				Perm: 4,
			},
		},
		NamedGroups: nil,
	}

	// case: setfacl
	if st := m.SetFacl(ctx, testDirIno, aclAPI.TypeAccess, rule); st != 0 {
		t.Fatalf("setfacl error: %s", st)
	}

	// case: getfacl
	rule2 := &aclAPI.Rule{}
	if st := m.GetFacl(ctx, testDirIno, aclAPI.TypeAccess, rule2); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	assert.True(t, rule.IsEqual(rule2))

	// case: setfacl will sync mode (group class is mask)
	attr2 := &Attr{}
	if st := m.GetAttr(ctx, testDirIno, attr2); st != 0 {
		t.Fatalf("getattr error: %s", st)
	}
	assert.Equal(t, uint16(0777), attr2.Mode)

	// case: setattr will sync acl
	set := uint16(0) | SetAttrMode
	attr2 = &Attr{
		Mode: 0555,
	}
	if st := m.SetAttr(ctx, testDirIno, set, 0, attr2); st != 0 {
		t.Fatalf("setattr error: %s", st)
	}

	rule3 := &aclAPI.Rule{}
	if st := m.GetFacl(ctx, testDirIno, aclAPI.TypeAccess, rule3); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	rule2.Owner = 5
	rule2.Mask = 5
	rule2.Other = 5
	assert.True(t, rule3.IsEqual(rule2))

	// case: remove acl
	rule3.Mask = 0xFFFF
	rule3.NamedUsers = nil
	rule3.NamedGroups = nil
	if st := m.SetFacl(ctx, testDirIno, aclAPI.TypeAccess, rule3); st != 0 {
		t.Fatalf("setattr error: %s", st)
	}

	st := m.GetFacl(ctx, testDirIno, aclAPI.TypeAccess, nil)
	assert.Equal(t, ENOATTR, st)

	attr2 = &Attr{}
	if st := m.GetAttr(ctx, testDirIno, attr2); st != 0 {
		t.Fatalf("getattr error: %s", st)
	}
	assert.Equal(t, uint16(0575), attr2.Mode)

	// case: set normal default acl
	if st := m.SetFacl(ctx, testDirIno, aclAPI.TypeDefault, rule); st != 0 {
		t.Fatalf("setfacl error: %s", st)
	}

	// case: get normal default acl
	rule2 = &aclAPI.Rule{}
	if st := m.GetFacl(ctx, testDirIno, aclAPI.TypeDefault, rule2); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	assert.True(t, rule2.IsEqual(rule))

	// case: mk subdir with normal default acl
	subDir := "sub_dir"
	var subDirIno Ino
	attr2 = &Attr{}

	mode := uint16(0222)
	// cumask will be ignored
	if st := m.Mkdir(ctx, testDirIno, subDir, mode, 0022, 0, &subDirIno, attr2); st != 0 {
		t.Fatalf("create %s: %s", subDir, st)
	}
	defer m.Rmdir(ctx, testDirIno, subDir)

	// subdir inherit default acl
	rule3 = &aclAPI.Rule{}
	if st := m.GetFacl(ctx, subDirIno, aclAPI.TypeDefault, rule3); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	assert.True(t, rule3.IsEqual(rule2))

	// subdir access acl
	rule3 = &aclAPI.Rule{}
	if st := m.GetFacl(ctx, subDirIno, aclAPI.TypeAccess, rule3); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	rule2.Owner &= (mode >> 6) & 7
	rule2.Mask &= (mode >> 3) & 7
	rule2.Other &= mode & 7
	assert.True(t, rule3.IsEqual(rule2))

	// case: set minimal default acl
	rule = &aclAPI.Rule{
		Owner:       5,
		Group:       5,
		Mask:        0xFFFF,
		Other:       5,
		NamedUsers:  nil,
		NamedGroups: nil,
	}
	if st := m.SetFacl(ctx, testDirIno, aclAPI.TypeDefault, rule); st != 0 {
		t.Fatalf("setfacl error: %s", st)
	}

	// case: get minimal default acl
	rule2 = &aclAPI.Rule{}
	if st := m.GetFacl(ctx, testDirIno, aclAPI.TypeDefault, rule2); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	assert.True(t, rule2.IsEqual(rule))

	// case: mk subdir with minimal default acl
	subDir2 := "sub_dir2"
	var subDirIno2 Ino
	attr2 = &Attr{}

	mode = uint16(0222)
	if st := m.Mkdir(ctx, testDirIno, subDir2, mode, 0022, 0, &subDirIno2, attr2); st != 0 {
		t.Fatalf("create %s: %s", subDir, st)
	}
	defer m.Rmdir(ctx, testDirIno, subDir2)
	assert.Equal(t, uint16(0), attr2.Mode)

	// subdir inherit default acl
	rule3 = &aclAPI.Rule{}
	if st := m.GetFacl(ctx, subDirIno2, aclAPI.TypeDefault, rule3); st != 0 {
		t.Fatalf("getfacl error: %s", st)
	}
	assert.True(t, rule3.IsEqual(rule2))

	// subdir have no access acl
	rule3 = &aclAPI.Rule{}
	st = m.GetFacl(ctx, subDirIno2, aclAPI.TypeAccess, rule3)
	assert.Equal(t, ENOATTR, st)

	// test cache all
	sz := m.getBase().aclCache.Size()
	err := m.getBase().en.cacheACLs(ctx)
	assert.Nil(t, err)
	assert.Equal(t, sz, m.getBase().aclCache.Size())
}

func testMetaClient(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error { return nil })
	ctx := Background()
	var attr = &Attr{}
	if st := m.GetAttr(ctx, 1, attr); st != 0 || attr.Mode != 0777 { // getattr of root always succeed
		t.Fatalf("getattr root: %s", st)
	}

	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}
	if err := m.Init(&Format{Name: "test2"}, false); err == nil { // not allowed
		t.Fatalf("change name without --force is not allowed")
	}
	format, err := m.Load(true)
	if err != nil {
		t.Fatalf("load failed after initialization: %s", err)
	}
	if format.Name != "test" {
		t.Fatalf("load got volume name %s, expected %s", format.Name, "test")
	}
	if err = m.NewSession(true); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()
	ses, err := m.ListSessions()
	if err != nil || len(ses) != 1 {
		t.Fatalf("list sessions %+v: %s", ses, err)
	}
	base := m.getBase()
	if base.sid != ses[0].Sid {
		t.Fatalf("my sid %d != registered sid %d", base.sid, ses[0].Sid)
	}
	go m.CleanStaleSessions(Background())

	var parent, inode, dummyInode Ino
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	defer m.Rmdir(ctx, 1, "d")
	if st := m.Unlink(ctx, 1, "d"); st != syscall.EPERM {
		t.Fatalf("unlink d: %s", st)
	}
	if st := m.Rmdir(ctx, parent, "."); st != syscall.EINVAL {
		t.Fatalf("unlink d.: %s", st)
	}
	if st := m.Rmdir(ctx, parent, ".."); st != syscall.ENOTEMPTY {
		t.Fatalf("unlink d..: %s", st)
	}
	if st := m.Lookup(ctx, 1, "d", &parent, attr, true); st != 0 {
		t.Fatalf("lookup d: %s", st)
	}
	if st := m.Lookup(ctx, 1, "d", &parent, nil, true); st != syscall.EINVAL {
		t.Fatalf("lookup d: %s", st)
	}
	if st := m.Lookup(ctx, 1, "..", &inode, attr, true); st != 0 || inode != 1 {
		t.Fatalf("lookup ..: %s", st)
	}
	if st := m.Lookup(ctx, parent, ".", &inode, attr, true); st != 0 || inode != parent {
		t.Fatalf("lookup .: %s", st)
	}
	if st := m.Lookup(ctx, parent, "..", &inode, attr, true); st != 0 || inode != 1 {
		t.Fatalf("lookup ..: %s", st)
	}
	if attr.Nlink != 3 {
		t.Fatalf("nlink expect 3, but got %d", attr.Nlink)
	}
	if st := m.Access(ctx, parent, 4, attr); st != 0 {
		t.Fatalf("access d: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	_ = m.Close(ctx, inode)
	var tino Ino
	if st := m.Lookup(ctx, inode, ".", &tino, attr, true); st != 0 {
		t.Fatalf("lookup /d/f/.: %s", st)
	}
	if st := m.Lookup(ctx, inode, "..", &tino, attr, true); st != syscall.ENOTDIR {
		t.Fatalf("lookup /d/f/..: %s", st)
	}
	defer m.Unlink(ctx, parent, "f")
	if st := m.Rmdir(ctx, parent, "f"); st != syscall.ENOTDIR {
		t.Fatalf("rmdir f: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != syscall.ENOTEMPTY {
		t.Fatalf("rmdir d: %s", st)
	}
	if st := m.Mknod(ctx, inode, "df", TypeFile, 0650, 022, 0, "", &dummyInode, nil); st != syscall.ENOTDIR {
		t.Fatalf("create fd: %s", st)
	}
	if st := m.Mknod(ctx, parent, "f", TypeFile, 0650, 022, 0, "", &inode, attr); st != syscall.EEXIST {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Lookup(ctx, parent, "f", &inode, attr, true); st != 0 {
		t.Fatalf("lookup f: %s", st)
	}
	if st := m.Resolve(ctx, 1, "d/f", &inode, attr); st != 0 && st != syscall.ENOTSUP {
		t.Fatalf("resolve d/f: %s", st)
	}
	if st := m.Resolve(ctx, parent, "/f", &inode, attr); st != 0 && st != syscall.ENOTSUP {
		t.Fatalf("resolve f: %s", st)
	}
	var ctx2 = NewContext(0, 1, []uint32{1})
	if st := m.Resolve(ctx2, parent, "/f", &inode, attr); st != syscall.EACCES && st != syscall.ENOTSUP {
		t.Fatalf("resolve f: %s", st)
	}
	if st := m.Resolve(ctx, parent, "/f/c", &inode, attr); st != syscall.ENOTDIR && st != syscall.ENOTSUP {
		t.Fatalf("resolve f: %s", st)
	}
	if st := m.Resolve(ctx, parent, "/f2", &inode, attr); st != syscall.ENOENT && st != syscall.ENOTSUP {
		t.Fatalf("resolve f2: %s", st)
	}
	// check owner permission
	var p1, c1 Ino
	if st := m.Mkdir(ctx2, 1, "d1", 02777, 0, 0, &p1, attr); st != 0 {
		t.Fatalf("mkdir d1: %s", st)
	}
	attr.Gid = 1
	m.SetAttr(ctx, p1, SetAttrGID, 0, attr)
	if attr.Mode&02000 == 0 {
		t.Fatalf("SGID is lost")
	}
	var ctx3 = NewContext(2, 2, []uint32{2})
	if st := m.Mkdir(ctx3, p1, "d2", 0777, 022, 0, &c1, attr); st != 0 {
		t.Fatalf("mkdir d2: %s", st)
	}
	if attr.Gid != ctx2.Gid() {
		t.Fatalf("inherit gid: %d != %d", attr.Gid, ctx2.Gid())
	}
	if runtime.GOOS == "linux" {
		if attr.Mode&02000 == 0 {
			t.Fatalf("not inherit sgid")
		}
		if st := m.Mknod(ctx2, p1, "f1", TypeFile, 02777, 022, 0, "", &dummyInode, attr); st != 0 {
			t.Fatalf("create f1: %s", st)
		} else if attr.Mode&02010 != 02010 {
			t.Fatalf("sgid should not be cleared")
		}
		if st := m.Mknod(ctx3, p1, "f2", TypeFile, 02777, 022, 0, "", &dummyInode, attr); st != 0 {
			t.Fatalf("create f2: %s", st)
		} else if attr.Mode&02010 != 00010 {
			t.Fatalf("sgid should be cleared")
		}

	}
	if st := m.Resolve(ctx2, 1, "/d1/d2", nil, nil); st != 0 && st != syscall.ENOTSUP {
		t.Fatalf("resolve /d1/d2: %s", st)
	}
	if st := m.Remove(ctx, 1, "d1", false, RmrDefaultThreads, nil); st != 0 {
		t.Fatalf("Remove d1: %s", st)
	}
	attr.Atime = 2
	attr.Mtime = 2
	attr.Uid = 1
	attr.Gid = 1
	attr.Mode = 0640
	if st := m.SetAttr(ctx, inode, SetAttrAtime|SetAttrMtime|SetAttrUID|SetAttrGID|SetAttrMode, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.SetAttr(ctx, inode, 0, 0, attr); st != 0 { // changes nothing
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Atime != 2 || attr.Mtime != 2 || attr.Uid != 1 || attr.Gid != 1 || attr.Mode != 0640 {
		t.Fatalf("atime:%d mtime:%d uid:%d gid:%d mode:%o", attr.Atime, attr.Mtime, attr.Uid, attr.Gid, attr.Mode)
	}
	if st := m.SetAttr(ctx, inode, SetAttrAtimeNow|SetAttrMtimeNow, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	fakeCtx := NewContext(100, 2, []uint32{2, 1})
	if st := m.Access(fakeCtx, parent, 2, nil); st != syscall.EACCES {
		t.Fatalf("access d: %s", st)
	}
	if st := m.Access(fakeCtx, inode, 4, nil); st != 0 {
		t.Fatalf("access f: %s", st)
	}
	var entries []*Entry
	if st := m.Readdir(ctx, parent, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 3 {
		t.Fatalf("entries: %d", len(entries))
	} else if string(entries[0].Name) != "." || string(entries[1].Name) != ".." || string(entries[2].Name) != "f" {
		t.Fatalf("entries: %+v", entries)
	}
	if st := m.Rename(ctx, parent, "f", 1, "f2", RenameWhiteout, &inode, attr); st != syscall.ENOTSUP {
		t.Fatalf("rename d/f -> f2: %s", st)
	}
	if st := m.Rename(ctx, parent, "f", 1, "f2", 0, &inode, attr); st != 0 {
		t.Fatalf("rename d/f -> f2: %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f2")
	}()
	if st := m.Rename(ctx, 1, "f2", 1, "f2", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> f2: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "f", RenameExchange, &inode, attr); st != syscall.ENOENT {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	_ = m.Close(ctx, inode)
	defer m.Unlink(ctx, 1, "f")
	if st := m.Rename(ctx, 1, "f2", 1, "f", RenameNoReplace, &inode, attr); st != syscall.EEXIST {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "f", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> f: %s", st)
	}
	if st := m.Rename(ctx, 1, "f", 1, "d", RenameExchange, &inode, attr); st != 0 {
		t.Fatalf("rename f <-> d: %s", st)
	}
	if st := m.Rename(ctx, 1, "d", 1, "f", 0, &inode, attr); st != 0 {
		t.Fatalf("rename d -> f: %s", st)
	}
	if st := m.GetAttr(ctx, 1, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Nlink != 2 {
		t.Fatalf("nlink expect 2, but got %d", attr.Nlink)
	}
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	// Test rename with parent change
	var parent2 Ino
	if st := m.Mkdir(ctx, 1, "d4", 0777, 0, 0, &parent2, attr); st != 0 {
		t.Fatalf("create dir d4: %s", st)
	}
	if st := m.Mkdir(ctx, parent2, "d5", 0777, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create dir d4/d5: %s", st)
	}
	if st := m.Rename(ctx, parent2, "d5", 1, "d5", RenameNoReplace, &inode, attr); st != 0 {
		t.Fatalf("rename d4/d5 <-> d5: %s", st)
	} else if attr.Parent != 1 {
		t.Fatalf("after rename d4/d5 <-> d5 parent %d expect 1", attr.Parent)
	}
	if st := m.Mknod(ctx, parent2, "f6", TypeFile, 0650, 022, 0, "", &inode, attr); st != 0 {
		t.Fatalf("create dir d4/f6: %s", st)
	}
	if st := m.Rename(ctx, 1, "d5", parent2, "f6", RenameExchange, &inode, attr); st != 0 {
		t.Fatalf("rename d5 <-> d4/d6: %s", st)
	} else if attr.Parent != parent2 {
		t.Fatalf("after exchange d5 <-> d4/f6 parent %d expect %d", attr.Parent, parent2)
	} else if attr.Typ != TypeDirectory {
		t.Fatalf("after exchange d5 <-> d4/f6 type %d expect %d", attr.Typ, TypeDirectory)
	}
	if st := m.Lookup(ctx, 1, "d5", &inode, attr, true); st != 0 || attr.Parent != 1 {
		t.Fatalf("lookup d5 after exchange: %s; parent %d expect 1", st, attr.Parent)
	} else if attr.Typ != TypeFile {
		t.Fatalf("after exchange d5 <-> d4/f6 type %d expect %d", attr.Typ, TypeFile)
	}
	if st := m.Rmdir(ctx, parent2, "f6"); st != 0 {
		t.Fatalf("rmdir d4/f6 : %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d4"); st != 0 {
		t.Fatalf("rmdir d4 first : %s", st)
	}
	if st := m.Unlink(ctx, 1, "d5"); st != 0 {
		t.Fatalf("rmdir d6 : %s", st)
	}
	if st := m.Lookup(ctx, 1, "f", &inode, attr, true); st != 0 {
		t.Fatalf("lookup f: %s", st)
	}
	if st := m.Link(ctx, inode, 1, "f3", attr); st != 0 {
		t.Fatalf("link f3 -> f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f3")
	if st := m.Link(ctx, inode, 1, "F3", attr); st != 0 { // CaseInsensi = false
		t.Fatalf("link F3 -> f: %s", st)
	}
	if st := m.Link(ctx, parent, 1, "d2", attr); st != syscall.EPERM {
		t.Fatalf("link d2 -> d: %s", st)
	}
	if st := m.Symlink(ctx, 1, "s", "/f", &inode, attr); st != 0 {
		t.Fatalf("symlink s -> /f: %s", st)
	}
	if attr.Mode&0777 != 0777 {
		t.Fatalf("mode of symlink should be 0777")
	}
	defer m.Unlink(ctx, 1, "s")
	var target1, target2 []byte
	if st := m.ReadLink(ctx, inode, &target1); st != 0 {
		t.Fatalf("readlink s: %s", st)
	}
	if st := m.ReadLink(ctx, inode, &target2); st != 0 { // cached
		t.Fatalf("readlink s: %s", st)
	}
	if !bytes.Equal(target1, target2) || !bytes.Equal(target1, []byte("/f")) {
		t.Fatalf("readlink got %s %s, expected %s", target1, target2, "/f")
	}
	if st := m.ReadLink(ctx, parent, &target1); st != syscall.EINVAL {
		t.Fatalf("readlink d: %s", st)
	}
	if st := m.Lookup(ctx, 1, "f", &inode, attr, true); st != 0 {
		t.Fatalf("lookup f: %s", st)
	}

	// data
	var sliceId uint64
	// try to open a file that does not exist
	if st := m.Open(ctx, 99999, syscall.O_RDWR, &Attr{}); st != syscall.ENOENT {
		t.Fatalf("open not exist inode got %d, expected %d", st, syscall.ENOENT)
	}
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	_ = m.Close(ctx, inode)
	if st := m.NewSlice(ctx, &sliceId); st != 0 {
		t.Fatalf("write chunk: %s", st)
	}
	var s = Slice{Id: sliceId, Size: 100, Len: 100}
	if st := m.Write(ctx, inode, 0, 100, s, time.Now()); st != 0 {
		t.Fatalf("write end: %s", st)
	}
	var slices []Slice
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 2 || slices[0].Id != 0 || slices[0].Size != 100 || slices[1].Id != sliceId || slices[1].Size != 100 {
		t.Fatalf("slices: %v", slices)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocKeepSize, 100, 50, nil); st != 0 {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocCollapesRange, 100, 50, nil); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocInsertRange, 100, 50, nil); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocCollapesRange, 100, 50, nil); st != syscall.ENOTSUP {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole, 100, 50, nil); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, inode, fallocPunchHole|fallocKeepSize, 0, 0, nil); st != syscall.EINVAL {
		t.Fatalf("fallocate: %s", st)
	}
	if st := m.Fallocate(ctx, parent, fallocPunchHole|fallocKeepSize, 100, 50, nil); st != syscall.EPERM {
		t.Fatalf("fallocate dir: %s", st)
	}
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 3 || slices[1].Id != 0 || slices[1].Len != 50 || slices[2].Id != sliceId || slices[2].Len != 50 {
		t.Fatalf("slices: %v", slices)
	}

	// xattr
	if st := m.SetXattr(ctx, inode, "a", []byte("v"), XattrCreateOrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v2"), XattrCreateOrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	var value []byte
	if st := m.GetXattr(ctx, inode, "a", &value); st != 0 || string(value) != "v2" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
	if st := m.ListXattr(ctx, inode, &value); st != 0 || string(value) != "a\000" {
		t.Fatalf("listxattr: %s %v", st, value)
	}
	if st := m.Unlink(ctx, 1, "F3"); st != 0 {
		t.Fatalf("unlink F3: %s", st)
	}
	if st := m.GetXattr(ctx, inode, "a", &value); st != 0 || string(value) != "v2" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
	if st := m.RemoveXattr(ctx, inode, "a"); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v"), XattrReplace); st != ENOATTR {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v3"), XattrCreate); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v3"), XattrCreate); st != syscall.EEXIST {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v3"), XattrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v4"), XattrReplace); st != 0 {
		t.Fatalf("setxattr: %s", st)
	}
	if st := m.SetXattr(ctx, inode, "a", []byte("v5"), 5); st != syscall.EINVAL {
		t.Fatalf("setxattr: %s", st)
	}

	var totalspace, availspace, iused, iavail uint64
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<50 || iavail != 10<<20 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}
	format.Capacity = 1 << 20
	format.Inodes = 100
	if err = m.Init(format, false); err != nil {
		t.Fatalf("set quota failed: %s", err)
	}
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<20 || iavail != 97 {
		time.Sleep(time.Millisecond * 100)
		_ = m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail)
		if totalspace != 1<<20 || iavail != 97 {
			t.Fatalf("total space %d, iavail %d", totalspace, iavail)
		}
	}
	// test StatFS with subdir and quota
	var subIno Ino
	if st := m.Mkdir(ctx, 1, "subdir", 0755, 0, 0, &subIno, nil); st != 0 {
		t.Fatalf("mkdir subdir: %s", st)
	}
	if st := m.Chroot(ctx, "subdir"); st != 0 {
		t.Fatalf("chroot: %s", st)
	}
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<20 || iavail != 96 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}

	if err := m.HandleQuota(ctx, QuotaSet, "/subdir", map[string]*Quota{
		"/subdir": {
			MaxSpace:  0,
			MaxInodes: 0,
		},
	}, false, false, false); err != nil {
		t.Fatalf("set quota: %s", err)
	}
	base.loadQuotas()
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<20-4*uint64(align4K(0)) || iavail != 96 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}

	if err := m.HandleQuota(ctx, QuotaSet, "/subdir", map[string]*Quota{
		"/subdir": {
			MaxSpace:  1 << 10,
			MaxInodes: 0,
		},
	}, false, false, false); err != nil {
		t.Fatalf("set quota: %s", err)
	}
	base.loadQuotas()
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<10 || iavail != 96 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}

	if err := m.HandleQuota(ctx, QuotaSet, "/subdir", map[string]*Quota{
		"/subdir": {
			MaxSpace:  0,
			MaxInodes: 10,
		},
	}, false, false, false); err != nil {
		t.Fatalf("set quota: %s", err)
	}
	base.loadQuotas()
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<20-4*uint64(align4K(0)) || iavail != 10 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}

	if err := m.HandleQuota(ctx, QuotaSet, "/subdir", map[string]*Quota{
		"/subdir": {
			MaxSpace:  1 << 10,
			MaxInodes: 10,
		},
	}, false, false, false); err != nil {
		t.Fatalf("set quota: %s", err)
	}
	base.loadQuotas()
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<10 || iavail != 10 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}

	m.chroot(RootInode)
	if st := m.StatFS(ctx, RootInode, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<20 || iavail != 96 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}
	// statfs subdir directly
	if st := m.StatFS(ctx, subIno, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<10 || iavail != 10 {
		t.Fatalf("total space %d, iavail %d", totalspace, iavail)
	}

	base.loadQuotas()
	base.quotaMu.RLock()
	q := base.dirQuotas[subIno]
	base.quotaMu.RUnlock()
	q.update(4<<10, 15) // used > max
	base.doFlushQuotas()
	if st := m.StatFS(ctx, subIno, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 4<<10 || availspace != 0 || iused != 15 || iavail != 0 {
		t.Fatalf("total space %d, availspace %d, iused %d, iavail %d", totalspace, availspace, iused, iavail)
	}
	q.update(-8<<10, -20) // used < 0
	base.doFlushQuotas()
	if st := m.StatFS(ctx, subIno, &totalspace, &availspace, &iused, &iavail); st != 0 {
		t.Fatalf("statfs: %s", st)
	}
	if totalspace != 1<<10 || availspace != 1<<10 || iused != 0 || iavail != 10 {
		t.Fatalf("total space %d, availspace %d, iused %d, iavail %d", totalspace, availspace, iused, iavail)
	}

	if st := m.Rmdir(ctx, 1, "subdir"); st != 0 {
		t.Fatalf("rmdir subdir: %s", st)
	}

	var summary Summary
	if st := m.GetSummary(ctx, parent, &summary, false, true); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected := Summary{Length: 0, Size: 4096, Files: 0, Dirs: 1}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	summary = Summary{}
	if st := m.GetSummary(ctx, 1, &summary, true, true); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected = Summary{Length: 400, Size: 20480, Files: 3, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := m.GetSummary(ctx, inode, &summary, true, true); st != 0 {
		t.Fatalf("summary: %s", st)
	}
	expected = Summary{Length: 600, Size: 24576, Files: 4, Dirs: 2}
	if summary != expected {
		t.Fatalf("summary %+v not equal to expected: %+v", summary, expected)
	}
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	if st := m.Unlink(ctx, 1, "f3"); st != 0 {
		t.Fatalf("unlink f3: %s", st)
	}
	time.Sleep(time.Millisecond * 100) // wait for delete
	if st := m.Read(ctx, inode, 0, &slices); st != syscall.ENOENT {
		t.Fatalf("read chunk: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmdir d: %s", st)
	}
}

func testStickyBit(t *testing.T, m Meta) {
	ctx := Background()
	var sticky, normal, inode Ino
	var attr = &Attr{}
	m.Mkdir(ctx, 1, "tmp", 01777, 0, 0, &sticky, attr)
	m.Mkdir(ctx, 1, "tmp2", 0777, 0, 0, &normal, attr)
	ctxA := NewContext(1, 1, []uint32{1})
	// file
	m.Create(ctxA, sticky, "f", 0777, 0, 0, &inode, attr)
	m.Create(ctxA, normal, "f", 0777, 0, 0, &inode, attr)
	ctxB := NewContext(1, 2, []uint32{2})
	if e := m.Unlink(ctxB, sticky, "f"); e != syscall.EACCES {
		t.Fatalf("unlink f: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "f", sticky, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "f", normal, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	m.Create(ctxB, sticky, "f2", 0777, 0, 0, &inode, attr)
	if e := m.Rename(ctxB, sticky, "f2", sticky, "f", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("overwrite f: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "f", sticky, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Rename(ctxA, normal, "f", sticky, "f2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "f", sticky, "f3", 0, &inode, attr); e != 0 {
		t.Fatalf("rename f: %s", e)
	}
	if e := m.Unlink(ctxA, sticky, "f3"); e != 0 {
		t.Fatalf("unlink f3: %s", e)
	}
	// dir
	m.Mkdir(ctxA, sticky, "d", 0777, 0, 0, &inode, attr)
	m.Mkdir(ctxA, normal, "d", 0777, 0, 0, &inode, attr)
	if e := m.Rmdir(ctxB, sticky, "d"); e != syscall.EACCES {
		t.Fatalf("rmdir d: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "d", sticky, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rename(ctxB, sticky, "d", normal, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	m.Mkdir(ctxB, sticky, "d2", 0777, 0, 0, &inode, attr)
	if e := m.Rename(ctxB, sticky, "d2", sticky, "d", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("overwrite d: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "d", sticky, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rename(ctxA, normal, "d", sticky, "d2", 0, &inode, attr); e != syscall.EACCES {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rename(ctxA, sticky, "d", sticky, "d3", 0, &inode, attr); e != 0 {
		t.Fatalf("rename d: %s", e)
	}
	if e := m.Rmdir(ctxA, sticky, "d3"); e != 0 {
		t.Fatalf("rmdir d3: %s", e)
	}
}

func testListLocks(t *testing.T, m Meta) {
	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}

	// flock
	o1 := uint64(0xF000000000000001)
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock: %s", st)
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 1 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
	for i := 2; i < 10; i++ {
		if st := m.Flock(ctx, inode, uint64(i), syscall.F_RDLCK, false); st != 0 {
			t.Fatalf("flock wlock: %s", st)
		}
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 8 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
	for i := 2; i < 10; i++ {
		if st := m.Flock(ctx, inode, uint64(i), syscall.F_UNLCK, false); st != 0 {
			t.Fatalf("flock unlock: %s", st)
		}
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}

	// plock
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_WRLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock rlock: %s", st)
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 1 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_UNLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
	for i := 2; i < 10; i++ {
		if st := m.Setlk(ctx, inode, uint64(i), false, syscall.F_RDLCK, 0, 0xFFFF, 1); st != 0 {
			t.Fatalf("plock rlock: %s", st)
		}
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 8 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
	for i := 2; i < 10; i++ {
		if st := m.Setlk(ctx, inode, uint64(i), false, syscall.F_UNLCK, 0, 0xFFFF, 1); st != 0 {
			t.Fatalf("plock unlock: %s", st)
		}
	}
	if plocks, flocks, err := m.ListLocks(ctx, inode); err != nil || len(plocks) != 0 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
}

func testLocks(t *testing.T, m Meta) {
	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	// flock
	o1 := uint64(0xF000000000000001)
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != 0 {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock again: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_WRLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Flock(ctx, inode, 2, syscall.F_RDLCK, false); st != syscall.EAGAIN {
		t.Fatalf("flock rlock: %s", st)
	}
	if st := m.Flock(ctx, inode, o1, syscall.F_UNLCK, false); st != 0 {
		t.Fatalf("flock unlock: %s", st)
	}
	if r, ok := m.(*redisMeta); ok {
		ms, err := r.rdb.SMembers(context.Background(), r.lockedKey(r.sid)).Result()
		if err != nil {
			t.Fatalf("Smember %s: %s", r.lockedKey(r.sid), err)
		}
		if len(ms) != 0 {
			t.Fatalf("locked inodes leaked: %d", len(ms))
		}
	}

	// POSIX locks
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_UNLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_RDLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock rlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_RDLCK, 0, 0xFFFF, 1); st != 0 {
		t.Fatalf("plock rlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_RDLCK, 0, 0x2FFFF, 1); st != 0 {
		t.Fatalf("plock rlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0, 0xFFFF, 1); st != syscall.EAGAIN {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0x10000, 0x20000, 1); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_UNLCK, 0, 0x20000, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0, 0xFFFF, 10); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_WRLCK, 0x2000, 0xFFFF, 20); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, o1, false, syscall.F_WRLCK, 0, 0xFFFF, 1); st != syscall.EAGAIN {
		t.Fatalf("plock rlock: %s", st)
	}
	var ltype, pid uint32 = syscall.F_WRLCK, 1
	var start, end uint64 = 0x2000, 0xFFFF
	if st := m.Getlk(ctx, inode, o1, &ltype, &start, &end, &pid); st != 0 || ltype != syscall.F_WRLCK || pid != 20 || start != 0x2000 || end != 0xFFFF {
		t.Fatalf("plock get rlock: %s, %d %d %x %x", st, ltype, pid, start, end)
	}
	if st := m.Setlk(ctx, inode, 2, false, syscall.F_UNLCK, 0, 0x2FFFF, 1); st != 0 {
		t.Fatalf("plock unlock: %s", st)
	}
	ltype = syscall.F_WRLCK
	start, end = 0, 0xFFFFFF
	if st := m.Getlk(ctx, inode, o1, &ltype, &start, &end, &pid); st != 0 || ltype != syscall.F_UNLCK || pid != 0 || start != 0 || end != 0 {
		t.Fatalf("plock get rlock: %s, %d %d %x %x", st, ltype, pid, start, end)
	}

	// concurrent locks
	var g sync.WaitGroup
	var count int
	var err syscall.Errno
	for i := 0; i < 100; i++ {
		g.Add(1)
		go func(i int) {
			defer g.Done()
			if st := m.Setlk(ctx, inode, uint64(i), true, syscall.F_WRLCK, 0, 0xFFFF, uint32(i)); st != 0 {
				err = st
			}
			count++
			time.Sleep(time.Millisecond)
			count--
			if count > 0 {
				panic(fmt.Errorf("count should be zero but got %d", count))
			}
			if st := m.Setlk(ctx, inode, uint64(i), false, syscall.F_UNLCK, 0, 0xFFFF, uint32(i)); st != 0 {
				panic(fmt.Errorf("plock unlock: %s", st))
			}
		}(i)
	}
	g.Wait()
	if err != 0 {
		t.Fatalf("lock fail: %s", err)
	}

	if r, ok := m.(*redisMeta); ok {
		ms, err := r.rdb.SMembers(context.Background(), r.lockedKey(r.sid)).Result()
		if err != nil {
			t.Fatalf("Smember %s: %s", r.lockedKey(r.sid), err)
		}
		if len(ms) != 0 {
			t.Fatalf("locked inode leaked: %d", len(ms))
		}
	}
}

func testResolve(t *testing.T, m Meta) {
	var inode, parent Ino
	var attr, pattr Attr
	if st := m.Mkdir(NewContext(1, 65534, []uint32{65534}), 1, "d", 0770, 0, 0, &parent, &pattr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if pattr.Gid != 65534 {
		pattr.Gid = 65534
		if st := m.SetAttr(NewContext(1, 65534, []uint32{65534}), parent, SetAttrGID, 0, &pattr); st != 0 {
			t.Fatalf("setattr gid: %s", st)
		}
	}

	if pattr.Uid != 65534 || pattr.Gid != 65534 {
		t.Fatalf("attr %+v", pattr)
	}
	if st := m.Create(NewContext(1, 65534, []uint32{65534}), parent, "f", 0644, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("create /d/f: %s", st)
	}

	defer func() {
		if st := m.Remove(NewContext(0, 65534, []uint32{65534}), parent, "f", false, RmrDefaultThreads, nil); st != 0 {
			t.Fatalf("remove /d/f by owner: %s", st)
		}
		if st := m.Rmdir(NewContext(0, 65534, []uint32{65534}), 1, "d"); st != 0 {
			t.Fatalf("rmdir /d by owner: %s", st)
		}
	}()

	if st := m.Resolve(NewContext(0, 65534, []uint32{65534}), 1, "/d/f", &inode, &attr); st != 0 {
		if st == syscall.ENOTSUP {
			return
		}
		t.Fatalf("resolve /d/f by owner: %s", st)
	}
	if st := m.Resolve(NewContext(0, 65533, []uint32{65534}), 1, "/d/f", &inode, &attr); st != 0 {
		t.Fatalf("resolve /d/f by group: %s", st)
	}
	if st := m.Resolve(NewContext(0, 65533, []uint32{65533, 65534}), 1, "/d/f", &inode, &attr); st != 0 {
		t.Fatalf("resolve /d/f by multi-group: %s", st)
	}
	if st := m.Resolve(NewContext(0, 65533, []uint32{65533}), 1, "/d/f", &inode, &attr); st != syscall.EACCES {
		t.Fatalf("resolve /d/f by non-group: %s", st)
	}
}

func testRemove(t *testing.T, m Meta) {
	ctx := Background()
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Remove(ctx, 1, "f", false, RmrDefaultThreads, nil); st != 0 {
		t.Fatalf("rmr f: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0755, 0, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d2", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/d2: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0644, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/f: %s", st)
	}
	if ps := m.GetPaths(ctx, parent); len(ps) == 0 || ps[0] != "/d" {
		t.Fatalf("get path /d: %v", ps)
	}
	if ps := m.GetPaths(ctx, inode); len(ps) == 0 || ps[0] != "/d/f" {
		t.Fatalf("get path /d/f: %v", ps)
	}
	for i := 0; i < 4096; i++ {
		if st := m.Create(ctx, 1, "f"+strconv.Itoa(i), 0644, 0, 0, &inode, attr); st != 0 {
			t.Fatalf("create f%s: %s", strconv.Itoa(i), st)
		}
	}
	var entries []*Entry
	if st := m.Readdir(ctx, 1, 1, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 4099 {
		t.Fatalf("entries: %d", len(entries))
	}
	if st := m.Remove(ctx, 1, "d", false, RmrDefaultThreads, nil); st != 0 {
		t.Fatalf("rmr d: %s", st)
	}
}

func testCaseIncensi(t *testing.T, m Meta) {
	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	_ = m.Create(ctx, 1, "foo", 0755, 0, 0, &inode, attr)
	if st := m.Create(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create Foo should be ok")
	}
	if st := m.Create(ctx, 1, "Foo", 0755, 0, syscall.O_EXCL, &inode, attr); st != syscall.EEXIST {
		t.Fatalf("create should fail with EEXIST")
	}
	if st := m.Lookup(ctx, 1, "Foo", &inode, attr, true); st != 0 {
		t.Fatalf("lookup Foo should be OK")
	}
	if st := m.Rename(ctx, 1, "Foo", 1, "bar", 0, &inode, attr); st != 0 {
		t.Fatalf("rename Foo to bar should be OK, but got %s", st)
	}
	if st := m.Create(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("create Foo should be OK")
	}
	if st := m.Resolve(ctx, 1, "/Foo", &inode, attr); st != syscall.ENOTSUP {
		t.Fatalf("resolve with case insensitive should be ENOTSUP")
	}
	if st := m.Lookup(ctx, 1, "Bar", &inode, attr, true); st != 0 {
		t.Fatalf("lookup Bar should be OK")
	}
	if st := m.Link(ctx, inode, 1, "foo", attr); st != syscall.EEXIST {
		t.Fatalf("link should fail with EEXIST")
	}
	if st := m.Unlink(ctx, 1, "Bar"); st != 0 {
		t.Fatalf("unlink Bar should be OK")
	}
	if st := m.Unlink(ctx, 1, "foo"); st != 0 {
		t.Fatalf("unlink foo should be OK")
	}
	if st := m.Mkdir(ctx, 1, "Foo", 0755, 0, 0, &inode, attr); st != 0 {
		t.Fatalf("mkdir Foo should be OK, but got %s", st)
	}
	if st := m.Rmdir(ctx, 1, "foo"); st != 0 {
		t.Fatalf("rmdir foo should be OK")
	}
}

type compactor interface {
	compactChunk(inode Ino, indx uint32, once, force bool)
}

func testCompaction(t *testing.T, m Meta, trash bool) {
	if trash {
		format := testFormat()
		format.TrashDays = 1
		_ = m.Init(format, false)
		defer func() {
			if err := m.Init(testFormat(), false); err != nil {
				t.Fatalf("init: %v", err)
			}
		}()
	} else {
		_ = m.Init(testFormat(), false)
	}

	if err := m.NewSession(false); err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer m.CloseSession()
	var l sync.Mutex
	deleted := make(map[uint64]int)
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		l.Lock()
		sliceId := args[0].(uint64)
		deleted[sliceId] = 1
		l.Unlock()
		return nil
	})
	m.OnMsg(CompactChunk, func(args ...interface{}) error {
		return nil
	})
	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer func() {
		_ = m.Unlink(ctx, 1, "f")
	}()

	// random write
	var sliceId uint64
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 1, uint32(0), Slice{Id: sliceId, Size: 64 << 20, Len: 64 << 20}, time.Now())
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 1, uint32(30<<20), Slice{Id: sliceId, Size: 8, Len: 8}, time.Now())
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 1, uint32(40<<20), Slice{Id: sliceId, Size: 8, Len: 8}, time.Now())
	var cs1 []Slice
	_ = m.Read(ctx, inode, 1, &cs1)
	if len(cs1) != 5 {
		t.Fatalf("expect 5 slices, but got %+v", cs1)
	}
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 1, false, true)
	}
	var cs []Slice
	_ = m.Read(ctx, inode, 1, &cs)
	if len(cs) != 1 {
		t.Fatalf("expect 1 slice, but got %+v", cs)
	}

	// append
	var size uint32 = 100000
	for i := 0; i < 200; i++ {
		var sliceId uint64
		m.NewSlice(ctx, &sliceId)
		if st := m.Write(ctx, inode, 0, uint32(i)*size, Slice{Id: sliceId, Size: size, Len: size}, time.Now()); st != 0 {
			t.Fatalf("write %d: %s", i, st)
		}
		time.Sleep(time.Millisecond)
	}
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 0, false, true)
	}
	var slices []Slice
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(slices) >= 10 {
		t.Fatalf("inode %d should be compacted, but have %d slices", inode, len(slices))
	}
	var total uint32
	for _, s := range slices {
		total += s.Len
	}
	if total != size*200 {
		t.Fatalf("size of slice should be %d, but got %d", size*200, total)
	}

	// TODO: check result if that's predictable
	p, bar := utils.MockProgress()
	if st := m.CompactAll(ctx, 8, bar); st != 0 {
		t.Fatalf("compactall: %s", st)
	}
	p.Done()
	sliceMap := make(map[Ino][]Slice)
	if st := m.ListSlices(ctx, sliceMap, false, false, nil); st != 0 {
		t.Fatalf("list all slices: %s", st)
	}

	if trash {
		l.Lock()
		deletes := len(deleted)
		l.Unlock()
		if deletes > 10 {
			t.Fatalf("deleted slices %d is greater than 10", deletes)
		}
		if len(sliceMap[1]) < 200 {
			t.Fatalf("list delayed slices %d is less than 200", len(sliceMap[1]))
		}
		m.(engine).doCleanupDelayedSlices(ctx, time.Now().Unix()+1)
	}
	m.getBase().stopDeleteSliceTasks()
	l.Lock()
	deletes := len(deleted)
	l.Unlock()
	if deletes < 200 {
		t.Fatalf("deleted slices %d is less than 200", deletes)
	}
	m.getBase().startDeleteSliceTasks()

	// truncate to 0
	if st := m.Truncate(ctx, inode, 0, 0, attr, false); st != 0 {
		t.Fatalf("truncate file: %s", st)
	}
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 0, false, true)
	}
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(slices) != 1 || slices[0].Len != 1 {
		t.Fatalf("inode %d should be compacted, but have %d slices, size %d", inode, len(slices), slices[0].Len)
	}

	if st := m.Truncate(ctx, inode, 0, 64<<10, attr, false); st != 0 {
		t.Fatalf("truncate file: %s", st)
	}
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 0, uint32(1<<20), Slice{Id: sliceId, Size: 2 << 20, Len: 2 << 20}, time.Now())
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 0, false, true)
	}
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(slices) != 2 || slices[0].Id != 0 || slices[1].Len != 2<<20 {
		t.Fatalf("inode %d should be compacted, but have %d slices, id %d size %d",
			inode, len(slices), slices[0].Id, slices[1].Len)
	}

	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 0, uint32(512<<10), Slice{Id: sliceId, Size: 2 << 20, Len: 64 << 10}, time.Now())
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 0, uint32(0), Slice{Id: sliceId, Size: 1 << 20, Len: 64 << 10}, time.Now())
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 0, uint32(128<<10), Slice{Id: sliceId, Size: 2 << 20, Len: 128 << 10}, time.Now())
	_ = m.Write(ctx, inode, 0, uint32(0), Slice{Id: 0, Size: 1 << 20, Len: 1 << 20}, time.Now())
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 0, false, true)
	}
	if st := m.Read(ctx, inode, 0, &slices); st != 0 {
		t.Fatalf("read 0: %s", st)
	}
	if len(slices) != 1 || slices[0].Len != 3<<20 {
		t.Fatalf("inode %d should be compacted, but have %d slices, size %d", inode, len(slices), slices[0].Len)
	}

	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 2, 0, Slice{Id: sliceId, Size: 2338508, Len: 2338508}, time.Now())
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 2, 8829056, Slice{Id: sliceId, Size: 1074933, Len: 1074933}, time.Now())
	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 2, 7663608, Slice{Id: sliceId, Size: 41480, Len: 4148}, time.Now())
	_ = m.Fallocate(ctx, inode, fallocZeroRange, 2*ChunkSize+4515328, 3152428, nil)
	_ = m.Fallocate(ctx, inode, fallocZeroRange, 2*ChunkSize+4515328, 2607724, nil)
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 2, false, true)
	}
	if st := m.Read(ctx, inode, 2, &slices); st != 0 {
		t.Fatalf("read 1: %s", st)
	}
	// compact twice: 4515328+2607724-2338508 = 4784544; 8829056+1074933-2338508-4784544=2780937
	if len(slices) != 3 || slices[0].Len != 2338508 || slices[1].Len != 4784544 || slices[2].Len != 2780937 {
		t.Fatalf("inode %d should be compacted, but have %d slices, size %d,%d,%d",
			inode, len(slices), slices[0].Len, slices[1].Len, slices[2].Len)
	}

	m.NewSlice(ctx, &sliceId)
	_ = m.Write(ctx, inode, 3, 0, Slice{Id: sliceId, Size: 2338508, Len: 2338508}, time.Now())
	_ = m.CopyFileRange(ctx, inode, 3*ChunkSize, inode, 4*ChunkSize, 2338508, 0, nil, nil)
	_ = m.Fallocate(ctx, inode, fallocZeroRange, 4*ChunkSize, ChunkSize, nil)
	_ = m.CopyFileRange(ctx, inode, 3*ChunkSize, inode, 4*ChunkSize, 2338508, 0, nil, nil)
	if c, ok := m.(compactor); ok {
		c.compactChunk(inode, 4, false, true)
	}
	if st := m.Read(ctx, inode, 4, &slices); st != 0 {
		t.Fatalf("read inode %d chunk 4: %s", inode, st)
	}
	if len(slices) != 1 || slices[0].Len != 2338508 {
		t.Fatalf("inode %d should be compacted, but have %d slices, size %d", inode, len(slices), slices[0].Len)
	}
}

func testConcurrentWrite(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		return nil
	})
	m.OnMsg(CompactChunk, func(args ...interface{}) error {
		return nil
	})

	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "f")
	if st := m.Create(ctx, 1, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "f")

	var errno syscall.Errno
	var g sync.WaitGroup
	for i := 0; i <= 10; i++ {
		g.Add(1)
		go func(indx uint32) {
			defer g.Done()
			for j := 0; j < 100; j++ {
				var sliceId uint64
				m.NewSlice(ctx, &sliceId)
				var slice = Slice{Id: sliceId, Size: 100, Len: 100}
				st := m.Write(ctx, inode, indx, 0, slice, time.Now())
				if st != 0 {
					errno = st
					break
				}
			}
		}(uint32(i))
	}
	g.Wait()
	if errno != 0 {
		t.Fatal()
	}

	var g2 sync.WaitGroup
	for i := 0; i <= 10; i++ {
		g2.Add(1)
		go func() {
			defer g2.Done()
			for j := 0; j < 1000; j++ {
				var sliceId uint64
				m.NewSlice(ctx, &sliceId)
				var slice = Slice{Id: sliceId, Size: 100, Len: 100}
				st := m.Write(ctx, inode, 0, uint32(200*j), slice, time.Now())
				if st != 0 {
					errno = st
					break
				}
			}
		}()
	}
	g2.Wait()
	if errno != 0 {
		t.Fatal()
	}
}

func testTruncateAndDelete(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		return nil
	})
	// remove quota
	format, _ := m.Load(false)
	format.Capacity = 0
	_ = m.Init(format, false)

	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	m.Unlink(ctx, 1, "f")
	if st := m.Truncate(ctx, 1, 0, 4<<10, attr, false); st != syscall.EPERM {
		t.Fatalf("truncate dir %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0650, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	var sliceId uint64
	if st := m.NewSlice(ctx, &sliceId); st != 0 {
		t.Fatalf("new chunk: %s", st)
	}
	if st := m.Write(ctx, inode, 0, 100, Slice{sliceId, 100, 0, 100}, time.Now()); st != 0 {
		t.Fatalf("write file %s", st)
	}
	if st := m.Truncate(ctx, inode, 0, 200<<20, attr, false); st != 0 {
		t.Fatalf("truncate file %s", st)
	}
	if st := m.Truncate(ctx, inode, 0, (10<<40)+10, attr, false); st != 0 {
		t.Fatalf("truncate file %s", st)
	}
	if st := m.Truncate(ctx, inode, 0, (300<<20)+10, attr, false); st != 0 {
		t.Fatalf("truncate file %s", st)
	}
	var total int64
	slices := make(map[Ino][]Slice)
	m.ListSlices(ctx, slices, false, false, func() { total++ })
	var totalSlices int
	for _, ss := range slices {
		totalSlices += len(ss)
	}
	if totalSlices != 1 {
		t.Fatalf("number of slices: %d != 1, %+v", totalSlices, slices)
	}
	_ = m.Close(ctx, inode)
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink file %s", st)
	}

	time.Sleep(time.Millisecond * 100)
	slices = make(map[Ino][]Slice)
	m.ListSlices(ctx, slices, false, false, nil)
	totalSlices = 0
	for _, ss := range slices {
		totalSlices += len(ss)
	}
	// the last chunk could be found and deleted
	if totalSlices > 1 {
		t.Fatalf("number of slices: %d > 1, %+v", totalSlices, slices)
	}
}

func testCopyFileRange(t *testing.T, m Meta) {
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		return nil
	})

	ctx := Background()
	var iin, iout Ino
	var attr = &Attr{}
	_ = m.Unlink(ctx, 1, "fin")
	_ = m.Unlink(ctx, 1, "fout")
	if st := m.Create(ctx, 1, "fin", 0650, 022, 0, &iin, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "fin")
	if st := m.Create(ctx, 1, "fout", 0650, 022, 0, &iout, attr); st != 0 {
		t.Fatalf("create file %s", st)
	}
	defer m.Unlink(ctx, 1, "fout")
	m.Write(ctx, iin, 0, 100, Slice{10, 200, 0, 100}, time.Now())
	m.Write(ctx, iin, 1, 100<<10, Slice{11, 40 << 20, 0, 40 << 20}, time.Now())
	m.Write(ctx, iin, 3, 0, Slice{12, 63 << 20, 10 << 20, 30 << 20}, time.Now())
	m.Write(ctx, iout, 2, 10<<20, Slice{13, 50 << 20, 10 << 20, 30 << 20}, time.Now())
	var copied uint64
	if st := m.CopyFileRange(ctx, iin, 150, iout, 30<<20, 200<<20, 0, &copied, nil); st != 0 {
		t.Fatalf("copy file range: %s", st)
	}
	var expected uint64 = 200 << 20
	if copied != expected {
		t.Fatalf("expect copy %d bytes, but got %d", expected, copied)
	}
	var expectedSlices = [][]Slice{
		{{0, 30 << 20, 0, 30 << 20}, {10, 200, 50, 50}, {0, 0, 200, ChunkSize - 30<<20 - 50}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {0, 0, 0, 100 << 10}, {11, 40 << 20, 0, (34 << 20) + 150 - (100 << 10)}},
		{{11, 40 << 20, (34 << 20) + 150 - (100 << 10), 6<<20 - 150 + 100<<10}, {0, 0, 40<<20 + 100<<10, ChunkSize - 40<<20 - 100<<10}, {0, 0, 0, 150 + (ChunkSize - 30<<20)}},
		{{0, 0, 150 + (ChunkSize - 30<<20), 30<<20 - 150}, {12, 63 << 20, 10 << 20, (8 << 20) + 150}},
	}
	for i := uint32(0); i < 4; i++ {
		var slices []Slice
		if st := m.Read(ctx, iout, i, &slices); st != 0 {
			t.Fatalf("read chunk %d: %s", i, st)
		}
		if len(slices) != len(expectedSlices[i]) {
			t.Fatalf("expect chunk %d: %+v, but got %+v", i, expectedSlices[i], slices)
		}
		for j, s := range slices {
			if s != expectedSlices[i][j] {
				t.Fatalf("expect slice %d,%d: %+v, but got %+v", i, j, expectedSlices[i][j], s)
			}
		}
	}
}

func testCloseSession(t *testing.T, m Meta) {
	// reset session
	m.getBase().sid = 0
	if err := m.NewSession(true); err != nil {
		t.Fatalf("new session: %s", err)
	}

	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Flock(ctx, inode, 1, syscall.F_WRLCK, false); st != 0 {
		t.Fatalf("flock wlock: %s", st)
	}
	if st := m.Setlk(ctx, inode, 1, false, syscall.F_WRLCK, 0x10000, 0x20000, 1); st != 0 {
		t.Fatalf("plock wlock: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	time.Sleep(10 * time.Millisecond)
	sid := m.getBase().sid
	s, err := m.GetSession(sid, true)
	if err != nil {
		t.Fatalf("get session: %s", err)
	} else {
		if len(s.Flocks) != 1 || len(s.Plocks) != 1 || len(s.Sustained) != 1 {
			t.Fatalf("incorrect session: flock %d plock %d sustained %d", len(s.Flocks), len(s.Plocks), len(s.Sustained))
		}
	}
	if err = m.CloseSession(); err != nil {
		t.Fatalf("close session: %s", err)
	}
	if _, err = m.GetSession(sid, true); err == nil {
		t.Fatalf("get a deleted session: %s", err)
	}
	switch m := m.(type) {
	case *redisMeta:
		s, err = m.getSession(strconv.FormatUint(sid, 10), true)
	case *dbMeta:
		s, err = m.getSession(&session2{Sid: sid, Info: []byte("{}")}, true)
	case *kvMeta:
		s, err = m.getSession(sid, true)
	}
	if err != nil {
		t.Fatalf("get session: %s", err)
	}
	if s.SessionInfo.Version != "" || s.SessionInfo.HostName != "" || s.SessionInfo.IPAddrs != nil ||
		s.SessionInfo.MountPoint != "" || s.SessionInfo.ProcessID != 0 {
		t.Fatalf("incorrect session info %+v", s.SessionInfo)
	}
	if len(s.Flocks) != 0 || len(s.Plocks) != 0 || len(s.Sustained) != 0 {
		t.Fatalf("incorrect session: flock %d plock %d sustained %d", len(s.Flocks), len(s.Plocks), len(s.Sustained))
	}
}

func testTrash(t *testing.T, m Meta) {
	format := testFormat()
	format.TrashDays = 1
	if err := m.Init(format, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() {
		if err := m.Init(testFormat(), false); err != nil {
			t.Fatalf("init: %v", err)
		}
	}()
	ctx := Background()
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f1", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f1: %s", st)
	}
	if st := m.Create(ctx, 1, "f2", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f2: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Create(ctx, parent, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create d/f: %s", st)
	}
	if st := m.Rename(ctx, 1, "f1", 1, "d", 0, &inode, attr); st != syscall.ENOTEMPTY {
		t.Fatalf("rename f1 -> d: %s", st)
	}
	if st := m.Unlink(ctx, parent, "f"); st != 0 {
		t.Fatalf("unlink d/f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("getattr f(%d): %s, attr %+v", inode, st, attr)
	}
	if st := m.Truncate(ctx, inode, 0, 1<<30, attr, false); st != syscall.EPERM {
		t.Fatalf("should not truncate a file in trash")
	}
	if st := m.Open(ctx, inode, uint32(syscall.O_RDWR), attr); st != syscall.EPERM {
		t.Fatalf("should not fallocate a file in trash")
	}
	if st := m.SetAttr(ctx, inode, SetAttrMode, 1, &Attr{Mode: 0}); st != syscall.EPERM {
		t.Fatalf("should not change mode of a file in trash")
	}
	var parent2 Ino
	if st := m.Mkdir(ctx, 1, "d2", 0755, 022, 0, &parent2, attr); st != 0 {
		t.Fatalf("mkdir d2: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d2"); st != 0 {
		t.Fatalf("rmdir d2: %s", st)
	}
	if st := m.GetAttr(ctx, parent2, attr); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("getattr d2(%d): %s, attr %+v", parent2, st, attr)
	}
	var tino Ino
	if st := m.Mkdir(ctx, parent2, "d3", 0777, 022, 0, &tino, attr); st != syscall.ENOENT {
		t.Fatalf("mkdir inside trash should fail")
	}
	if st := m.Create(ctx, parent2, "d3", 0755, 022, 0, &tino, attr); st != syscall.ENOENT {
		t.Fatalf("create inside trash should fail")
	}
	if st := m.Link(ctx, inode, parent2, "ttlink", attr); st != syscall.ENOENT {
		t.Fatalf("link inside trash should fail")
	}
	if st := m.Rename(ctx, 1, "d", parent2, "ttlink", 0, &tino, attr); st != syscall.ENOENT {
		t.Fatalf("link inside trash should fail")
	}
	if st := m.Rename(ctx, 1, "f1", 1, "d", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f1 -> d: %s", st)
	}
	if st := m.Lookup(ctx, TrashInode+1, fmt.Sprintf("1-%d-d", parent), &inode, attr, true); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("lookup subTrash/d: %s, attr %+v", st, attr)
	}
	if st := m.Rename(ctx, 1, "f2", TrashInode, "td", 0, &inode, attr); st != syscall.EPERM {
		t.Fatalf("rename f2 -> td: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", TrashInode+1, "td", 0, &inode, attr); st != syscall.EPERM {
		t.Fatalf("rename f2 -> td: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "d", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> d: %s", st)
	}
	if st := m.Link(ctx, inode, 1, "l", attr); st != 0 || attr.Nlink != 2 {
		t.Fatalf("link d -> l1: %s", st)
	}
	if st := m.Unlink(ctx, 1, "l"); st != 0 {
		t.Fatalf("unlink l: %s", st)
	}
	// hardlink goes to the trash
	if st := m.GetAttr(ctx, inode, attr); st != 0 || attr.Nlink != 2 {
		t.Fatalf("getattr d(%d): %s, attr %+v", inode, st, attr)
	}
	if st := m.Link(ctx, inode, 1, "l", attr); st != 0 || attr.Nlink != 3 {
		t.Fatalf("link d -> l1: %s", st)
	}
	if st := m.Unlink(ctx, 1, "l"); st != 0 {
		t.Fatalf("unlink l: %s", st)
	}
	// hardlink is deleted directly
	if st := m.GetAttr(ctx, inode, attr); st != 0 || attr.Nlink != 2 {
		t.Fatalf("getattr d(%d): %s, attr %+v", inode, st, attr)
	}
	if st := m.Unlink(ctx, 1, "d"); st != 0 {
		t.Fatalf("unlink d: %s", st)
	}
	lname := strings.Repeat("f", MaxName)
	if st := m.Create(ctx, 1, lname, 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create %s: %s", lname, st)
	}
	if st := m.Unlink(ctx, 1, lname); st != 0 {
		t.Fatalf("unlink %s: %s", lname, st)
	}
	tname := fmt.Sprintf("1-%d-%s", inode, lname)[:MaxName]
	if st := m.Lookup(ctx, TrashInode+1, tname, &inode, attr, true); st != 0 || attr.Parent != TrashInode+1 {
		t.Fatalf("lookup subTrash/%s: %s, attr %+v", tname, st, attr)
	}
	var entries []*Entry
	if st := m.Readdir(ctx, 1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %d", len(entries))
	}
	entries = entries[:0]
	if st := m.Readdir(ctx, TrashInode+1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	}
	if len(entries) != 9 {
		t.Fatalf("entries: %d", len(entries))
	}
	// test Remove with skipTrash true/false
	if st := m.Mkdir(ctx, 1, "d10", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d10: %s", st)
	}
	if st := m.Create(ctx, parent, "f10", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create d10/f10: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d10", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d10/d10: %s", st)
	}
	if st := m.Remove(ctx, 1, "d10", false, RmrDefaultThreads, nil); st != 0 {
		t.Fatalf("rmr d10: %s", st)
	}
	entries = entries[:0]
	if st := m.Readdir(ctx, TrashInode+1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	}
	if len(entries) != 12 {
		t.Fatalf("entries: %d", len(entries))
	}
	if st := m.Mkdir(ctx, 1, "d10", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d10: %s", st)
	}
	if st := m.Create(ctx, parent, "f10", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create d10/f10: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d10", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d10/d10: %s", st)
	}
	if st := m.Remove(ctx, 1, "d10", true, RmrDefaultThreads, nil); st != 0 {
		t.Fatalf("rmr d10: %s", st)
	}
	entries = entries[:0]
	if st := m.Readdir(ctx, TrashInode+1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	}
	if len(entries) != 12 {
		t.Fatalf("entries: %d", len(entries))
	}

	ctx2 := NewContext(1000, 1, []uint32{1})
	if st := m.Unlink(ctx2, TrashInode+1, "d"); st != syscall.EPERM {
		t.Fatalf("unlink d: %s", st)
	}
	if st := m.Rmdir(ctx2, TrashInode+1, "d"); st != syscall.EPERM {
		t.Fatalf("rmdir d: %s", st)
	}
	if st := m.Rename(ctx2, TrashInode+1, "d", 1, "f", 0, &inode, attr); st != syscall.EPERM {
		t.Fatalf("rename d -> f: %s", st)
	}
	m.getBase().doCleanupTrash(Background(), format.TrashDays, true)
	if st := m.GetAttr(ctx2, TrashInode+1, attr); st != syscall.ENOENT {
		t.Fatalf("getattr: %s", st)
	}
}

func testParents(t *testing.T, m Meta) {
	ctx := Background()
	var inode, parent Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if attr.Parent != 1 {
		t.Fatalf("expect parent 1, but got %d", attr.Parent)
	}
	checkParents := func(inode Ino, expect map[Ino]int) {
		if ps := m.GetParents(ctx, inode); ps == nil {
			t.Fatalf("get parents of inode %d returns nil", inode)
		} else if !reflect.DeepEqual(ps, expect) {
			t.Fatalf("expect parents %v, but got %v", expect, ps)
		}
	}
	checkParents(inode, map[Ino]int{1: 1})

	if st := m.Link(ctx, inode, 1, "l1", attr); st != 0 {
		t.Fatalf("link l1 -> f: %s", st)
	}
	if attr.Parent != 0 {
		t.Fatalf("expect parent 0, but got %d", attr.Parent)
	}
	checkParents(inode, map[Ino]int{1: 2})

	if st := m.Mkdir(ctx, 1, "d", 0755, 022, 0, &parent, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Link(ctx, inode, parent, "l2", attr); st != 0 {
		t.Fatalf("link l2 -> f: %s", st)
	}
	if st := m.Link(ctx, inode, parent, "l3", attr); st != 0 {
		t.Fatalf("link l3 -> f: %s", st)
	}
	checkParents(inode, map[Ino]int{1: 2, parent: 2})

	if st := m.Unlink(ctx, 1, "f"); st != 0 {
		t.Fatalf("unlink f: %s", st)
	}
	if st := m.Create(ctx, 1, "f2", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f2: %s", st)
	}
	if st := m.Rename(ctx, 1, "f2", 1, "l1", 0, &inode, attr); st != 0 {
		t.Fatalf("rename f2 -> l1: %s", st)
	}
	if st := m.Lookup(ctx, parent, "l2", &inode, attr, true); st != 0 {
		t.Fatalf("lookup d/l2: %s", st)
	}
	if attr.Parent != 0 {
		t.Fatalf("expect parent 0, but got %d", attr.Parent)
	}
	if st := m.Unlink(ctx, parent, "l2"); st != 0 {
		t.Fatalf("unlink d/l2: %s", st)
	}
	checkParents(inode, map[Ino]int{parent: 1})

	// clean up
	if st := m.Unlink(ctx, 1, "l1"); st != 0 {
		t.Fatalf("unlink l1: %s", st)
	}
	if st := m.Unlink(ctx, parent, "l3"); st != 0 {
		t.Fatalf("unlink d/l3: %s", st)
	}
	if st := m.Rmdir(ctx, 1, "d"); st != 0 {
		t.Fatalf("rmdir d: %s", st)
	}
}

func testOpenCache(t *testing.T, m Meta) {
	ctx := Background()
	var inode Ino
	var attr = &Attr{}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	defer m.Unlink(ctx, 1, "f")
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	defer m.Close(ctx, inode)

	var attr2 = &Attr{}
	if st := m.GetAttr(ctx, inode, attr2); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if *attr != *attr2 {
		t.Fatalf("attrs not the same: attr %+v; attr2 %+v", *attr, *attr2)
	}
	attr2.Uid = 1
	if st := m.SetAttr(ctx, inode, SetAttrUID, 0, attr2); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.GetAttr(ctx, inode, attr); st != 0 {
		t.Fatalf("getattr f: %s", st)
	}
	if attr.Uid != 1 {
		t.Fatalf("attr uid should be 1: %+v", *attr)
	}
}

func testReadOnly(t *testing.T, m Meta) {
	ctx := Background()
	if err := m.NewSession(true); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()

	var inode Ino
	var attr = &Attr{}
	if st := m.GetAttr(ctx, 1, attr); st != 0 {
		t.Fatalf("getattr 1: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &inode, attr); st != syscall.EROFS {
		t.Fatalf("mkdir d: %s", st)
	}
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, attr); st != syscall.EROFS {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_RDWR, attr); st != syscall.EROFS {
		t.Fatalf("open f: %s", st)
	}

	if plocks, flocks, err := m.ListLocks(ctx, 1); err != nil || len(plocks) != 0 || len(flocks) != 0 {
		t.Fatalf("list locks: %v %v %v", plocks, flocks, err)
	}
}

func testConcurrentDir(t *testing.T, m Meta) {
	ctx := Background()
	var g sync.WaitGroup
	var err error
	format, err := m.Load(false)
	format.Capacity = 0
	format.Inodes = 0
	if err = m.Init(format, false); err != nil {
		t.Fatalf("set quota failed: %s", err)
	}
	for i := 0; i < 100; i++ {
		g.Add(1)
		go func(i int) {
			defer g.Done()
			var d1, d2 Ino
			var attr = new(Attr)
			if st := m.Mkdir(ctx, 1, "d1", 0640, 022, 0, &d1, attr); st != 0 && st != syscall.EEXIST {
				panic(fmt.Errorf("mkdir d1: %s", st))
			} else if st == syscall.EEXIST {
				st = m.Lookup(ctx, 1, "d1", &d1, attr, true)
				if st != 0 {
					panic(fmt.Errorf("lookup d1: %s", st))
				}
			}
			if st := m.Mkdir(ctx, 1, "d2", 0640, 022, 0, &d2, attr); st != 0 && st != syscall.EEXIST {
				panic(fmt.Errorf("mkdir d2: %s", st))
			} else if st == syscall.EEXIST {
				st = m.Lookup(ctx, 1, "d2", &d2, attr, true)
				if st != 0 {
					panic(fmt.Errorf("lookup d2: %s", st))
				}
			}
			name := fmt.Sprintf("file%d", i)
			var f Ino
			if st := m.Create(ctx, d1, name, 0664, 0, 0, &f, attr); st != 0 {
				panic(fmt.Errorf("create d1/%s: %s", name, st))
			}
			if st := m.Rename(ctx, d1, name, d2, name, 0, &f, attr); st != 0 {
				panic(fmt.Errorf("rename d1/%s -> d2/%s: %s", name, name, st))
			}
		}(i)
	}
	g.Wait()
	if err != nil {
		t.Fatalf("concurrent dir: %s", err)
	}
	for i := 0; i < 100; i++ {
		g.Add(1)
		go func(i int) {
			defer g.Done()
			var d2 Ino
			var attr = new(Attr)
			st := m.Lookup(ctx, 1, "d2", &d2, attr, true)
			if st != 0 {
				panic(fmt.Errorf("lookup d2: %s", st))
			}
			name := fmt.Sprintf("file%d", i)
			if st := m.Unlink(ctx, d2, name); st != 0 {
				panic(fmt.Errorf("unlink d2/%s: %s", name, st))
			}
			if st := m.Rmdir(ctx, 1, "d1"); st != 0 && st != syscall.ENOTEMPTY && st != syscall.ENOENT {
				panic(fmt.Errorf("rmdir d1: %s", st))
			}
			if st := m.Rmdir(ctx, 1, "d2"); st != 0 && st != syscall.ENOTEMPTY && st != syscall.ENOENT {
				panic(fmt.Errorf("rmdir d2: %s", st))
			}
		}(i)
	}
	g.Wait()
}

func testAttrFlags(t *testing.T, m Meta) {
	ctx := Background()
	var attr = &Attr{}
	var inode Ino
	if st := m.Create(ctx, 1, "f", 0644, 022, 0, &inode, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, inode, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY, attr); st != syscall.EPERM {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY|syscall.O_APPEND, attr); st != 0 {
		t.Fatalf("open f: %s", st)
	}
	attr.Flags = FlagAppend | FlagImmutable
	if st := m.SetAttr(ctx, inode, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY, attr); st != syscall.EPERM {
		t.Fatalf("open f: %s", st)
	}
	if st := m.Open(ctx, inode, syscall.O_WRONLY|syscall.O_APPEND, attr); st != syscall.EPERM {
		t.Fatalf("open f: %s", st)
	}

	var d Ino
	if st := m.Mkdir(ctx, 1, "d", 0640, 022, 0, &d, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, d, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Create(ctx, d, "f", 0644, 022, 0, &inode, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Unlink(ctx, d, "f"); st != syscall.EPERM {
		t.Fatalf("unlink f: %s", st)
	}
	attr.Flags = FlagAppend | FlagImmutable
	if st := m.SetAttr(ctx, d, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Create(ctx, d, "f2", 0644, 022, 0, &inode, nil); st != syscall.EPERM {
		t.Fatalf("create f2: %s", st)
	}

	var Immutable Ino
	if st := m.Mkdir(ctx, 1, "ImmutFile", 0640, 022, 0, &Immutable, attr); st != 0 {
		t.Fatalf("mkdir d: %s", st)
	}
	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, Immutable, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Create(ctx, Immutable, "f2", 0644, 022, 0, &inode, nil); st != syscall.EPERM {
		t.Fatalf("create f2: %s", st)
	}

	var src1, dst1, mfile Ino
	attr.Flags = 0
	if st := m.Mkdir(ctx, 1, "src1", 0640, 022, 0, &src1, attr); st != 0 {
		t.Fatalf("mkdir src1: %s", st)
	}
	if st := m.Create(ctx, src1, "mfile", 0644, 022, 0, &mfile, nil); st != 0 {
		t.Fatalf("create mfile: %s", st)
	}
	if st := m.Mkdir(ctx, 1, "dst1", 0640, 022, 0, &dst1, attr); st != 0 {
		t.Fatalf("mkdir dst1: %s", st)
	}

	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, src1, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Rename(ctx, src1, "mfile", dst1, "mfile", 0, &mfile, attr); st != syscall.EPERM {
		t.Fatalf("rename d: %s", st)
	}

	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, src1, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Rename(ctx, src1, "mfile", dst1, "mfile", 0, &mfile, attr); st != syscall.EPERM {
		t.Fatalf("rename d: %s", st)
	}

	if st := m.SetAttr(ctx, dst1, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Rename(ctx, src1, "mfile", dst1, "mfile", 0, &mfile, attr); st != syscall.EPERM {
		t.Fatalf("rename d: %s", st)
	}

	var delFile Ino
	if st := m.Create(ctx, 1, "delfile", 0644, 022, 0, &delFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	attr.Flags = FlagImmutable | FlagAppend
	if st := m.SetAttr(ctx, delFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr d: %s", st)
	}
	if st := m.Unlink(ctx, 1, "delfile"); st != syscall.EPERM {
		t.Fatalf("unlink f: %s", st)
	}

	var fallocFile Ino
	if st := m.Create(ctx, 1, "fallocfile", 0644, 022, 0, &fallocFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, fallocFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Fallocate(ctx, fallocFile, fallocKeepSize, 0, 1024, nil); st != 0 {
		t.Fatalf("fallocate f: %s", st)
	}
	if st := m.Fallocate(ctx, fallocFile, fallocKeepSize|fallocZeroRange, 0, 1024, nil); st != syscall.EPERM {
		t.Fatalf("fallocate f: %s", st)
	}
	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, fallocFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.Fallocate(ctx, fallocFile, fallocKeepSize, 0, 1024, nil); st != syscall.EPERM {
		t.Fatalf("fallocate f: %s", st)
	}

	var copysrcFile, copydstFile Ino
	if st := m.Create(ctx, 1, "copysrcfile", 0644, 022, 0, &copysrcFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Create(ctx, 1, "copydstfile", 0644, 022, 0, &copydstFile, nil); st != 0 {
		t.Fatalf("create f: %s", st)
	}
	if st := m.Fallocate(ctx, copysrcFile, 0, 0, 1024, nil); st != 0 {
		t.Fatalf("fallocate f: %s", st)
	}
	attr.Flags = FlagAppend
	if st := m.SetAttr(ctx, copydstFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.CopyFileRange(ctx, copysrcFile, 0, copydstFile, 0, 1024, 0, nil, nil); st != syscall.EPERM {
		t.Fatalf("copy_file_range f: %s", st)
	}
	attr.Flags = FlagImmutable
	if st := m.SetAttr(ctx, copydstFile, SetAttrFlag, 0, attr); st != 0 {
		t.Fatalf("setattr f: %s", st)
	}
	if st := m.CopyFileRange(ctx, copysrcFile, 0, copydstFile, 0, 1024, 0, nil, nil); st != syscall.EPERM {
		t.Fatalf("copy_file_range f: %s", st)
	}
}

func setAttr(t *testing.T, m Meta, inode Ino, attr *Attr) {
	var err error
	switch m := m.(type) {
	case *redisMeta:
		err = m.txn(Background(), func(tx *redis.Tx) error {
			return tx.Set(Background(), m.inodeKey(inode), m.marshal(attr), 0).Err()
		}, m.inodeKey(inode))
	case *dbMeta:
		err = m.txn(func(s *xorm.Session) error {
			_, err = s.ID(inode).AllCols().Update(&node{
				Inode:     inode,
				Type:      attr.Typ,
				Flags:     attr.Flags,
				Mode:      attr.Mode,
				Uid:       attr.Uid,
				Gid:       attr.Gid,
				Atime:     attr.Atime*1e6 + int64(attr.Atimensec)/1e3,
				Mtime:     attr.Mtime*1e6 + int64(attr.Mtimensec)/1e3,
				Ctime:     attr.Ctime*1e6 + int64(attr.Ctimensec)/1e3,
				Atimensec: int16(attr.Atimensec % 1e3),
				Mtimensec: int16(attr.Mtimensec % 1e3),
				Ctimensec: int16(attr.Ctimensec % 1e3),

				Nlink:  attr.Nlink,
				Length: attr.Length,
				Rdev:   attr.Rdev,
				Parent: attr.Parent,
			})
			return err
		})
	case *kvMeta:
		err = m.txn(Background(), func(tx *kvTxn) error {
			tx.set(m.inodeKey(inode), m.marshal(attr))
			return nil
		})
	}
	if err != nil {
		t.Fatalf("setAttr: %v", err)
	}
}

func testCheckAndRepair(t *testing.T, m Meta) {
	var checkInode, d1Inode, d2Inode, d3Inode, d4Inode Ino
	dirAttr := &Attr{Mode: 0644, Full: true, Typ: TypeDirectory, Nlink: 3}
	if st := m.Mkdir(Background(), RootInode, "check", 0640, 022, 0, &checkInode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background(), checkInode, "d1", 0640, 022, 0, &d1Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background(), d1Inode, "d2", 0640, 022, 0, &d2Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background(), d2Inode, "d3", 0640, 022, 0, &d3Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if st := m.Mkdir(Background(), d3Inode, "d4", 0640, 022, 0, &d4Inode, dirAttr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}

	if st := m.GetAttr(Background(), checkInode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, checkInode, dirAttr)

	if st := m.GetAttr(Background(), d1Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, d1Inode, dirAttr)

	if st := m.GetAttr(Background(), d2Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, d2Inode, dirAttr)

	if st := m.GetAttr(Background(), d3Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Nlink = 0
	setAttr(t, m, d3Inode, dirAttr)

	if st := m.GetAttr(Background(), d4Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	dirAttr.Full = false
	dirAttr.Nlink = 0
	setAttr(t, m, d4Inode, dirAttr)

	if err := m.Check(Background(), "/check", false, false, false); err == nil {
		t.Fatal("check should fail")
	}
	if st := m.GetAttr(Background(), checkInode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 0 {
		t.Fatalf("checkInode nlink should is 0 now: %d", dirAttr.Nlink)
	}

	if err := m.Check(Background(), "/check", true, false, false); err != nil {
		t.Fatalf("check: %s", err)
	}
	if st := m.GetAttr(Background(), checkInode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 3 || dirAttr.Parent != RootInode {
		t.Fatalf("checkInode nlink should is 3 now: %d", dirAttr.Nlink)
	}

	if err := m.Check(Background(), "/check/d1/d2", true, false, false); err != nil {
		t.Fatalf("check: %s", err)
	}
	if st := m.GetAttr(Background(), d2Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 3 || dirAttr.Parent != d1Inode {
		t.Fatalf("d2Inode nlink should is 3 now: %d", dirAttr.Nlink)
	}
	if st := m.GetAttr(Background(), d1Inode, dirAttr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if dirAttr.Nlink != 0 || dirAttr.Parent != checkInode {
		t.Fatalf("d1Inode nlink should is 0 now: %d", dirAttr.Nlink)
	}

	if m.Name() != "etcd" {
		if err := m.Check(Background(), "/", true, true, false); err != nil {
			t.Fatalf("check: %s", err)
		}
		for _, ino := range []Ino{checkInode, d1Inode, d2Inode, d3Inode} {
			if st := m.GetAttr(Background(), ino, dirAttr); st != 0 {
				t.Fatalf("getattr: %s", st)
			}
			if !dirAttr.Full || dirAttr.Nlink != 3 {
				t.Fatalf("nlink should is 3 now: %d", dirAttr.Nlink)
			}
		}
		if st := m.GetAttr(Background(), d4Inode, dirAttr); st != 0 {
			t.Fatalf("getattr: %s", st)
		}
		if !dirAttr.Full || dirAttr.Nlink != 2 || dirAttr.Parent != d3Inode {
			t.Fatalf("d4Inode  attr: %+v", *dirAttr)
		}
	}
}

func testDirStat(t *testing.T, m Meta) {
	testDir := "testDirStat"
	var testInode Ino
	// test empty dir
	if st := m.Mkdir(Background(), RootInode, testDir, 0640, 022, 0, &testInode, nil); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	if err := m.NewSession(true); err != nil {
		t.Fatalf("new session: %s", err)
	}
	defer m.CloseSession()
	stat, st := m.GetDirStat(Background(), testInode)
	checkResult := func(length, space, inodes int64) {
		if st != 0 {
			t.Fatalf("get dir usage: %s", st)
		}
		expect := dirStat{length, space, inodes}
		if *stat != expect {
			t.Fatalf("test dir usage: expect %+v, but got %+v", expect, stat)
		}
	}
	checkResult(0, 0, 0)

	// test dir with file
	var fileInode Ino
	if st := m.Create(Background(), testInode, "file", 0640, 022, 0, &fileInode, nil); st != 0 {
		t.Fatalf("create: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(0, align4K(0), 1)

	// test dir with file and fallocate
	if st := m.Fallocate(Background(), fileInode, 0, 0, 4097, nil); st != 0 {
		t.Fatalf("fallocate: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(4097, align4K(4097), 1)

	// test dir with file and truncate
	if st := m.Truncate(Background(), fileInode, 0, 0, nil, false); st != 0 {
		t.Fatalf("truncate: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(0, align4K(0), 1)

	// test dir with file and write
	if st := m.Write(Background(), fileInode, 0, 0, Slice{Id: 1, Size: 1 << 20, Off: 0, Len: 4097}, time.Now()); st != 0 {
		t.Fatalf("write: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(4097, align4K(4097), 1)

	// test dir with file and link
	if st := m.Link(Background(), fileInode, testInode, "file2", nil); st != 0 {
		t.Fatalf("link: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(2*4097, 2*align4K(4097), 2)

	// test dir with subdir
	var subInode Ino
	if st := m.Mkdir(Background(), testInode, "sub", 0640, 022, 0, &subInode, nil); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(2*4097, align4K(0)+2*align4K(4097), 3)

	// test rename
	if st := m.Rename(Background(), testInode, "file2", subInode, "file", 0, nil, nil); st != 0 {
		t.Fatalf("rename: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(4097, align4K(0)+align4K(4097), 2)
	stat, st = m.GetDirStat(Background(), subInode)
	checkResult(4097, align4K(4097), 1)

	// test unlink
	if st := m.Unlink(Background(), testInode, "file"); st != 0 {
		t.Fatalf("unlink: %s", st)
	}
	if st := m.Unlink(Background(), subInode, "file"); st != 0 {
		t.Fatalf("unlink: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(0, align4K(0), 1)
	stat, st = m.GetDirStat(Background(), subInode)
	checkResult(0, 0, 0)

	// test rmdir
	if st := m.Rmdir(Background(), testInode, "sub"); st != 0 {
		t.Fatalf("rmdir: %s", st)
	}
	time.Sleep(500 * time.Millisecond)
	stat, st = m.GetDirStat(Background(), testInode)
	checkResult(0, 0, 0)
}

func testClone(t *testing.T, m Meta) {
	//$ tree cloneDir
	//.
	//├── dir
	//└── dir1
	//    ├── dir2
	//    │ ├── dir3
	//    │ │ └── file3
	//    │ ├── file2
	//    │ └── file2Hardlink
	//    ├── file1
	//    └── file1Symlink -> file1
	var cloneDir Ino
	if eno := m.Mkdir(Background(), RootInode, "cloneDir", 0777, 022, 0, &cloneDir, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir1 Ino
	if eno := m.Mkdir(Background(), cloneDir, "dir1", 0777, 022, 0, &dir1, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir Ino
	if eno := m.Mkdir(Background(), cloneDir, "dir", 0777, 022, 0, &dir, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir2 Ino
	if eno := m.Mkdir(Background(), dir1, "dir2", 0777, 022, 0, &dir2, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir3 Ino
	if eno := m.Mkdir(Background(), dir2, "dir3", 0777, 022, 0, &dir3, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var file1 Ino
	if eno := m.Mknod(Background(), dir1, "file1", TypeFile, 0777, 022, 0, "", &file1, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var sliceId uint64
	if st := m.NewSlice(Background(), &sliceId); st != 0 {
		t.Fatalf("new chunk: %s", st)
	}
	if st := m.Write(Background(), file1, 0, 0, Slice{sliceId, 67108864, 0, 67108864}, time.Now()); st != 0 {
		t.Fatalf("write file %s", st)
	}

	var file2 Ino
	if eno := m.Mknod(Background(), dir2, "file2", TypeFile, 0777, 022, 0, "", &file2, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var sliceId2 uint64
	if st := m.NewSlice(Background(), &sliceId2); st != 0 {
		t.Fatalf("new chunk: %s", st)
	}
	if st := m.Write(Background(), file2, 0, 0, Slice{sliceId2, 67108863, 0, 67108863}, time.Now()); st != 0 {
		t.Fatalf("write file %s", st)
	}
	var file3 Ino
	if eno := m.Mknod(Background(), dir3, "file3", TypeFile, 0777, 022, 0, "", &file3, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	if eno := m.Fallocate(Background(), file3, 0, 0, 67108864, nil); eno != 0 {
		t.Fatalf("fallocate: %s", eno)
	}

	if eno := m.SetXattr(Background(), file1, "name", []byte("juicefs"), XattrCreateOrReplace); eno != 0 {
		t.Fatalf("setxattr: %s", eno)
	}
	if eno := m.SetXattr(Background(), file1, "name2", []byte("juicefs2"), XattrCreateOrReplace); eno != 0 {
		t.Fatalf("setxattr: %s", eno)
	}

	if eno := m.SetXattr(Background(), dir1, "name", []byte("juicefs"), XattrCreateOrReplace); eno != 0 {
		t.Fatalf("setxattr: %s", eno)
	}
	if eno := m.SetXattr(Background(), dir1, "name2", []byte("juicefs2"), XattrCreateOrReplace); eno != 0 {
		t.Fatalf("setxattr: %s", eno)
	}

	var file1Symlink Ino
	if eno := m.Symlink(Background(), dir1, "file1Symlink", "file1", &file1Symlink, nil); eno != 0 {
		t.Fatalf("symlink: %s", eno)
	}
	if eno := m.Link(Background(), file2, dir2, "file2Hardlink", nil); eno != 0 {
		t.Fatalf("hardlink: %s", eno)
	}

	var attr Attr
	attr.Mtime = 1
	m.SetAttr(Background(), cloneDir, SetAttrMtime, 0, &attr)
	var totalspace, availspace, iused, iavail, space, iused2 uint64
	m.StatFS(Background(), RootInode, &totalspace, &availspace, &iused, &iavail)
	space = totalspace - availspace
	iused2 = iused

	cloneDstName := "cloneDir1"
	var count, total uint64
	var cmode uint8
	cmode |= CLONE_MODE_PRESERVE_ATTR
	if eno := m.Clone(Background(), cloneDir, dir1, cloneDir, cloneDstName, cmode, 022, &count, &total); eno != 0 {
		t.Fatalf("clone: %s", eno)
	}
	var entries1 []*Entry
	if eno := m.Readdir(Background(), cloneDir, 1, &entries1); eno != 0 {
		t.Fatalf("readdir: %s", eno)
	}

	if len(entries1) != 5 {
		t.Fatalf("clone dst dir not found or name not correct")
	}
	var idx int
	for i, ent := range entries1 {
		if string(ent.Name) == cloneDstName {
			idx = i
			break
		}
	}
	if idx == 0 {
		t.Fatalf("clone dst dir not found or name not correct")
	}
	cloneDstIno := entries1[idx].Inode
	cloneDstAttr := entries1[idx].Attr
	if cloneDstAttr.Mode != 0755 {
		t.Fatalf("mode should be 0755 %o", cloneDstAttr.Mode)
	}
	// check dst parent dir nlink
	var rootAttr Attr
	if eno := m.GetAttr(Background(), cloneDir, &rootAttr); eno != 0 {
		t.Fatalf("get rootAttr: %s", eno)
	}
	if rootAttr.Nlink != 5 {
		t.Fatalf("rootDir nlink not correct,nlink: %d", rootAttr.Nlink)
	}
	if rootAttr.Mtime == 1 {
		t.Fatalf("mtime of rootDir is not updated")
	}
	m.StatFS(Background(), cloneDir, &totalspace, &availspace, &iused, &iavail)
	if totalspace-availspace-space != 268451840 {
		time.Sleep(time.Second * 2)
		m.StatFS(Background(), cloneDir, &totalspace, &availspace, &iused, &iavail)
		if totalspace-availspace-space != 268451840 {
			t.Logf("warning: added space: %d", totalspace-availspace-space)
		}
	}
	if iused-iused2 != 8 {
		t.Fatalf("added inodes: %d", iused-iused2)
	}
	if eno := m.Clone(Background(), RootInode, dir1, cloneDir, "no_preserve", 0, 022, &count, &total); eno != 0 {
		t.Fatalf("clone: %s", eno)
	}
	var d2 Ino
	var noPreserveAttr = new(Attr)
	m.Lookup(Background(), cloneDir, "no_preserve", &d2, noPreserveAttr, true)
	var cloneSrcAttr = new(Attr)
	m.GetAttr(Background(), dir1, cloneSrcAttr)
	if noPreserveAttr.Mtimensec == cloneSrcAttr.Mtimensec {
		t.Fatalf("clone: should not preserve mtime")
	}
	if eno := m.Remove(Background(), cloneDir, "no_preserve", false, RmrDefaultThreads, nil); eno != 0 {
		t.Fatalf("Rmdir: %s", eno)
	}
	// check attr
	var removedItem []interface{}
	checkEntryTree(t, m, dir1, cloneDstIno, func(srcEntry, dstEntry *Entry, dstIno Ino) {
		checkEntry(t, m, srcEntry, dstEntry, dstIno)

		switch m := m.(type) {
		case *redisMeta:
			removedItem = append(removedItem, m.inodeKey(dstEntry.Inode), m.entryKey(dstEntry.Inode), m.xattrKey(dstEntry.Inode), m.symKey(dstEntry.Inode))
		case *dbMeta:
			removedItem = append(removedItem, &node{Inode: dstEntry.Inode}, &edge{Inode: dstEntry.Inode, Parent: dstEntry.Attr.Parent}, &xattr{Inode: dstEntry.Inode}, &symlink{Inode: dstEntry.Inode})
		case *kvMeta:
			removedItem = append(removedItem, m.inodeKey(dstEntry.Inode), m.entryKey(dstEntry.Attr.Parent, string(dstEntry.Name)), m.symKey(dstEntry.Inode))
		}
	})
	// check slice ref after clone
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		t.Fatalf("should not delete slice")
		return nil
	})
	if eno := m.Remove(Background(), cloneDir, "dir1", false, RmrDefaultThreads, nil); eno != 0 {
		t.Fatalf("Rmdir: %s", eno)
	}

	var sli1del, sli2del bool
	m.OnMsg(DeleteSlice, func(args ...interface{}) error {
		if args[0].(uint64) == sliceId {
			sli1del = true
		}
		if args[0].(uint64) == sliceId2 {
			sli2del = true
		}
		return nil
	})
	// check remove tree
	var dNode1, dNode2, dNode3, dNode4 Ino = 101, 102, 103, 104
	switch m := m.(type) {
	case *redisMeta:
		// del edge first
		if err := m.rdb.HDel(Background(), m.entryKey(cloneDstAttr.Parent), cloneDstName).Err(); err != nil {
			t.Fatalf("del edge error: %v", err)
		}
		// check remove tree
		if eno := m.doCleanupDetachedNode(Background(), cloneDstIno); eno != 0 {
			t.Fatalf("remove tree error rootInode: %v", cloneDstIno)
		}
		removedKeysStr := make([]string, len(removedItem))
		for i, key := range removedItem {
			removedKeysStr[i] = key.(string)
		}
		removedKeysStr = append(removedKeysStr, m.detachedNodes())
		if exists := m.rdb.Exists(Background(), removedKeysStr...).Val(); exists != 0 {
			t.Fatalf("has keys not removed: %v", removedItem)
		}
		// check detached node
		m.rdb.ZAdd(Background(), m.detachedNodes(), redis.Z{Member: dNode1.String(), Score: float64(time.Now().Add(-1 * time.Minute).Unix())}).Err()
		m.rdb.ZAdd(Background(), m.detachedNodes(), redis.Z{Member: dNode2.String(), Score: float64(time.Now().Add(-5 * time.Minute).Unix())}).Err()
		m.rdb.ZAdd(Background(), m.detachedNodes(), redis.Z{Member: dNode3.String(), Score: float64(time.Now().Add(-48 * time.Hour).Unix())}).Err()
		m.rdb.ZAdd(Background(), m.detachedNodes(), redis.Z{Member: dNode4.String(), Score: float64(time.Now().Add(-48 * time.Hour).Unix())}).Err()
	case *dbMeta:
		if n, err := m.db.Delete(&edge{Parent: cloneDstAttr.Parent, Name: []byte(cloneDstName)}); err != nil || n != 1 {
			t.Fatalf("del edge error: %v", err)
		}
		// check remove tree
		if eno := m.doCleanupDetachedNode(Background(), cloneDstIno); eno != 0 {
			t.Fatalf("remove tree error rootInode: %v", cloneDstIno)
		}
		removedItem = append(removedItem, &detachedNode{Inode: cloneDstIno})
		time.Sleep(1 * time.Second)
		if exists, err := m.db.Exist(removedItem...); err != nil || exists {
			t.Fatalf("has keys not removed: %v", removedItem)
		}
		m.txn(func(s *xorm.Session) error {
			return mustInsert(s,
				&detachedNode{Inode: dNode1, Added: time.Now().Add(-1 * time.Minute).Unix()},
				&detachedNode{Inode: dNode2, Added: time.Now().Add(-5 * time.Minute).Unix()},
				&detachedNode{Inode: dNode3, Added: time.Now().Add(-48 * time.Hour).Unix()},
				&detachedNode{Inode: dNode4, Added: time.Now().Add(-48 * time.Hour).Unix()},
			)
		})
	case *kvMeta:
		// del edge first
		if err := m.deleteKeys(m.entryKey(cloneDstAttr.Parent, cloneDstName)); err != nil {
			t.Fatalf("del edge error: %v", err)
		}
		// check remove tree
		if eno := m.doCleanupDetachedNode(Background(), cloneDstIno); eno != 0 {
			t.Fatalf("remove tree error rootInode: %v", cloneDstIno)
		}
		removedItem = append(removedItem, m.detachedKey(cloneDstIno))
		m.txn(Background(), func(tx *kvTxn) error {
			for _, key := range removedItem {
				if buf := tx.get(key.([]byte)); buf != nil {
					t.Fatalf("has keys not removed: %v", removedItem)
				}
			}
			tx.set(m.detachedKey(dNode1), m.packInt64(time.Now().Add(-1*time.Minute).Unix()))
			tx.set(m.detachedKey(dNode2), m.packInt64(time.Now().Add(-5*time.Minute).Unix()))
			tx.set(m.detachedKey(dNode3), m.packInt64(time.Now().Add(-48*time.Hour).Unix()))
			tx.set(m.detachedKey(dNode4), m.packInt64(time.Now().Add(-48*time.Hour).Unix()))
			return nil
		})

	}
	time.Sleep(1 * time.Second)
	if !sli1del || !sli2del {
		t.Fatalf("slice should be deleted")
	}
	nodes := m.(engine).doFindDetachedNodes(time.Now())
	if len(nodes) != 4 {
		t.Fatalf("find detached nodes error: %v", nodes)
	}
	nodes = m.(engine).doFindDetachedNodes(time.Now().Add(-24 * time.Hour))
	if len(nodes) != 2 {
		t.Fatalf("find detached nodes error: %v", nodes)
	}
	if eno := m.Clone(Background(), RootInode, TrashInode, cloneDir, "xxx", 0, 022, &count, &total); !errors.Is(eno, syscall.EPERM) {
		t.Fatalf("cloning trash files are not supported")
	}
	if eno := m.Clone(Background(), TrashInode+1, 1000, cloneDir, "xxx", 0, 022, &count, &total); !errors.Is(eno, syscall.EPERM) {
		t.Fatalf("cloning files in the trash is not supported")
	}
}

func checkEntryTree(t *testing.T, m Meta, srcIno, dstIno Ino, walkFunc func(srcEntry, dstEntry *Entry, dstIno Ino)) {
	var entries1 []*Entry
	if eno := m.Readdir(Background(), srcIno, 1, &entries1); eno != 0 {
		t.Fatalf("Readdir: %s", eno)
	}

	var entries2 []*Entry
	if eno := m.Readdir(Background(), dstIno, 1, &entries2); eno != 0 {
		t.Fatalf("Readdir: %s", eno)
	}
	sort.Slice(entries1, func(i, j int) bool { return string(entries1[i].Name) < string(entries1[j].Name) })
	sort.Slice(entries2, func(i, j int) bool { return string(entries2[i].Name) < string(entries2[j].Name) })
	if len(entries1) != len(entries2) {
		t.Fatalf("number of children: %d != %d", len(entries1), len(entries2))
	}
	for idx, entry := range entries1 {
		if string(entry.Name) == "." || string(entry.Name) == ".." {
			continue
		}
		if entry.Attr.Typ == TypeDirectory {
			checkEntryTree(t, m, entry.Inode, entries2[idx].Inode, walkFunc)
		}
		walkFunc(entry, entries2[idx], dstIno)
	}
}

func checkEntry(t *testing.T, m Meta, srcEntry, dstEntry *Entry, dstParentIno Ino) {
	if !bytes.Equal(srcEntry.Name, dstEntry.Name) {
		t.Fatalf("unmatched name: %s, %s", srcEntry.Name, dstEntry.Name)
	}
	srcAttr := srcEntry.Attr
	dstAttr := dstEntry.Attr
	if dstAttr.Parent != dstParentIno {
		t.Fatalf("unmatched parent: %d, %d", dstAttr.Parent, dstParentIno)
	}
	if srcAttr.Typ == TypeFile && dstAttr.Nlink != 1 || srcAttr.Typ != TypeFile && srcAttr.Nlink != dstAttr.Nlink {
		t.Fatalf("nlink not correct: srcType:%d,srcNlink:%d,dstType:%d,dstNlink:%d", srcAttr.Typ, srcAttr.Nlink, dstAttr.Typ, dstAttr.Nlink)
	}

	srcAttr.Nlink = 0
	dstAttr.Nlink = 0
	srcAttr.Parent = 0
	dstAttr.Parent = 0
	if *srcAttr != *dstAttr {
		t.Fatalf("unmatched attr: %#v, %#v", *srcAttr, *dstAttr)
	}

	// check xattr
	var value1 []byte
	if eno := m.ListXattr(Background(), srcEntry.Inode, &value1); eno != 0 {
		t.Fatalf("list xattr: %s", eno)
	}
	keys := bytes.Split(value1, []byte{0})
	for _, key := range keys {
		if key == nil || len(key) == 0 {
			continue
		}
		var v1, v2 []byte
		if eno := m.GetXattr(Background(), srcEntry.Inode, string(key), &v1); eno != 0 {
			t.Fatalf("get xattr: %s", eno)
		}
		if eno := m.GetXattr(Background(), dstEntry.Inode, string(key), &v2); eno != 0 {
			t.Fatalf("get xattr: %s", eno)
		}
		if !bytes.Equal(v1, v2) {
			t.Fatalf("xattr not equal")
		}
	}
}

func testQuota(t *testing.T, m Meta) {
	if err := m.NewSession(true); err != nil {
		t.Fatalf("New session: %s", err)
	}
	defer m.CloseSession()
	ctx := Background()
	var inode, parent Ino
	var attr Attr
	if st := m.Mkdir(ctx, RootInode, "quota", 0755, 0, 0, &parent, &attr); st != 0 {
		t.Fatalf("Mkdir quota: %s", st)
	}
	p := "/quota"
	if err := m.HandleQuota(ctx, QuotaSet, p, map[string]*Quota{p: {MaxSpace: 2 << 30, MaxInodes: 6}}, false, false, false); err != nil {
		t.Fatalf("HandleQuota set %s: %s", p, err)
	}
	m.getBase().loadQuotas()
	if st := m.Mkdir(ctx, parent, "d1", 0755, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("Mkdir quota/d1: %s", st)
	}
	p = "/quota/d1"
	if err := m.HandleQuota(ctx, QuotaSet, p, map[string]*Quota{p: {MaxSpace: 1 << 30, MaxInodes: 5}}, false, false, false); err != nil {
		t.Fatalf("HandleQuota %s: %s", p, err)
	}
	m.getBase().loadQuotas()
	if st := m.Create(ctx, inode, "f1", 0644, 0, 0, nil, &attr); st != 0 {
		t.Fatalf("Create quota/d1/f1: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d2", 0755, 0, 0, &parent, &attr); st != 0 {
		t.Fatalf("Mkdir quota/d2: %s", st)
	}
	if st := m.Mkdir(ctx, parent, "d22", 0755, 0, 0, &inode, &attr); st != 0 {
		t.Fatalf("Mkdir quota/d2/d22: %s", st)
	}
	p = "/quota/d2/d22"
	if err := m.HandleQuota(ctx, QuotaSet, p, map[string]*Quota{p: {MaxSpace: 1 << 30, MaxInodes: 5}}, false, false, false); err != nil {
		t.Fatalf("HandleQuota %s: %s", p, err)
	}
	m.getBase().loadQuotas()
	// parent -> d2, inode -> d22
	if st := m.Create(ctx, parent, "f2", 0644, 0, 0, nil, &attr); st != 0 {
		t.Fatalf("Create quota/d2/f2: %s", st)
	}
	if st := m.Create(ctx, inode, "f22", 0644, 0, 0, nil, &attr); st != 0 {
		t.Fatalf("Create quota/d22/f22: %s", st)
	}
	time.Sleep(time.Second * 5)

	qs := make(map[string]*Quota)
	p = "/quota"
	if err := m.HandleQuota(ctx, QuotaGet, p, qs, false, false, false); err != nil {
		t.Fatalf("HandleQuota get %s: %s", p, err)
	} else if q := qs[p]; q.MaxSpace != 2<<30 || q.MaxInodes != 6 || q.UsedSpace != 6*4<<10 || q.UsedInodes != 6 {
		t.Fatalf("HandleQuota get %s: %+v", p, q)
	}
	delete(qs, p)
	p = "/quota/d1"
	if err := m.HandleQuota(ctx, QuotaGet, p, qs, false, false, false); err != nil {
		t.Fatalf("HandleQuota get %s: %s", p, err)
	} else if q := qs[p]; q.MaxSpace != 1<<30 || q.MaxInodes != 5 || q.UsedSpace != 4<<10 || q.UsedInodes != 1 {
		t.Fatalf("HandleQuota get %s: %+v", p, q)
	}
	delete(qs, p)
	p = "/quota/d2/d22"
	if err := m.HandleQuota(ctx, QuotaGet, p, qs, false, false, false); err != nil {
		t.Fatalf("HandleQuota get %s: %s", p, err)
	} else if q := qs[p]; q.MaxSpace != 1<<30 || q.MaxInodes != 5 || q.UsedSpace != 4<<10 || q.UsedInodes != 1 {
		t.Fatalf("HandleQuota get %s: %+v", p, q)
	}
	delete(qs, p)

	if err := m.HandleQuota(ctx, QuotaList, "", qs, false, false, false); err != nil {
		t.Fatalf("HandleQuota list: %s", err)
	} else {
		if len(qs) != 3 {
			t.Fatalf("HandleQuota list bad result: %d", len(qs))
		}
	}

	getUsedInodes := func(path string) int64 {
		m.getBase().doFlushQuotas()
		qs := make(map[string]*Quota)
		if err := m.HandleQuota(ctx, QuotaGet, path, qs, false, false, false); err != nil {
			t.Fatalf("HandleQuota list: %s", err)
		}
		return qs[path].UsedInodes
	}

	// unlink opened file
	var nInode Ino
	if st := m.Lookup(ctx, parent, "f2", &nInode, &attr, false); st != 0 {
		t.Fatalf("Lookup quota/d2/f2: %s", st)
	}

	if st := m.Open(ctx, nInode, 0, &attr); st != 0 {
		t.Fatalf("Open quota/d2/f2: %s", st)
	}

	if st := m.Unlink(ctx, parent, "f2"); st != 0 {
		t.Fatalf("Unlink quota/d2/f2 err: %s", st)
	}

	if st := m.Close(ctx, nInode); st != 0 {
		t.Fatalf("Close quota/d2/f2: %s", st)
	}

	if used := getUsedInodes("/quota"); used != 5 {
		t.Fatalf("used inodes of /quota should be 5, but got %d", used)
	}

	// rename opened file
	if st := m.Lookup(ctx, inode, "f22", &nInode, &attr, false); st != 0 {
		t.Fatalf("Lookup quota/d2/d22/f22: %s", st)
	}

	if st := m.Open(ctx, nInode, 0, &attr); st != 0 {
		t.Fatalf("Open quota/d2/d22/f22: %s", st)
	}

	if st := m.Rename(ctx, inode, "f22", inode, "f23", 0, &nInode, nil); st != 0 {
		t.Fatalf("Rename quota/d2/d22/f22 to quota/d2/d22/f23 err: %s", st)
	}

	if st := m.Close(ctx, nInode); st != 0 {
		t.Fatalf("Close quota/d2/d22/f23: %s", st)
	}

	if used := getUsedInodes("/quota"); used != 5 {
		t.Fatalf("used inodes of /quota should be 5, but got %d", used)
	}

	if st := m.Create(ctx, parent, "f3", 0644, 0, 0, &nInode, &attr); st != 0 {
		t.Fatalf("Create quota/d2/f3: %s", st)
	}

	if err := m.HandleQuota(ctx, QuotaDel, "/quota/d1", nil, false, false, false); err != nil {
		t.Fatalf("HandleQuota del /quota/d1: %s", err)
	}
	if err := m.HandleQuota(ctx, QuotaDel, "/quota/d2", nil, false, false, false); err != nil {
		t.Fatalf("HandleQuota del /quota/d2: %s", err)
	}

	qs = make(map[string]*Quota)
	if err := m.HandleQuota(ctx, QuotaList, "", qs, false, false, false); err != nil {
		t.Fatalf("HandleQuota list: %s", err)
	} else {
		if len(qs) != 2 {
			t.Fatalf("HandleQuota list bad result: %d", len(qs))
		}
	}
	m.getBase().loadQuotas()
	if st := m.Create(ctx, parent, "f4", 0644, 0, 0, nil, &attr); st != syscall.EDQUOT {
		t.Fatalf("Create quota/d22/f4: %s", st)
	}
}

func testAtime(t *testing.T, m Meta) {
	ctx := Background()
	var inode, parent Ino
	var attr Attr
	if st := m.Mkdir(ctx, RootInode, "atime", 0755, 0, 0, &parent, &attr); st != 0 {
		t.Fatalf("Mkdir atime: %s", st)
	}

	// open, read, read atime < mtime, read recent, readdir, readlink
	testFn := func(name string) (ret [6]bool) {
		fname := "f-" + name
		if st := m.Create(ctx, parent, fname, 0644, 0, 0, &inode, &attr); st != 0 {
			t.Fatalf("Create atime/%s: %s", fname, st)
		}
		// atime < ctime
		attr.Atime, attr.Atimensec = 1234, 5678
		if st := m.SetAttr(ctx, inode, SetAttrAtime, 0, &attr); st != 0 {
			t.Fatalf("Setattr atime/%s: %s", fname, st)
		}
		if st := m.Open(ctx, inode, 0, &attr); st != 0 {
			t.Fatalf("Open atime/%s: %s", fname, st)
		}
		defer m.Close(ctx, inode)
		ret[0] = attr.Atime != 1234

		attr.Atime, attr.Atimensec = 1234, 5678
		if st := m.SetAttr(ctx, inode, SetAttrAtime, 0, &attr); st != 0 {
			t.Fatalf("Setattr atime/%s: %s", fname, st)
		}
		var slices []Slice
		if st := m.Read(ctx, inode, 0, &slices); st != 0 {
			t.Fatalf("Read atime/%s: %s", fname, st)
		}
		if st := m.GetAttr(ctx, inode, &attr); st != 0 {
			t.Fatalf("Getattr after read atime/%s: %s", fname, st)
		}
		ret[1] = attr.Atime != 1234

		// atime < mtime
		now := time.Now()
		attr.Atime = now.Unix() - 2
		attr.Mtime = now.Unix()
		if st := m.SetAttr(ctx, inode, SetAttrAtime|SetAttrMtime, 0, &attr); st != 0 {
			t.Fatalf("Setattr atime/%s: %s", fname, st)
		}
		if st := m.Read(ctx, inode, 0, &slices); st != 0 {
			t.Fatalf("Read atime/%s: %s", fname, st)
		}
		if st := m.GetAttr(ctx, inode, &attr); st != 0 {
			t.Fatalf("Getattr after read atime/%s: %s", fname, st)
		}
		ret[2] = attr.Atime >= now.Unix()

		// atime = ctime = mtime, atime = now
		if st := m.SetAttr(ctx, inode, SetAttrAtimeNow|SetAttrMtimeNow, 0, &attr); st != 0 {
			t.Fatalf("Setattr atime/%s: %s", fname, st)
		}
		time.Sleep(time.Second * 2)
		now = time.Now()
		if st := m.Read(ctx, inode, 0, &slices); st != 0 {
			t.Fatalf("Read atime/%s: %s", fname, st)
		}
		if st := m.GetAttr(ctx, inode, &attr); st != 0 {
			t.Fatalf("Getattr after read atime/%s: %s", fname, st)
		}
		ret[3] = attr.Atime >= now.Unix()

		// readdir
		fname = "d-" + name
		if st := m.Mkdir(ctx, parent, fname, 0755, 0, 0, &inode, &attr); st != 0 {
			t.Fatalf("Mkdir atime/%s: %s", fname, st)
		}
		attr.Atime, attr.Atimensec = 1234, 5678
		if st := m.SetAttr(ctx, inode, SetAttrAtime, 0, &attr); st != 0 {
			t.Fatalf("Setattr atime/%s: %s", fname, st)
		}
		var entries []*Entry
		if st := m.Readdir(ctx, inode, 0, &entries); st != 0 {
			t.Fatalf("Readdir atime/%s: %s", fname, st)
		}
		if st := m.GetAttr(ctx, inode, &attr); st != 0 {
			t.Fatalf("Getattr after readdir atime/%s: %s", fname, st)
		}
		ret[4] = attr.Atime != 1234

		// readlink
		fname = "l-" + name
		if st := m.Symlink(ctx, parent, fname, "f-"+name, &inode, &attr); st != 0 {
			t.Fatalf("Symlink atime/%s: %s", fname, st)
		}
		attr.Atime, attr.Atimensec = 1234, 5678
		if st := m.SetAttr(ctx, inode, SetAttrAtime, 0, &attr); st != 0 {
			t.Fatalf("Setattr atime/%s: %s", fname, st)
		}
		var target []byte
		if st := m.ReadLink(ctx, inode, &target); st != 0 {
			t.Fatalf("Readlink atime/%s: %s", fname, st)
		}
		if st := m.GetAttr(ctx, inode, &attr); st != 0 {
			t.Fatalf("Getattr after readlink atime/%s: %s", fname, st)
		}
		ret[5] = attr.Atime != 1234
		return
	}

	for name, exp := range map[string][6]bool{
		RelAtime:    {true, true, true, false, true, true},
		StrictAtime: {true, true, true, true, true, true},
		NoAtime:     {false, false, false, false, false, false},
	} {
		m.getBase().conf.AtimeMode = name
		if ret := testFn(name); ret != exp {
			t.Fatalf("Test %s: expected %v, got %v", name, exp, ret)
		}
	}
}

func TestSymlinkCache(t *testing.T) {
	cache := newSymlinkCache(10000)

	job := make(chan Ino)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ino := range job {
				cache.Store(ino, []byte(fmt.Sprintf("file%d", ino)))
			}
		}()
	}

	for i := 0; i < 10000; i++ {
		job <- Ino(i)
	}
	close(job)
	wg.Wait()

	cache.doClean()
	require.Equal(t, int32(8000), cache.size.Load())
}
