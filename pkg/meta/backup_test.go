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
	"path/filepath"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta/pb"
)

func TestDumpMetaV2RecordsSource(t *testing.T) {
	m := NewClient("sqlite3://"+filepath.Join(t.TempDir(), "meta.db"), nil)
	defer m.Shutdown()
	if err := m.Init(&Format{Name: "test"}, true); err != nil {
		t.Fatalf("init metadata: %v", err)
	}

	var data bytes.Buffer
	if err := m.DumpMetaV2(Background(), &data, &DumpOption{Threads: 1}); err != nil {
		t.Fatalf("dump metadata: %v", err)
	}
	footer, err := (&BakFormat{}).ReadFooter(bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatalf("read footer: %v", err)
	}
	if footer.Msg.Source != pb.Footer_SQL {
		t.Fatalf("source: got %s, want %s", footer.Msg.Source, pb.Footer_SQL)
	}
}

func TestBackupCountersBySource(t *testing.T) {
	newCounters := func() (DumpedCounters, []*pb.Counter, []*pb.Counter) {
		counters := DumpedCounters{NextInode: 2, NextChunk: 1}
		var others, dumped []*pb.Counter
		counterSeg := newBakSegment(&pb.Batch{Counters: []*pb.Counter{
			{Key: usedSpace, Value: 7},
			{Key: totalInodes, Value: 8},
			{Key: "nextInode", Value: 9},
		}})
		dumped = append(dumped, counterSeg.val.(*pb.Batch).Counters...)
		counters.updateFromSegment(counterSeg, &others)
		counters.updateFromSegment(newBakSegment(&pb.Batch{Nodes: []*pb.Node{
			{Inode: 3, Data: (&Attr{Typ: TypeFile, Length: 1}).Marshal()},
		}}), &others)
		return counters, others, dumped
	}

	tests := []struct {
		name           string
		source         pb.Footer_Engine
		wantUsedSpace  int64
		wantUsedInodes int64
	}{
		{"redis", pb.Footer_REDIS, 7, 8},
		{"sql", pb.Footer_SQL, 4096, 1},
		{"kv", pb.Footer_KV, 4096, 1},
		{"unknown", pb.Footer_UNKNOWN, 4096, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			counters, others, dumped := newCounters()
			batch := &pb.Batch{Counters: dumped}
			if test.source != pb.Footer_REDIS {
				batch = counters.toBatch(others)
			}
			values := make(map[string]int64)
			for _, counter := range batch.Counters {
				values[counter.Key] = counter.Value
			}
			if values[usedSpace] != test.wantUsedSpace {
				t.Errorf("used space: got %d, want %d", values[usedSpace], test.wantUsedSpace)
			}
			if values[totalInodes] != test.wantUsedInodes {
				t.Errorf("used inodes: got %d, want %d", values[totalInodes], test.wantUsedInodes)
			}
		})
	}
}

func makeCounterBackup(t *testing.T, source pb.Footer_Engine) []byte {
	t.Helper()
	data := &bytes.Buffer{}
	bak := newBakFormat()
	bak.Footer.Msg.Source = source
	if err := bak.writeSegment(data, newBakSegment(&pb.Batch{Counters: []*pb.Counter{
		{Key: usedSpace, Value: 7},
		{Key: totalInodes, Value: 8},
	}})); err != nil {
		t.Fatalf("write counters: %v", err)
	}
	if err := bak.writeSegment(data, newBakSegment(&pb.Batch{Nodes: []*pb.Node{
		{Inode: 3, Data: (&Attr{Typ: TypeFile, Length: 1}).Marshal()},
	}})); err != nil {
		t.Fatalf("write nodes: %v", err)
	}
	if err := bak.writeFooter(data); err != nil {
		t.Fatalf("write footer: %v", err)
	}
	return data.Bytes()
}

func TestLoadMetaV2CountersBySource(t *testing.T) {
	tests := []struct {
		name           string
		source         pb.Footer_Engine
		wantUsedSpace  int64
		wantUsedInodes int64
	}{
		{"redis", pb.Footer_REDIS, 7, 8},
		{"sql", pb.Footer_SQL, 4096, 1},
		{"kv", pb.Footer_KV, 4096, 1},
		{"old footer", pb.Footer_UNKNOWN, 4096, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := NewClient("sqlite3://"+filepath.Join(t.TempDir(), "meta.db"), nil)
			defer m.Shutdown()
			if err := m.LoadMetaV2(Background(), bytes.NewReader(makeCounterBackup(t, test.source)), &LoadOption{Threads: 1}); err != nil {
				t.Fatalf("load backup: %v", err)
			}
			for name, expected := range map[string]int64{
				usedSpace:   test.wantUsedSpace,
				totalInodes: test.wantUsedInodes,
			} {
				actual, err := m.getBase().en.getCounter(name)
				if err != nil {
					t.Fatalf("get counter %s: %v", name, err)
				}
				if actual != expected {
					t.Errorf("counter %s: got %d, want %d", name, actual, expected)
				}
			}
		})
	}
}

func TestKVLoadMetaV2CountersBySource(t *testing.T) {
	tests := []struct {
		name           string
		source         pb.Footer_Engine
		wantUsedSpace  int64
		wantUsedInodes int64
	}{
		{"redis", pb.Footer_REDIS, 7, 8},
		{"sql", pb.Footer_SQL, 4096, 1},
		{"old footer", pb.Footer_UNKNOWN, 4096, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := NewClient("badger://"+filepath.Join(t.TempDir(), "meta"), nil)
			defer m.Shutdown()
			if err := m.LoadMetaV2(Background(), bytes.NewReader(makeCounterBackup(t, test.source)), &LoadOption{Threads: 1}); err != nil {
				t.Fatalf("load backup: %v", err)
			}
			for name, expected := range map[string]int64{
				usedSpace:   test.wantUsedSpace,
				totalInodes: test.wantUsedInodes,
			} {
				actual, err := m.getBase().en.getCounter(name)
				if err != nil {
					t.Fatalf("get counter %s: %v", name, err)
				}
				if actual != expected {
					t.Errorf("counter %s: got %d, want %d", name, actual, expected)
				}
			}
		})
	}
}
