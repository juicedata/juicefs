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

package chunk

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestChecksum(t *testing.T) {
	conf := testConf()
	conf.FreeSpace = 0.01
	conf.CacheEviction = EvictionNone
	defer os.RemoveAll(conf.CacheDir)
	m := new(cacheManagerMetrics)
	m.initMetrics()
	s := newDiskCache(m, conf.CacheDir, 1<<30, conf.CacheItems, 1, &conf, nil)
	k1 := "0_0_10" // no checksum
	k2 := "1_0_10"
	k3 := "2_1_102400"
	k4 := "3_5_102400" // corrupt data
	k5 := "4_8_1048576"

	p := NewPage([]byte("helloworld"))
	defer p.Release()
	s.cache(k1, p, true, false)

	s.checksum = CsFull
	s.cache(k2, p, true, false)

	buf := make([]byte, 102400)
	utils.RandRead(buf)
	s.cache(k3, NewPage(buf), true, false)

	fpath := s.cachePath(k4)
	dir := filepath.Dir(fpath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir parent dir %s: %s", dir, err)
	}
	f, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE, s.mode)
	if err != nil {
		t.Fatalf("Create cache file %s: %s", fpath, err)
	}
	if _, err = f.Write(buf); err != nil {
		_ = f.Close()
		t.Fatalf("Write cache file %s: %s", fpath, err)
	}
	corrupt := make([]byte, 102400)
	copy(corrupt, buf)
	for i := 98304; i < 102400; i++ { // reset 96K ~ 100K
		corrupt[i] = 0
	}
	if _, err = f.Write(checksum(corrupt)); err != nil {
		_ = f.Close()
		t.Fatalf("Write checksum to cache file %s: %s", fpath, err)
	}
	_ = f.Close()
	s.add(k4, 102400, uint32(time.Now().Unix()))

	buf = make([]byte, 1048576)
	utils.RandRead(buf)
	s.cache(k5, NewPage(buf), true, false)
	time.Sleep(time.Second * 5) // wait for cache file flushed

	check := func(key string, off int64, size int) error {
		rc, err := s.load(key)
		if err != nil {
			t.Logf("CacheStore files in %s:", s.dir)
			filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					t.Logf("error accessing %s: %v", path, err)
					return nil
				}
				t.Logf("cache file: %s", path)
				return nil
			})
			t.Fatalf("CacheStore load key %s: %s", key, err)
		}
		defer rc.Close()
		buf := make([]byte, size)
		_, err = rc.ReadAt(buf, off)
		return err
	}
	cases := []struct {
		key    string
		off    int64
		size   int
		expect bool
	}{
		{k1, 0, 10, true},
		{k1, 3, 5, true},
		{k2, 0, 10, true},
		{k2, 3, 5, true},
		{k3, 0, 102400, true},
		{k3, 8192, 92160, true}, // 8K ~ 98K
		{k4, 0, 102400, true},
		{k4, 8192, 92160, true}, // only CsExtend can detect the error
		{k5, 0, 1048576, true},
		{k5, 131072, 131072, true},
		{k5, 102400, 512000, true},
	}
	for _, l := range []string{CsNone, CsFull, CsShrink, CsExtend} {
		s.checksum = l
		if l != CsNone {
			cases[6].expect = false
		}
		if l == CsExtend {
			cases[7].expect = false
		}
		for _, c := range cases {
			if err = check(c.key, c.off, c.size); (err == nil) != c.expect {
				t.Fatalf("CacheStore check level %s case %+v: %s", l, c, err)
			}
		}
	}
}

func TestOpenCacheFileReads(t *testing.T) {
	cases := []struct {
		name        string
		dataSize    int
		hasChecksum bool
		hasFooter   bool
		tier        uint8
		openLevel   string
		wantCsLevel string
		wantTier    uint8
	}{
		{name: "data only", dataSize: 64 << 10, openLevel: CsFull, wantCsLevel: CsNone},
		{name: "checksum only", dataSize: 64 << 10, hasChecksum: true, openLevel: CsFull, wantCsLevel: CsFull},
		{name: "checksum and tier", dataSize: 64 << 10, hasChecksum: true, hasFooter: true, tier: 2, openLevel: CsFull, wantCsLevel: CsFull, wantTier: 2},
		{name: "tier only", dataSize: 1 << 10, hasFooter: true, tier: 3, openLevel: CsNone, wantCsLevel: CsNone, wantTier: 3},
		// Regression: 80KiB data => checksumLength = 12 bytes, close to the
		// trailer size; a tier-only file must not be mistaken for checksum-only.
		{name: "tier without checksum no collision", dataSize: 80 << 10, hasFooter: true, tier: 2, openLevel: CsExtend, wantCsLevel: CsNone, wantTier: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := make([]byte, c.dataSize)
			utils.RandRead(data)

			path := filepath.Join(t.TempDir(), "cache")
			f, err := os.Create(path)
			require.NoError(t, err)
			_, err = f.Write(data)
			require.NoError(t, err)
			if c.hasChecksum {
				_, err = f.Write(checksum(data))
				require.NoError(t, err)
			}
			if c.hasFooter {
				_, err = f.Write(marshalFooter(t, c.tier, c.hasChecksum))
				require.NoError(t, err)
			}
			require.NoError(t, f.Close())

			cf, err := openCacheFile(path, len(data), c.openLevel)
			require.NoError(t, err)
			defer cf.Close()
			require.Equal(t, c.wantCsLevel, cf.csLevel)

			got := make([]byte, len(data))
			n, err := cf.ReadAt(got, 0)
			require.NoError(t, err)
			require.Equal(t, len(data), n)
			require.Equal(t, data, got)

			var ft stageFooter
			if c.hasFooter {
				require.NotZero(t, cf.footerOff)
				require.NoError(t, ft.unmarshal(cf))
				require.Equal(t, c.wantTier, ft.Tier)
			} else {
				// A file without a footer defaults to tier 0.
				require.Zero(t, cf.footerOff)
				require.NoError(t, ft.unmarshal(cf))
				require.Equal(t, uint8(0), ft.Tier)
			}
		})
	}
}

func marshalFooter(t *testing.T, tier uint8, hasChecksum bool) []byte {
	t.Helper()
	f := stageFooter{Tier: tier}
	b, err := f.marshal(hasChecksum)
	require.NoError(t, err)
	return b
}

// decodeStageFooter parses a marshaled footer ([magic][len][msgpack]) and
// returns the decoded metadata, verifying the framing along the way.
func decodeStageFooter(t *testing.T, b []byte) stageFooter {
	t.Helper()
	require.GreaterOrEqual(t, len(b), 4)
	require.Equal(t, stageFooterMagic, binary.BigEndian.Uint16(b[:2]))
	size := int(binary.BigEndian.Uint16(b[2:4]))
	require.Equal(t, size, len(b)-4)
	var m stageFooter
	require.NoError(t, msgpack.Unmarshal(b[4:], &m))
	return m
}

func TestEncodeStageFooterLengthParity(t *testing.T) {
	// The encoded length must be a multiple of 4 iff a checksum is present, and
	// the stored tier must round-trip regardless of the padding.
	for tier := uint8(0); tier <= maxTierID; tier++ {
		for _, hasChecksum := range []bool{true, false} {
			b := marshalFooter(t, tier, hasChecksum)
			require.Equal(t, hasChecksum, len(b)%4 == 0,
				"tier=%d hasChecksum=%v len=%d", tier, hasChecksum, len(b))

			require.Equal(t, tier, decodeStageFooter(t, b).Tier)
		}
	}
}

func TestOpenCacheFileRejectsInvalidSize(t *testing.T) {
	cases := []struct {
		name      string
		dataSize  int
		truncate  int    // bytes dropped from the end of the data
		trailer   []byte // extra bytes appended after the data
		openLevel string
	}{
		{
			// 40KiB data => checksumLength = 8 bytes; an "extra" that is a
			// multiple of 4 but smaller than the checksum length is impossible.
			name:      "extra smaller than checksum",
			dataSize:  40 << 10,
			trailer:   []byte{0, 0, 0, 0},
			openLevel: CsFull,
		},
		{
			name:      "truncated data",
			dataSize:  1 << 10,
			truncate:  1,
			openLevel: CsNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := make([]byte, c.dataSize)
			utils.RandRead(data)

			content := make([]byte, 0, len(data))
			content = append(content, data[:len(data)-c.truncate]...)
			content = append(content, c.trailer...)
			path := filepath.Join(t.TempDir(), "cache")
			require.NoError(t, os.WriteFile(path, content, 0600))

			_, err := openCacheFile(path, len(data), c.openLevel)
			require.Error(t, err)
		})
	}
}

func TestOpenCacheFileParsesStageFooter(t *testing.T) {
	data := make([]byte, 1024)
	utils.RandRead(data)

	badMagic := marshalFooter(t, 2, false)
	badMagic[0] ^= 0xff // corrupt the magic without changing the length

	cases := []struct {
		name     string
		footer   []byte
		wantErr  bool
		wantTier uint8
	}{
		{name: "truncated header", footer: []byte{0x46}, wantErr: true},                                // shorter than the 4-byte header
		{name: "bad magic", footer: badMagic, wantErr: true},                                           // valid length, wrong magic
		{name: "trailing data", footer: append(marshalFooter(t, 2, false), 0, 0, 0, 0), wantErr: true}, // extra bytes after the footer
		{name: "invalid tier clamped to zero", footer: marshalFooter(t, 9, false), wantTier: 0},        // out-of-range tier -> default
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache")
			f, err := os.Create(path)
			require.NoError(t, err)
			_, err = f.Write(data)
			require.NoError(t, err)
			_, err = f.Write(c.footer)
			require.NoError(t, err)
			require.NoError(t, f.Close())

			// openCacheFile only validates sizes; the footer content is
			// validated when it is unmarshaled.
			cf, err := openCacheFile(path, len(data), CsNone)
			require.NoError(t, err)
			defer cf.Close()

			var ft stageFooter
			err = ft.unmarshal(cf)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wantTier, ft.Tier)
		})
	}
}

// writeStageFile writes a stage file laid out as [data][checksum?][footer] and
// returns its path.
func writeStageFile(tb testing.TB, dir, name string, data []byte, tier uint8, hasChecksum bool) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(tb, err)
	_, err = f.Write(data)
	require.NoError(tb, err)
	if hasChecksum {
		_, err = f.Write(checksum(data))
		require.NoError(tb, err)
	}
	sf := stageFooter{Tier: tier}
	fb, err := sf.marshal(hasChecksum)
	require.NoError(tb, err)
	_, err = f.Write(fb)
	require.NoError(tb, err)
	require.NoError(tb, f.Close())
	return path
}

func TestStageFooterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 64*1024)
	utils.RandRead(data)
	for tier := uint8(0); tier <= maxTierID; tier++ {
		for _, hasChecksum := range []bool{true, false} {
			name := fmt.Sprintf("tier%d_cs%v", tier, hasChecksum)
			t.Run(name, func(t *testing.T) {
				path := writeStageFile(t, dir, name, data, tier, hasChecksum)
				level := CsNone
				if hasChecksum {
					level = CsFull
				}

				cf, err := openCacheFile(path, len(data), level)
				require.NoError(t, err)
				defer cf.Close()

				got := make([]byte, len(data))
				n, err := cf.ReadAt(got, 0)
				require.NoError(t, err)
				require.Equal(t, len(data), n)
				require.Equal(t, data, got)

				var ft stageFooter
				require.NoError(t, ft.unmarshal(cf))
				require.Equal(t, tier, ft.Tier)
			})
		}
	}
}
