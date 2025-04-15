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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/sirupsen/logrus"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const sampleFile = "metadata.sample"
const subSampleFile = "metadata-sub.sample"

func TestEscape(t *testing.T) {
	cases := []struct {
		value            []rune
		gbkStart, gbkEnd int
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

	quota, err := m.(engine).doGetQuota(ctx, 1)
	if err != nil {
		t.Fatalf("get quota: %s", err)
	}
	if quota == nil {
		t.Fatalf("get quota: nil")
	}
	if *quota != expectedQuota {
		t.Fatalf("expected: %v, but got: %v", expectedQuota, *quota)
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
	// testLoadDump(t, "mysql", "mysql://root:@/dev")
	testLoadDump(t, "badger", "badger://jfs-load-dump")
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
		"sqlite3": {"sqlite3://dev.db", "sqlite3://dev2.db"},
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
	m2.getBase().scanTrashFiles(Background(), func(inode Ino, size uint64, ts time.Time) (clean bool, err error) {
		cnt++
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
