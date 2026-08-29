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

package main

// Golden-file tests against the REAL Lance fixture in testdata/lance-dataset.
//
// The fixture is written by the official pylance library (gen-fixtures.py,
// pinned version) and the expected values in testdata/expected.json are
// extracted from its bytes by an independent parser (dump_fixture.py). These
// tests are the only proof that the resolver agrees with the actual Lance
// format; the synthetic-byte tests in resolver_test.go only cover error and
// edge cases (see the note at the top of that file).
//
// The writer permutes which content lands in which random file name across
// regenerations, so file identity is not stable. All assertions therefore
// compare MULTISETS of per-file values (range strings, resolved targets),
// which is invariant under that permutation — the same normalization
// dump_fixture.py applies when assigning FILE0/FILE1 placeholders.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	goldenDatasetDir  = "testdata/lance-dataset"
	goldenExpectedDoc = "testdata/expected.json"
)

type goldenRange [2]uint64

type goldenColumn struct {
	Name        string        `json:"name"`
	Leaf        bool          `json:"leaf"`
	CmPos       uint64        `json:"cm_pos"`
	CmLen       uint64        `json:"cm_len"`
	RangesMeta  []goldenRange `json:"ranges_meta"`
	RangesPages []goldenRange `json:"ranges_pages"`
}

type goldenFooter struct {
	ColumnMetadataStart uint64    `json:"column_metadata_start"`
	CmoTableStart       uint64    `json:"cmo_table_start"`
	GboTableStart       uint64    `json:"gbo_table_start"`
	NumGlobalBuffers    uint32    `json:"num_global_buffers"`
	NumColumns          uint32    `json:"num_columns"`
	FooterVersion       [2]uint16 `json:"footer_version"`
}

type goldenFile struct {
	Placeholder      string                  `json:"placeholder"`
	FileMajorVersion uint32                  `json:"file_major_version"`
	Footer           goldenFooter            `json:"footer"`
	Columns          []goldenColumn          `json:"columns"`
	Warmup           map[string]goldenWarmup `json:"warmup"`
}

// goldenWarmup holds the expected byte ranges for one requestable column
// name, including nested (list/struct) columns resolved via subtree
// expansion.
type goldenWarmup struct {
	RangesMeta  []goldenRange `json:"ranges_meta"`
	RangesPages []goldenRange `json:"ranges_pages"`
}

type goldenManifest struct {
	Version         uint64   `json:"version"`
	ManifestPos     uint64   `json:"manifest_pos"`
	FooterVersion   [2]int16 `json:"footer_version"`
	TransactionFile *string  `json:"transaction_file"`
}

type goldenExternalFile struct {
	Placeholder string `json:"placeholder"`
	Offset      uint64 `json:"offset"`
	Size        uint64 `json:"size"`
}

type goldenExpected struct {
	Format        string               `json:"format"`
	Generator     string               `json:"generator"`
	Manifest      goldenManifest       `json:"manifest"`
	Files         []goldenFile         `json:"files"`
	ExternalFiles []goldenExternalFile `json:"external_files"`
	FullResolve   []string             `json:"full_resolve"`
}

func loadGolden(t *testing.T) *goldenExpected {
	t.Helper()
	data, err := os.ReadFile(goldenExpectedDoc)
	if err != nil {
		t.Fatalf("read %s (fixtures are committed; regenerate with gen-fixtures.py + dump_fixture.py if missing): %v", goldenExpectedDoc, err)
	}
	var exp goldenExpected
	if err := json.Unmarshal(data, &exp); err != nil {
		t.Fatalf("parse %s: %v", goldenExpectedDoc, err)
	}
	if exp.Format != "lance-fixture-expected/1" {
		t.Fatalf("unexpected fixture format %q", exp.Format)
	}
	return &exp
}

// rangesBracket renders ranges exactly like resolver output: "a-b;c-d".
func rangesBracket(ranges []goldenRange) string {
	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		parts = append(parts, strconv.FormatUint(r[0], 10)+"-"+strconv.FormatUint(r[1], 10))
	}
	return strings.Join(parts, ";")
}

// lineKind classifies a resolved path line into a stable token, erasing the
// volatile random file names. Anything that is not a manifest, transaction or
// data file counts as an external sequence file; unexpected lines therefore
// surface as an EXTFILE count mismatch.
func lineKind(line string) string {
	switch {
	case strings.HasSuffix(line, lanceManifestExt):
		return "MANIFEST"
	case strings.Contains(line, lanceTransactionsDir):
		return "TXN"
	case strings.Contains(filepath.ToSlash(line), "/"+lanceDataDir+"/"):
		return "DATAFILE"
	default:
		return "EXTFILE"
	}
}

// bracketOf extracts the byte-range part of a warmup line ("path [a-b]" ->
// "a-b"), or "" when the line carries no ranges (full-file fallback).
func bracketOf(line string) string {
	i := strings.Index(line, " [")
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(line[i+2:], "]")
}

func countMultiset(items []string) map[string]int {
	m := make(map[string]int, len(items))
	for _, it := range items {
		m[it]++
	}
	return m
}

func TestGolden_FullResolve(t *testing.T) {
	exp := loadGolden(t)
	paths, err := resolveLanceDataset(goldenDatasetDir, "", false, false, nil, false)
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}

	got := countMultiset(mapStr(paths, lineKind))
	want := map[string]int{"MANIFEST": 1, "DATAFILE": len(exp.Files)}
	if len(exp.ExternalFiles) > 0 {
		want["EXTFILE"] = len(exp.ExternalFiles)
	}
	if exp.Manifest.TransactionFile != nil {
		want["TXN"] = 1
	}
	if len(got) != len(want) {
		t.Fatalf("resolved kinds = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("resolved kinds = %v, want %v", got, want)
		}
	}
}

func TestGolden_ColumnRanges(t *testing.T) {
	exp := loadGolden(t)

	// Every data file in this fixture carries the same schema; the warmup
	// map keys are exactly the requestable column names, nested (list/struct)
	// columns included via subtree expansion.
	names := map[string]bool{}
	for name := range exp.Files[0].Warmup {
		names[name] = true
	}
	if len(names) < 4 {
		t.Fatalf("fixture too small: warmup columns = %v", names)
	}
	// The fixture must cover nested warmup with REAL bytes: a struct column
	// and a list column, not just leaves.
	for _, nested := range []string{"addr", "tags"} {
		if !names[nested] {
			t.Fatalf("fixture warmup map lacks nested column %q: %v", nested, names)
		}
	}

	modes := []struct {
		name   string
		pages  bool
		ranges func(goldenWarmup) []goldenRange
	}{
		{"metadata", false, func(w goldenWarmup) []goldenRange { return w.RangesMeta }},
		{"pages", true, func(w goldenWarmup) []goldenRange { return w.RangesPages }},
	}

	for _, mode := range modes {
		for col := range names {
			t.Run(mode.name+"/"+col, func(t *testing.T) {
				paths, err := resolveLanceDataset(goldenDatasetDir, "", false, false, []string{col}, mode.pages)
				if err != nil {
					t.Fatalf("resolveLanceDataset: %v", err)
				}

				got := []string{}
				for _, p := range paths {
					if lineKind(p) != "DATAFILE" {
						continue
					}
					b := bracketOf(p)
					if b == "" {
						t.Fatalf("column %q: %s fell back to full file, want ranges", col, filepath.Base(p))
					}
					got = append(got, b)
				}

				want := []string{}
				for _, f := range exp.Files {
					if w, ok := f.Warmup[col]; ok {
						want = append(want, rangesBracket(mode.ranges(w)))
					}
				}
				if len(got) != len(want) {
					t.Fatalf("column %q (%s): got %d range lines, want %d", col, mode.name, len(got), len(want))
				}
				if gm, wm := countMultiset(got), countMultiset(want); !equalMultiset(gm, wm) {
					t.Fatalf("column %q (%s): ranges = %v, want %v", col, mode.name, got, want)
				}
			})
		}
	}
}

func TestGolden_UnknownColumnFallsBackToFile(t *testing.T) {
	exp := loadGolden(t)
	paths, err := resolveLanceDataset(goldenDatasetDir, "", false, false, []string{"no-such-column"}, false)
	if err != nil {
		t.Fatalf("resolveLanceDataset: %v", err)
	}
	dataLines := 0
	for _, p := range paths {
		if lineKind(p) == "DATAFILE" {
			dataLines++
		}
	}
	if dataLines != len(exp.Files) {
		t.Fatalf("got %d data lines, want %d", dataLines, len(exp.Files))
	}
}

func mapStr(in []string, f func(string) string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func equalMultiset(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
