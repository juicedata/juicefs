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

package meta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta/pb"
	"google.golang.org/protobuf/proto"
)

func newBackupTestMeta(t *testing.T, engine string) Meta {
	t.Helper()
	uri := engine + "://" + filepath.Join(t.TempDir(), "meta")
	if engine == "redis" {
		uri = os.Getenv("REDIS_ADDR")
		if uri == "" {
			uri = "redis://127.0.0.1:6379/14"
		}
	}
	m := NewClient(uri, nil)
	if err := m.Reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Reset(); err != nil {
			t.Error(err)
		}
		if err := m.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestDumpMetaV2RecordsSource(t *testing.T) {
	for engine, source := range map[string]pb.Footer_Engine{"redis": pb.Footer_REDIS, "sqlite3": pb.Footer_SQL, "badger": pb.Footer_KV} {
		t.Run(engine, func(t *testing.T) {
			m := newBackupTestMeta(t, engine)
			if err := m.Init(&Format{Name: "test"}, true); err != nil {
				t.Fatal(err)
			}
			var data bytes.Buffer
			if err := m.DumpMetaV2(Background(), &data, &DumpOption{Threads: 1}); err != nil {
				t.Fatal(err)
			}
			footer, err := (&BakFormat{}).ReadFooter(bytes.NewReader(data.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if footer.Msg.Source != source {
				t.Fatalf("source: got %s, want %s", footer.Msg.Source, source)
			}
		})
	}
}

func makeCounterBackup(t *testing.T, source pb.Footer_Engine) []byte {
	t.Helper()
	var data bytes.Buffer
	bak := newBakFormat()
	bak.Footer.Msg.Source = source
	for _, batch := range []*pb.Batch{
		{Counters: []*pb.Counter{
			{Key: usedSpace, Value: 7}, {Key: totalInodes, Value: 8},
			{Key: "nextInode", Value: 9}, {Key: "nextChunk", Value: 10},
		}},
		{Nodes: []*pb.Node{{Inode: 23, Data: (&Attr{Typ: TypeFile, Length: 1, Nlink: 1}).Marshal()}}},
		{SliceRefs: []*pb.SliceRef{{Id: 30, Size: 1, Refs: 2}}},
		{Counters: []*pb.Counter{{Key: "nextSession", Value: 6}, {Key: "nextTrash", Value: 4}, {Key: "otherCounter", Value: 17}}},
	} {
		if err := bak.writeSegment(&data, newBakSegment(batch)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bak.writeFooter(&data); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type backupShortReader struct{ io.Reader }

func (r *backupShortReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func backupTestReader(t *testing.T, data []byte, input string) io.Reader {
	t.Helper()
	switch input {
	case "seekable":
		return bytes.NewReader(data)
	case "reader":
		return bytes.NewBuffer(data)
	case "short reads":
		return &backupShortReader{bytes.NewReader(data)}
	case "pipe":
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		if _, err := w.Write(data); err != nil {
			w.Close()
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return r
	default:
		t.Fatalf("unknown input %q", input)
		return nil
	}
}

func TestLoadMetaV2CountersBySource(t *testing.T) {
	for _, engine := range []string{"redis", "sqlite3", "badger"} {
		t.Run(engine, func(t *testing.T) {
			for _, source := range []pb.Footer_Engine{pb.Footer_REDIS, pb.Footer_SQL, pb.Footer_KV, pb.Footer_UNKNOWN} {
				t.Run(source.String(), func(t *testing.T) {
					for _, input := range []string{"seekable", "reader", "short reads", "pipe"} {
						t.Run(input, func(t *testing.T) {
							m := newBackupTestMeta(t, engine)
							if err := m.LoadMetaV2(Background(), backupTestReader(t, makeCounterBackup(t, source), input), &LoadOption{Threads: 2}); err != nil {
								t.Fatal(err)
							}
							want := map[string]int64{usedSpace: 4096, totalInodes: 1, "nextInode": 24, "nextChunk": 31, "nextSession": 6, "nextTrash": 4, "otherCounter": 17}
							if source == pb.Footer_REDIS {
								want[usedSpace] = 7
								want[totalInodes] = 8
								want["nextInode"] = 9
								want["nextChunk"] = 10
							}
							for name, expected := range want {
								if engine == "redis" && (name == "nextInode" || name == "nextChunk") {
									expected--
								}
								got, err := m.getBase().en.getCounter(name)
								if err != nil {
									t.Fatal(err)
								}
								if got != expected {
									t.Errorf("%s: got %d, want %d", name, got, expected)
								}
							}
						})
					}
				})
			}
		})
	}
}

func replaceBackupFooter(t *testing.T, data []byte, change func(*pb.Footer)) []byte {
	t.Helper()
	length := int(binary.BigEndian.Uint64(data[len(data)-8:]))
	offset := len(data) - 8 - length
	footer := &pb.Footer{}
	if err := proto.Unmarshal(data[offset:len(data)-8], footer); err != nil {
		t.Fatal(err)
	}
	change(footer)
	result := bytes.NewBuffer(append([]byte(nil), data[:offset]...))
	if err := (&BakFooter{Msg: footer}).Marshal(result); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

type backupErrorReader struct{ err error }

func (r backupErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestLoadMetaV2InvalidFooter(t *testing.T) {
	data := makeCounterBackup(t, pb.Footer_REDIS)
	readErr := errors.New("backup read failed")
	invalidProto := append([]byte(nil), data...)
	footerStart := len(data) - 8 - int(binary.BigEndian.Uint64(data[len(data)-8:]))
	invalidProto[footerStart] = 0xff
	cases := []struct {
		name string
		data []byte
		err  string
	}{
		{"truncated", data[:len(data)-5], "footer length"},
		{"missing", data[:len(data)-8-int(binary.BigEndian.Uint64(data[len(data)-8:]))], "footer length"},
		{"oversized", append(append([]byte(nil), data[:len(data)-8]...), bytes.Repeat([]byte{255}, 8)...), "footer length"},
		{"magic", replaceBackupFooter(t, data, func(f *pb.Footer) { f.Magic = 0 }), "magic"},
		{"version", replaceBackupFooter(t, data, func(f *pb.Footer) { f.Version = BakVersion + 1 }), "version"},
		{"source", replaceBackupFooter(t, data, func(f *pb.Footer) { f.Source = 99 }), "source"},
		{"protobuf", invalidProto, "unmarshal footer"},
		{"read error", data, readErr.Error()},
	}
	for _, engine := range []string{"redis", "sqlite3", "badger"} {
		t.Run(engine, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					m := newBackupTestMeta(t, engine)
					var r io.Reader = bytes.NewBuffer(tc.data)
					if tc.name == "read error" {
						r = io.MultiReader(r, backupErrorReader{readErr})
					}
					ctx := Background()
					err := m.LoadMetaV2(ctx, r, &LoadOption{Threads: 2})
					if err == nil || !strings.Contains(err.Error(), tc.err) {
						t.Fatalf("got %v, want error containing %q", err, tc.err)
					}
					if tc.name == "read error" && !errors.Is(err, readErr) {
						t.Fatalf("lost read error: %v", err)
					}
					if !ctx.Canceled() {
						t.Fatal("load context was not canceled")
					}
					// LoadMetaV2 waits for its workers before returning; no counter task may escape.
					got, err := m.getBase().en.getCounter(usedSpace)
					if err != nil {
						t.Fatal(err)
					}
					if got != 0 {
						t.Fatalf("counter written before validating footer: %d", got)
					}
				})
			}
		})
	}
}

type backupTestSeeker struct {
	*bytes.Reader
	short   bool
	failAt  int
	seeks   int
	readErr error
}

func (r *backupTestSeeker) Seek(offset int64, whence int) (int64, error) {
	r.seeks++
	if r.seeks == r.failAt {
		return 0, errors.New("seek failed")
	}
	return r.Reader.Seek(offset, whence)
}
func (r *backupTestSeeker) Read(p []byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}
	if r.short && len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestBackupReadFooter(t *testing.T) {
	data := makeCounterBackup(t, pb.Footer_SQL)
	for _, tc := range []struct {
		name    string
		data    []byte
		failAt  int
		short   bool
		readErr error
		wantErr bool
	}{
		{name: "short reads", data: data, short: true},
		{name: "length seek", data: data, failAt: 1, wantErr: true},
		{name: "footer seek", data: data, failAt: 2, wantErr: true},
		{name: "read error", data: data, readErr: io.ErrUnexpectedEOF, wantErr: true},
		{name: "truncated", data: data[:4], wantErr: true},
		{name: "oversized", data: append(append([]byte(nil), data[:len(data)-8]...), bytes.Repeat([]byte{255}, 8)...), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &backupTestSeeker{Reader: bytes.NewReader(tc.data), short: tc.short, failAt: tc.failAt, readErr: tc.readErr}
			_, err := (&BakFormat{}).ReadFooter(r)
			if (err != nil) != tc.wantErr {
				t.Fatalf("got %v, want error: %t", err, tc.wantErr)
			}
		})
	}
}
