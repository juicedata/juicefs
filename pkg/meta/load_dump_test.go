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

package meta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/juicedata/juicefs/pkg/meta/pb"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const sampleFile = "metadata.sample"
const subSampleFile = "metadata-sub.sample"

func TestEscape(t *testing.T) {
	cases := []struct {
		value                []rune
		gbkStart, gbkEnd     int
		expectedEscapedValue string
	}{
		{value: []rune("%1F果汁数据科技有限公司%2B"), gbkStart: 0, gbkEnd: 0},
		{value: []rune("果汁数据科技有限公司%1F"), gbkStart: 0, gbkEnd: 1},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 1, gbkEnd: 2},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 5, gbkEnd: 10},
		{value: []rune("果汁数据科技有限公司"), gbkStart: 0, gbkEnd: 10},
		{value: []rune("GBK果汁数据科技有限公司文件"), gbkStart: 0, gbkEnd: 15},
		{value: []rune("%果汁数据科%技有限公司%"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("\"果汁数据科\"技有限公司%"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("\\果汁数\\据科技有限公司"), gbkStart: 1, gbkEnd: 4},
		{value: []rune("file name with spaces"), expectedEscapedValue: "file%20name%20with%20spaces"},
	}
	for _, c := range cases {
		var v []byte
		prefix := c.value[:c.gbkStart]
		middle := c.value[c.gbkStart:c.gbkEnd]
		suffix := c.value[c.gbkEnd:]
		gbk, err := Utf8ToGbk([]byte(string(middle)))
		if err != nil {
			t.Fatalf("Utf8ToGbk error: %v", err)
		}
		v = append(v, []byte(string(prefix))...)
		v = append(v, gbk...)
		v = append(v, []byte(string(suffix))...)
		s := escape(string(v))
		t.Log("escape value: ", s)
		if c.expectedEscapedValue != "" && s != c.expectedEscapedValue {
			t.Fatalf("expected escaped value %q, but got %q", c.expectedEscapedValue, s)
		}
		r := unescape(s)
		if !bytes.Equal(r, v) {
			t.Fatalf("expected %v, but got %v", v, r)
		}
	}
}

func Utf8ToGbk(s []byte) ([]byte, error) {
	reader := transform.NewReader(bytes.NewReader(s), simplifiedchinese.GBK.NewEncoder())
	d, e := io.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	return d, nil
}

func GbkToUtf8(s []byte) ([]byte, error) {
	reader := transform.NewReader(bytes.NewReader(s), simplifiedchinese.GBK.NewDecoder())
	d, e := io.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	return d, nil
}

func checkMeta(t *testing.T, m Meta) {
	if _, err := m.Load(true); err != nil {
		t.Fatalf("load setting: %s", err)
	}

	counters := map[string]int64{
		"usedSpace":   115392512,
		"totalInodes": 14,
		"nextInode":   35,
		"nextChunk":   9,
		"nextSession": 0,
		"nextTrash":   1,
	}
	for name, expect := range counters {
		val, err := m.getBase().en.getCounter(name)
		if err != nil {
			t.Fatalf("get counter %s: %s", name, err)
		}
		if m.Name() == "redis" && (name == "nextChunk" || name == "nextInode") {
			expect--
		}
		if val != expect {
			t.Fatalf("counter %s: %d != %d", name, val, expect)
		}
	}

	ctx := Background()
	var entries []*Entry
	if st := m.Readdir(ctx, 1, 1, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 11 {
		t.Fatalf("entries: %d", len(entries))
	}

	var expectedStat dirStat
	for _, entry := range entries {
		fname := string(entry.Name)
		if strings.HasPrefix(fname, "GBK") {
			if utf8, err := GbkToUtf8(entry.Name); err != nil || string(utf8) != "GBK果汁数据科技有限公司文件" {
				t.Fatalf("load GBK file error: %s", string(utf8))
			}
		}
		if strings.HasPrefix(fname, "UTF8") && fname != "UTF8果汁数据科技有限公司目录" && fname != "UTF8果汁数据科技有限公司文件" {
			t.Fatalf("load entries error: %s", fname)
		}
		if string(entry.Name) != "." && string(entry.Name) != ".." {
			var length uint64
			if entry.Attr.Typ == TypeFile {
				length = entry.Attr.Length
			}
			expectedStat.inodes++
			expectedStat.length += int64(length)
			expectedStat.space += align4K(length)
		}
	}

	stat, st := m.(engine).doGetDirStat(ctx, 1, false)
	if st != 0 {
		t.Fatalf("get dir stat: %s", st)
	}
	if stat == nil {
		t.Fatalf("get dir stat: nil")
	}
	if *stat != expectedStat {
		t.Fatalf("expected: %v, but got: %v", expectedStat, *stat)
	}

	var summary Summary
	if st = m.GetSummary(ctx, 1, &summary, true, true); st != 0 {
		t.Fatalf("get summary: %s", st)
	}
	expectedQuota := Quota{
		MaxInodes:  100,
		MaxSpace:   1 << 30,
		UsedSpace:  int64(summary.Size) - align4K(0),
		UsedInodes: int64(summary.Dirs+summary.Files) - 1,
	}

	quota, err := m.(engine).doGetQuota(ctx, DirQuotaType, 1)
	if err != nil {
		t.Fatalf("get quota: %s", err)
	}
	if quota == nil {
		t.Fatalf("get quota: nil")
	}
	if *quota != expectedQuota {
		t.Fatalf("expected: %v, but got: %v", expectedQuota, *quota)
	}

	userQuota, err := m.(engine).doGetQuota(ctx, UserQuotaType, 501)
	if err != nil {
		t.Fatalf("get user quota: %s", err)
	}
	if userQuota == nil {
		t.Fatalf("get user quota: nil")
	}
	expectedUserQuota := Quota{
		MaxSpace:  1099511627776,
		MaxInodes: 1000000,
	}
	if userQuota.MaxSpace != expectedUserQuota.MaxSpace || userQuota.MaxInodes != expectedUserQuota.MaxInodes {
		t.Fatalf("user quota: expected maxSpace=%d, maxInodes=%d, but got maxSpace=%d, maxInodes=%d",
			expectedUserQuota.MaxSpace, expectedUserQuota.MaxInodes, userQuota.MaxSpace, userQuota.MaxInodes)
	}

	groupQuota, err := m.(engine).doGetQuota(ctx, GroupQuotaType, 20)
	if err != nil {
		t.Fatalf("get group quota: %s", err)
	}
	if groupQuota == nil {
		t.Fatalf("get group quota: nil")
	}
	expectedGroupQuota := Quota{
		MaxSpace:  2199023255552,
		MaxInodes: 2000000,
	}
	if groupQuota.MaxSpace != expectedGroupQuota.MaxSpace || groupQuota.MaxInodes != expectedGroupQuota.MaxInodes {
		t.Fatalf("group quota: expected maxSpace=%d, maxInodes=%d, but got maxSpace=%d, maxInodes=%d",
			expectedGroupQuota.MaxSpace, expectedGroupQuota.MaxInodes, groupQuota.MaxSpace, groupQuota.MaxInodes)
	}

	attr := &Attr{}
	if st := m.GetAttr(ctx, 2, attr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if attr.Nlink != 1 || attr.Length != 24 {
		t.Fatalf("nlink: %d, length: %d", attr.Nlink, attr.Length)
	}

	if attr.Flags != 128 {
		t.Fatalf("expect the flags euqal 128, but actual is: %d", attr.Flags)
	}

	if attr.AccessACL == 0 || attr.DefaultACL == 0 {
		t.Fatalf("expect ACL not 0, but actual is: %d, %d", attr.AccessACL, attr.DefaultACL)
	}

	ar := &aclAPI.Rule{}
	if st := m.GetFacl(ctx, 2, aclAPI.TypeAccess, ar); st != 0 {
		t.Fatalf("get access acl: %s", st)
	}
	ar2 := &aclAPI.Rule{
		Owner: 6,
		Group: 4,
		Mask:  4,
		Other: 4,
		NamedUsers: []aclAPI.Entry{
			{Id: 1, Perm: 6},
			{Id: 2, Perm: 7},
		},
		NamedGroups: nil,
	}
	if !bytes.Equal(ar.Encode(), ar2.Encode()) {
		t.Fatalf("access acl: %v != %v", ar, ar2)
	}

	dr := &aclAPI.Rule{}
	if st := m.GetFacl(ctx, 2, aclAPI.TypeDefault, dr); st != 0 {
		t.Fatalf("get default acl: %s", st)
	}
	dr2 := &aclAPI.Rule{
		Owner:      7,
		Group:      5,
		Mask:       5,
		Other:      5,
		NamedUsers: nil,
		NamedGroups: []aclAPI.Entry{
			{Id: 3, Perm: 6},
			{Id: 4, Perm: 7},
		},
	}
	if !bytes.Equal(dr.Encode(), dr2.Encode()) {
		t.Fatalf("default acl: %v != %v", dr, dr2)
	}

	var slices []Slice
	if st := m.Read(ctx, 2, 0, &slices); st != 0 {
		t.Fatalf("read chunk: %s", st)
	}
	if len(slices) != 1 || slices[0].Id != 4 || slices[0].Size != 24 {
		t.Fatalf("slices: %v", slices)
	}
	if st := m.GetAttr(ctx, 4, attr); st != 0 || attr.Nlink != 2 { // hard link
		t.Fatalf("getattr: %s, %d", st, attr.Nlink)
	}
	if ps := m.GetParents(ctx, 4); len(ps) != 2 || ps[1] != 1 || ps[3] != 1 {
		t.Fatalf("getparents: %+v != {1:1, 3:1}", ps)
	}
	var target []byte

	if st := m.ReadLink(ctx, 5, &target); st == 0 { // symlink
		if utf8, err := GbkToUtf8(target); err != nil || string(utf8) != "GBK果汁数据科技有限公司文件" {
			t.Fatalf("readlink: %s, %s", st, target)
		}
	} else {
		t.Fatalf("readlink: %s, %s", st, target)
	}

	var value []byte
	if st := m.GetXattr(ctx, 2, "k", &value); st != 0 || string(value) != "v" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
	if st := m.GetXattr(ctx, 3, "dk", &value); st != 0 || string(value) != "果汁%25" {
		t.Fatalf("getxattr: %s %v", st, value)
	}
}

func testLoadSub(t *testing.T, uri, fname string) {
	m := NewClient(uri, nil)
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	fp, err := os.Open(fname)
	if err != nil {
		t.Fatalf("open file: %s", fname)
	}
	defer fp.Close()
	if err = m.LoadMeta(fp); err != nil {
		t.Fatalf("load meta: %s", err)
	}

	var entries []*Entry
	if st := m.Readdir(Background(), 1, 0, &entries); st != 0 {
		t.Fatalf("readdir: %s", st)
	} else if len(entries) != 4 {
		t.Fatalf("entries: %d", len(entries))
	}
	for _, entry := range entries {
		fname := string(entry.Name)
		if fname != "." && fname != ".." && fname != "big" && fname != "f11" {
			t.Fatalf("invalid entry name: %s", fname)
		}
	}
}

func testDump(t *testing.T, m Meta, root Ino, expect, result string) {
	fp, err := os.OpenFile(result, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("open file %s: %s", result, err)
	}
	defer fp.Close()
	if _, err = m.Load(true); err != nil {
		t.Fatalf("load setting: %s", err)
	}
	if err = m.DumpMeta(fp, root, 1, false, true, false); err != nil {
		t.Fatalf("dump meta: %s", err)
	}
	cmd := exec.Command("diff", expect, result)
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("diff %s %s: %s", expect, result, out)
	}
	fp.Seek(0, 0)
	if err = m.DumpMeta(fp, root, 10, false, false, false); err != nil {
		t.Fatalf("dump meta: %s", err)
	}
	cmd = exec.Command("diff", expect, result)
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("diff %s %s: %s", expect, result, out)
	}
}

func testLoadDump(t *testing.T, name, addr string) {
	t.Run("Metadata Engine: "+name, func(t *testing.T) {
		m := testLoad(t, addr, sampleFile, false)
		testDump(t, m, 1, sampleFile, "test.dump")
		m.Shutdown()
		conf := DefaultConf()
		conf.Subdir = "d1"
		m = NewClient(addr, conf)
		_ = m.Chroot(Background(), "d1")
		testDump(t, m, 1, subSampleFile, "test_subdir.dump")
		testDump(t, m, 0, sampleFile, "test.dump")
		_ = m.Shutdown()
		testLoadSub(t, addr, subSampleFile)
	})
}

func TestLoadDump(t *testing.T) { //skip mutate
	testLoadDump(t, "redis", "redis://127.0.0.1/10")
	testLoadDump(t, "sqlite3", "sqlite3://"+path.Join(t.TempDir(), "jfs-load-dump-sqlite3.db"))
	testLoadDump(t, "badger", "badger://"+path.Join(t.TempDir(), "jfs-load-dump"))
	testLoadDump(t, "tikv", "tikv://127.0.0.1:2379/jfs-load-dump")
}

func testDumpV2(t *testing.T, m Meta, result string, opt *DumpOption) {
	if opt == nil {
		opt = &DumpOption{Threads: 10, KeepSecret: true}
	}
	fp, err := os.OpenFile(result, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("open file %s: %s", result, err)
	}
	defer fp.Close()
	if _, err = m.Load(true); err != nil {
		t.Fatalf("load setting: %s", err)
	}
	if err = m.DumpMetaV2(Background(), fp, opt); err != nil {
		t.Fatalf("dump meta: %s", err)
	}
	fp.Sync()
}

func testLoad(t *testing.T, uri, fname string, v2 bool) Meta {
	m := NewClient(uri, nil)
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	fp, err := os.Open(fname)
	if err != nil {
		t.Fatalf("open file: %s", fname)
	}
	defer fp.Close()
	if v2 {
		if err = m.LoadMetaV2(Background(), fp, &LoadOption{Threads: 10}); err != nil {
			t.Fatalf("load meta: %s", err)
		}
	} else {
		if err = m.LoadMeta(fp); err != nil {
			t.Fatalf("load meta: %s", err)
		}
	}
	checkMeta(t, m)
	return m
}

func testLoadDumpV2(t *testing.T, name, addr1, addr2 string) {
	t.Run("Metadata Engine: "+name, func(t *testing.T) {
		start := time.Now()
		m := testLoad(t, addr1, sampleFile, false)
		t.Logf("load meta: %v", time.Since(start))
		start = time.Now()
		testDumpV2(t, m, fmt.Sprintf("%s.dump", name), nil)
		m.Shutdown()
		t.Logf("dump meta v2: %v", time.Since(start))
		start = time.Now()
		m = testLoad(t, addr2, fmt.Sprintf("%s.dump", name), true)
		m.Shutdown()
		t.Logf("load meta v2: %v", time.Since(start))
	})
}

func testLoadOtherEngine(t *testing.T, src, dst, dstAddr string) {
	t.Run(fmt.Sprintf("Load %s to %s", src, dst), func(t *testing.T) {
		m := testLoad(t, dstAddr, fmt.Sprintf("%s.dump", src), true)
		m.Shutdown()
	})
}

func TestLoadDumpV2(t *testing.T) {
	logger.SetLevel(logrus.DebugLevel)

	engines := map[string][]string{
		"sqlite3": {"sqlite3://" + path.Join(t.TempDir(), "dev.db"), "sqlite3://" + path.Join(t.TempDir(), "dev2.db")},
		// "mysql": {"mysql://root:@/dev", "mysql://root:@/dev2"},
		"redis":  {"redis://127.0.0.1:6379/2", "redis://127.0.0.1:6379/3"},
		"badger": {"badger://" + path.Join(t.TempDir(), "jfs-load-duimp-testdb-bk1"), "badger://" + path.Join(t.TempDir(), "jfs-load-duimp-testdb-bk2")},
		// "tikv":  {"tikv://127.0.0.1:2379/jfs-load-dump-1", "tikv://127.0.0.1:2379/jfs-load-dump-2"},
	}

	for name, addrs := range engines {
		testLoadDumpV2(t, name, addrs[0], addrs[1])
		testSecretAndTrash(t, addrs[0], addrs[1])
	}

	for src := range engines {
		for dst, dstAddr := range engines {
			if src == dst {
				continue
			}
			testLoadOtherEngine(t, src, dst, dstAddr[1])
		}
	}
}

func duplicateBackup(t *testing.T, conflict bool) []byte {
	t.Helper()
	format, err := json.Marshal(testFormat())
	if err != nil {
		t.Fatal(err)
	}

	attr := (&Attr{Typ: TypeFile, Mode: 0644, Nlink: 1, Parent: RootInode, Length: 4096}).Marshal()
	otherAttr := (&Attr{Typ: TypeFile, Mode: 0600, Nlink: 1, Parent: RootInode, Length: 8192}).Marshal()
	slices := marshalSlice(0, 11, 4096, 0, 4096)
	records := []*pb.Batch{
		{Nodes: []*pb.Node{{Inode: 2, Data: attr}, {Inode: 2, Data: attr}}},
		{Edges: []*pb.Edge{{Parent: 1, Inode: 2, Name: []byte("file"), Type: TypeFile}, {Parent: 1, Inode: 2, Name: []byte("file"), Type: TypeFile}}},
		{Chunks: []*pb.Chunk{{Inode: 2, Index: 0, Slices: slices}, {Inode: 2, Index: 0, Slices: slices}}},
		{Symlinks: []*pb.Symlink{{Inode: 3, Target: []byte("target")}, {Inode: 3, Target: []byte("target")}}},
		{Xattrs: []*pb.Xattr{{Inode: 2, Name: "user.test", Value: []byte("value")}, {Inode: 2, Name: "user.test", Value: []byte("value")}}},
		{Parents: []*pb.Parent{{Inode: 2, Parent: 1, Cnt: 2}, {Inode: 2, Parent: 1, Cnt: 2}}},
		{SliceRefs: []*pb.SliceRef{{Id: 11, Size: 4096, Refs: 2}, {Id: 11, Size: 4096, Refs: 2}}},
	}
	if conflict {
		records = []*pb.Batch{{Nodes: []*pb.Node{{Inode: 2, Data: attr}, {Inode: 2, Data: otherAttr}}}}
	}

	var buf bytes.Buffer
	bak := newBakFormat()
	if err = bak.writeSegment(&buf, newBakSegment(&pb.Format{Data: format})); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err = bak.writeSegment(&buf, newBakSegment(record)); err != nil {
			t.Fatal(err)
		}
		if !conflict {
			if err = bak.writeSegment(&buf, newBakSegment(record)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = bak.writeFooter(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func loadDuplicateBackup(t *testing.T, m Meta) {
	t.Helper()
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	if err := m.LoadMetaV2(Background(), bytes.NewReader(duplicateBackup(t, false)), &LoadOption{Threads: 10}); err != nil {
		t.Fatalf("load duplicate backup: %s", err)
	}
}

func checkSQLDuplicateBackup(t *testing.T, m *dbMeta) {
	t.Helper()
	loadDuplicateBackup(t, m)
	t.Cleanup(func() { _ = m.Reset() })
	for name, bean := range map[string]interface{}{
		"node":     &node{},
		"edge":     &edge{},
		"chunk":    &chunk{},
		"symlink":  &symlink{},
		"xattr":    &xattr{},
		"sliceRef": &sliceRef{},
	} {
		count, err := m.db.Count(bean)
		if err != nil {
			t.Fatalf("count %s: %s", name, err)
		}
		if count != 1 {
			t.Fatalf("%s count: got %d, want 1", name, count)
		}
	}
	if err := m.loadXattrs(Background(), &pb.Batch{Xattrs: []*pb.Xattr{{Inode: 4, Name: "user.empty"}}}); err != nil {
		t.Fatalf("load empty xattr: %s", err)
	}
	var empty xattr
	if ok, err := m.db.Where("inode = ?", 4).Get(&empty); err != nil || !ok || len(empty.Value) != 0 {
		t.Fatalf("empty xattr: %+v, found %v, err %v", empty, ok, err)
	}
	checkSQLIgnoredRows(t, m)
}

func TestLoadMetaV2DuplicateRecords(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		client, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "duplicate.db")+"?table_prefix=backup", testConfig())
		if err != nil {
			t.Fatal(err)
		}
		m := client.(*dbMeta)
		t.Cleanup(func() { _ = m.Shutdown() })
		checkSQLDuplicateBackup(t, m)
	})

	t.Run("badger", func(t *testing.T) {
		client, err := newKVMeta("badger", t.TempDir(), testConfig())
		if err != nil {
			t.Fatal(err)
		}
		m := client.(*kvMeta)
		t.Cleanup(func() { _ = m.Shutdown() })
		loadDuplicateBackup(t, m)

		edgeValue := utils.NewBuffer(9)
		edgeValue.Put8(TypeFile)
		edgeValue.Put64(2)
		checks := []struct {
			key, want []byte
		}{
			{m.inodeKey(2), (&Attr{Typ: TypeFile, Mode: 0644, Nlink: 1, Parent: RootInode, Length: 4096}).Marshal()},
			{m.entryKey(1, "file"), edgeValue.Bytes()},
			{m.chunkKey(2, 0), marshalSlice(0, 11, 4096, 0, 4096)},
			{m.symKey(3), []byte("target")},
			{m.xattrKey(2, "user.test"), []byte("value")},
			{m.parentKey(2, 1), packCounter(2)},
			{m.sliceKey(11, 4096), packCounter(1)},
		}
		for _, check := range checks {
			got, err := m.get(check.key)
			if err != nil {
				t.Fatalf("get %q: %s", check.key, err)
			}
			if !bytes.Equal(got, check.want) {
				t.Fatalf("value for %q: got %v, want %v", check.key, got, check.want)
			}
		}
	})
}

func TestMySQLClientLoadMetaV2DuplicateRecords(t *testing.T) { //skip mutate
	client, err := newSQLMeta("mysql", "root:@/dev", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*dbMeta)
	t.Cleanup(func() { _ = m.Shutdown() })
	checkSQLDuplicateBackup(t, m)
}

func TestPostgreSQLClientLoadMetaV2DuplicateRecords(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	client, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*dbMeta)
	t.Cleanup(func() { _ = m.Shutdown() })
	checkSQLDuplicateBackup(t, m)
}

func TestLoadMetaV2ConflictingDuplicateRecord(t *testing.T) {
	client, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "duplicate-conflict.db"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*dbMeta)
	t.Cleanup(func() { _ = m.Shutdown() })
	if err = m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}
	if err = m.LoadMetaV2(Background(), bytes.NewReader(duplicateBackup(t, true)), &LoadOption{Threads: 10}); err != nil {
		t.Fatalf("load conflicting duplicate record: %s", err)
	}
	got := node{Inode: 2}
	if ok, err := m.db.Get(&got); err != nil || !ok || got.Mode != 0644 || got.Length != 4096 {
		t.Fatalf("existing node changed: %+v, found %v, err %v", got, ok, err)
	}
}

func checkSQLIgnoredRows(t *testing.T, m *dbMeta) {
	t.Helper()
	tests := []struct {
		name string
		row  func(Ino) interface{}
	}{
		{"node", func(ino Ino) interface{} {
			return &node{Inode: ino, Type: TypeFile, Flags: 1, Mode: 0640, Uid: 1001, Gid: 1002,
				Atime: -100, Mtime: 200, Ctime: 300, Atimensec: 123, Mtimensec: 456, Ctimensec: 789,
				Nlink: 2, Length: 1 << 33, Rdev: 17, Parent: 1, AccessACLId: 7, DefaultACLId: 8, Tier: 2}
		}},
		{"edge", func(ino Ino) interface{} {
			return &edge{Parent: 1, Name: []byte(fmt.Sprintf("file-%d\xff\n", ino)), Inode: ino, Type: TypeFile}
		}},
		{"chunk", func(ino Ino) interface{} {
			return &chunk{Inode: ino, Indx: 2, Slices: marshalSlice(0, 11, 4096, 0, 4096)}
		}},
		{"symlink", func(ino Ino) interface{} { return &symlink{Inode: ino, Target: []byte("target\xff")} }},
		{"xattr", func(ino Ino) interface{} { return &xattr{Inode: ino, Name: "user.empty", Value: []byte{}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const first Ino = 100
			n := m.getTxnBatchNum() + 1
			rows := make([]interface{}, 0, n+2)
			for i := 0; i < n; i++ {
				rows = append(rows, tt.row(first+Ino(i)))
				if i == 0 {
					rows = append(rows, tt.row(first))
				}
			}
			rows = append(rows, tt.row(first))
			if err := m.insertRowsIdempotent(rows); err != nil {
				t.Fatal(err)
			}
			// Keep the existing row even when the incoming contents differ.
			conflict := tt.row(first)
			switch v := conflict.(type) {
			case *node:
				v.Mode = 0600
			case *edge:
				v.Inode = 99999
			case *chunk:
				v.Slices = marshalSlice(0, 12, 4096, 0, 4096)
			case *symlink:
				v.Target = []byte("other")
			case *xattr:
				v.Value = []byte("other")
			}
			if err := m.insertRowsIdempotent([]interface{}{conflict}); err != nil {
				t.Fatal(err)
			}
			const workers = 4
			start := make(chan struct{})
			errs := make(chan error, workers)
			for i := 0; i < workers; i++ {
				go func(i int) {
					<-start
					errs <- m.insertRowsIdempotent([]interface{}{tt.row(first), tt.row(first + Ino(n+i)), tt.row(first + Ino(n+workers))})
				}(i)
			}
			close(start)
			for i := 0; i < workers; i++ {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			count, err := m.db.Where("inode >= ?", first).Count(reflect.New(reflect.TypeOf(tt.row(first)).Elem()).Interface())
			if err != nil || count != int64(n+workers+1) {
				t.Fatalf("count: got %d, want %d, err %v", count, n+workers+1, err)
			}
			for _, ino := range []Ino{first, first + Ino(n-1), first + Ino(n+workers-1)} {
				want := tt.row(ino)
				got := reflect.New(reflect.TypeOf(want).Elem()).Interface()
				if ok, err := m.db.Where("inode = ?", ino).Get(got); err != nil || !ok {
					t.Fatalf("get inode %d: found %v, err %v", ino, ok, err)
				}
				gv, wv := reflect.ValueOf(got).Elem(), reflect.ValueOf(want).Elem()
				for j := 0; j < wv.NumField(); j++ {
					if wv.Type().Field(j).Name == "Id" {
						continue
					}
					g, w := gv.Field(j).Interface(), wv.Field(j).Interface()
					if wb, ok := w.([]byte); ok {
						if !bytes.Equal(g.([]byte), wb) {
							t.Fatalf("%s: got %v, want %v", wv.Type().Field(j).Name, g, w)
						}
					} else if !reflect.DeepEqual(g, w) {
						t.Fatalf("%s: got %v, want %v", wv.Type().Field(j).Name, g, w)
					}
				}
			}
			for _, row := range rows {
				if id := reflect.ValueOf(row).Elem().FieldByName("Id"); id.IsValid() && id.Int() != 0 {
					t.Fatalf("input row ID was changed: %v", row)
				}
			}
		})
	}
}

func TestInsertRowsIdempotentLogs(t *testing.T) {
	client, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "duplicate-logs.db")+"?table_prefix=backup", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*dbMeta)
	t.Cleanup(func() { _ = m.Shutdown() })
	if err = m.prepareLoad(Background(), &LoadOption{}); err != nil {
		t.Fatal(err)
	}
	hooks := logger.ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(hooks) })
	hook := logtest.NewLocal(&logger.Logger)
	first := &xattr{Inode: 2, Name: "user.secret", Value: []byte("secret")}
	second := &xattr{Inode: 3, Name: "user.secret", Value: []byte{}}
	if err = m.insertRowsIdempotent([]interface{}{first}); err != nil {
		t.Fatal(err)
	}
	if len(hook.AllEntries()) != 0 {
		t.Fatalf("unexpected log for unique rows: %v", hook.AllEntries())
	}
	if err = m.insertRowsIdempotent([]interface{}{first, second, first}); err != nil {
		t.Fatal(err)
	}
	entries := hook.AllEntries()
	want := "Load backup rows: table=jfs_backup_xattr submitted=3 inserted=1 skipped=2"
	if len(entries) != 1 || entries[0].Level != logrus.WarnLevel || entries[0].Message != want {
		t.Fatalf("unexpected skipped-row logs: %v", entries)
	}
}

func TestRedisLoadMetaV2DuplicateRecords(t *testing.T) {
	client, err := newRedisMeta("redis", "127.0.0.1:6379/13", testConfig())
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*redisMeta)
	t.Cleanup(func() {
		_ = m.Reset()
		_ = m.Shutdown()
	})
	loadDuplicateBackup(t, m)

	slices, err := m.rdb.LRange(Background(), m.chunkKey(2, 0), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) != 1 || !bytes.Equal([]byte(slices[0]), marshalSlice(0, 11, 4096, 0, 4096)) {
		t.Fatalf("chunk slices: %v", slices)
	}
	parent, err := m.rdb.HGet(Background(), m.parentKey(2), "1").Int64()
	if err != nil {
		t.Fatal(err)
	}
	if parent != 2 {
		t.Fatalf("parent count: got %d, want 2", parent)
	}
	multi := append(marshalSlice(0, 21, 4096, 0, 2048), marshalSlice(2048, 22, 4096, 0, 2048)...)
	batch := &pb.Batch{}
	for i := 0; i <= redisPipeLimit; i++ {
		batch.Chunks = append(batch.Chunks, &pb.Chunk{Inode: 4, Index: uint32(i), Slices: multi})
	}
	batch.Chunks = append(batch.Chunks, batch.Chunks[0])
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- m.loadChunks(Background(), batch) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= redisPipeLimit; i++ {
		got, err := m.rdb.LRange(Background(), m.chunkKey(4, uint32(i)), 0, -1).Result()
		if err != nil || len(got) != 2 || strings.Join(got, "") != string(multi) {
			t.Fatalf("chunk %d: %v, err %v", i, got, err)
		}
	}

}

func TestLoadDumpSlow(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	testLoadDump(t, "redis cluster", "redis://127.0.0.1:7001/10")
	testLoadDump(t, "sqlite", "sqlite3://"+path.Join(t.TempDir(), "jfs-load-dump-test.db"))
	testLoadDump(t, "badger", "badger://"+path.Join(t.TempDir(), "jfs-load-duimp-testdb"))
	testLoadDump(t, "etcd", fmt.Sprintf("etcd://%s/jfs-load-dump", os.Getenv("ETCD_ADDR")))
	testLoadDump(t, "postgres", "postgres://localhost:5432/test?sslmode=disable")
}

func TestLoadDump_MemKV(t *testing.T) {
	t.Run("Metadata Engine: memkv", func(t *testing.T) {
		_ = os.Remove(settingPath)
		m := testLoad(t, "memkv://test/jfs", sampleFile, false)
		testDump(t, m, 1, sampleFile, "test.dump")
	})
	t.Run("Metadata Engine: memkv; --SubDir d1 ", func(t *testing.T) {
		_ = os.Remove(settingPath)
		m := testLoad(t, "memkv://user:pass@test/jfs", sampleFile, false)
		if kvm, ok := m.(*kvMeta); ok { // memkv will be empty if created again
			if st := kvm.Chroot(Background(), "d1"); st != 0 {
				t.Fatalf("Chroot to subdir d1: %s", st)
			}
		}
		testDump(t, m, 1, subSampleFile, "test_subdir.dump")
		testDump(t, m, 0, sampleFile, "test.dump")
		_ = os.Remove(settingPath)
		testLoadSub(t, "memkv://user:pass@test/jfs", subSampleFile)
	})
}

func testSecretAndTrash(t *testing.T, addr, addr2 string) {
	m := testLoad(t, addr, sampleFile, false)
	testDumpV2(t, m, "sqlite-secret.dump", &DumpOption{Threads: 10, KeepSecret: true})
	m2 := testLoad(t, addr2, "sqlite-secret.dump", true)
	if m2.GetFormat().EncryptKey != m.GetFormat().EncryptKey {
		t.Fatalf("encrypt key not valid: %s", m2.GetFormat().EncryptKey)
	}
	testDumpV2(t, m, "sqlite-non-secret.dump", &DumpOption{Threads: 10, KeepSecret: false})
	m2.Shutdown()

	m2 = testLoad(t, addr2, "sqlite-non-secret.dump", true)
	if m2.GetFormat().EncryptKey != "removed" {
		t.Fatalf("encrypt key not valid: %s", m2.GetFormat().EncryptKey)
	}

	// trash
	trashs := map[Ino]uint64{
		27: 11,
		29: 10485760,
	}
	cnt := 0
	m2.getBase().scanTrashFiles(Background(), func(inode Ino, size uint64, ts time.Time, count int64) (clean bool, err error) {
		cnt += int(count)
		if tSize, ok := trashs[inode]; !ok || size != tSize {
			t.Fatalf("trash file: %d %d", inode, size)
		}
		return false, nil
	})
	if cnt != len(trashs) {
		t.Fatalf("trash count: %d != %d", cnt, len(trashs))
	}

	m.Shutdown()
	m2.Shutdown()
}

/*
func BenchmarkLoadDumpV2(b *testing.B) {
	logrus.SetLevel(logrus.DebugLevel)
	b.ReportAllocs()
	engines := map[string]string{
		"mysql": "mysql://root:@/dev",
		"redis": "redis://127.0.0.1:6379/2",
		"tikv": "tikv://127.0.0.1:2379/jfs-load-dump-1",
	}

	sample := "../../1M_files_in_one_dir.dump"
	for name, addr := range engines {
		m := NewClient(addr, nil)
		defer func() {
			m.Reset()
			m.Shutdown()
		}()
		b.Run("Load "+name, func(b *testing.B) {
			if err := m.Reset(); err != nil {
				b.Fatalf("reset meta: %s", err)
			}
			fp, err := os.Open(sample)
			if err != nil {
				b.Fatalf("open file: %s", sample)
			}
			defer fp.Close()

			b.ResetTimer()
			if err = m.LoadMeta(fp); err != nil {
				b.Fatalf("load meta: %s", err)
			}
		})

		b.Run("Dump "+name, func(b *testing.B) {
			path := fmt.Sprintf("%s.v1.dump", name)
			fp, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				b.Fatalf("open file %s: %s", path, err)
			}
			defer fp.Close()
			if _, err = m.Load(true); err != nil {
				b.Fatalf("load setting: %s", err)
			}

			b.ResetTimer()
			if err = m.DumpMeta(fp, RootInode, 10, true, true, false); err != nil {
				b.Fatalf("dump meta: %s", err)
			}
			fp.Sync()
		})

		b.Run("DumpV2 "+name, func(b *testing.B) {
			path := fmt.Sprintf("%s.v2.dump", name)
			fp, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				b.Fatalf("open file %s: %s", path, err)
			}
			defer fp.Close()

			b.ResetTimer()
			if err = m.DumpMetaV2(Background(), fp, &DumpOption{Threads: 10}); err != nil {
				b.Fatalf("dump meta: %s", err)
			}
			fp.Sync()

			b.StopTimer()
			bak := &bakFormat{}
			fp2, err := os.Open(path)
			if err != nil {
				b.Fatalf("open file: %s", path)
			}
			defer fp2.Close()
			footer, err := bak.readFooter(fp2)
			if err != nil {
				b.Fatalf("read footer: %s", err)
			}
			for name, info := range footer.msg.Infos {
				b.Logf("segment: %s, num: %d", name, info.Num)
			}
			b.StartTimer()
		})

		b.Run("LoadV2 "+name, func(b *testing.B) {
			path := fmt.Sprintf("%s.v2.dump", name)
			if err := m.Reset(); err != nil {
				b.Fatalf("reset meta: %s", err)
			}
			fp, err := os.Open(path)
			if err != nil {
				b.Fatalf("open file: %s", path)
			}
			defer fp.Close()

			b.ResetTimer()
			if err = m.LoadMetaV2(Background(), fp, &LoadOption{Threads: 10}); err != nil {
				b.Fatalf("load meta: %s", err)
			}
		})
	}
}
*/
