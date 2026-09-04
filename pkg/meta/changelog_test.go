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
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestLogEncodeDecode(t *testing.T) {
	cases := [][]byte{
		{},
		[]byte("plain"),
		[]byte("with,comma"),
		[]byte("with(paren)"),
		[]byte("with|pipe"),
		[]byte("with%percent"),
		[]byte(`with"quote\backslash`),
		[]byte("with\x00\x01\x7f\xff"),
		[]byte("中文名字"),
		[]byte("a|b,c(d)e%f\"g\\h"),
	}
	for _, c := range cases {
		enc := logEncode(c)
		if strings.ContainsAny(enc, ",()|\"\\") {
			t.Fatalf("logEncode(%q) = %q still contains a separator", c, enc)
		}
		dec, err := logDecode(enc)
		if err != nil {
			t.Fatalf("logDecode(%q): %s", enc, err)
		}
		if string(dec) != string(c) {
			t.Fatalf("round-trip of %q got %q", c, dec)
		}
	}
}

func TestLogEncodeDecodeFuzz(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	buf := make([]byte, 64)
	for i := 0; i < 2000; i++ {
		n := r.Intn(len(buf))
		for j := 0; j < n; j++ {
			buf[j] = byte(r.Intn(256))
		}
		raw := buf[:n]
		dec, err := logDecode(logEncode(raw))
		if err != nil {
			t.Fatalf("logDecode of %q: %s", raw, err)
		}
		if string(dec) != string(raw) {
			t.Fatalf("round-trip of %q got %q", raw, dec)
		}
	}
}

func TestLogDecodeInvalid(t *testing.T) {
	for _, s := range []string{"%", "%A", "%ZZ", "abc%1", "a%GG"} {
		if _, err := logDecode(s); err == nil {
			t.Fatalf("logDecode(%q) should fail", s)
		}
	}
}

func TestParseChangeEntry(t *testing.T) {
	cases := []struct {
		entry  string
		op     string
		args   []string
		result []string
		sid    uint64
		txn    uint64
	}{
		{
			entry:  "1716440752.123456789|CREATE(1,report.txt,1000,1000,1,420,18,,Keep,true):1024|(3,88)",
			op:     OpCreate,
			args:   []string{"1", "report.txt", "1000", "1000", "1", "420", "18", "", "Keep", "true"},
			result: []string{"1024"},
			sid:    3,
			txn:    88,
		},
		{
			entry:  "1716440753.000000000|WRITE(1024,0,0,233344,4096,1716440753,0):1|(3,89)",
			op:     OpWrite,
			args:   []string{"1024", "0", "0", "233344", "4096", "1716440753", "0"},
			result: []string{"1"},
			sid:    3,
			txn:    89,
		},
		{
			entry: "1716440760.000000000|INIT_ENABLE_DIRSTATS()|(3,90)",
			op:    OpInitDirStats,
			sid:   3,
			txn:   90,
		},
		{
			entry:  "1716440761.000000000|UNLINKBATCH(1,a,b,c,0,true):11,12,13|(3,91)",
			op:     OpUnlinkBatch,
			args:   []string{"1", "a", "b", "c", "0", "true"},
			result: []string{"11", "12", "13"},
			sid:    3,
			txn:    91,
		},
		{ // names containing every separator that logEncode escapes
			entry:  "1716440762.000000000|RMDIR(1," + logEncode2("a|b,c(d)") + ",0):7|(3,92)",
			op:     OpRmdir,
			args:   []string{"1", "a|b,c(d)", "0"},
			result: []string{"7"},
			sid:    3,
			txn:    92,
		},
		{ // legacy entry written before '|' was escaped
			entry:  "1716440763.000000000|RMDIR(1,a|b,0):7|(3,93)",
			op:     OpRmdir,
			args:   []string{"1", "a|b", "0"},
			result: []string{"7"},
			sid:    3,
			txn:    93,
		},
	}
	for _, c := range cases {
		e, err := ParseChangeEntry(100, c.entry)
		if err != nil {
			t.Fatalf("parse %q: %s", c.entry, err)
		}
		if e.Op != c.op {
			t.Fatalf("parse %q: op %q != %q", c.entry, e.Op, c.op)
		}
		if strings.Join(e.Args, "\x00") != strings.Join(c.args, "\x00") {
			t.Fatalf("parse %q: args %q != %q", c.entry, e.Args, c.args)
		}
		if strings.Join(e.Result, "\x00") != strings.Join(c.result, "\x00") {
			t.Fatalf("parse %q: result %q != %q", c.entry, e.Result, c.result)
		}
		if e.Sid != c.sid || e.TxnId != c.txn {
			t.Fatalf("parse %q: (%d,%d) != (%d,%d)", c.entry, e.Sid, e.TxnId, c.sid, c.txn)
		}
		if err := e.Validate(); err != nil {
			t.Fatalf("validate %q: %s", c.entry, err)
		}
	}
}

func TestParseChangeEntryTime(t *testing.T) {
	e, err := ParseChangeEntry(1, "1716440752.123456789|ACCESS(1)|(3,88)")
	if err != nil {
		t.Fatal(err)
	}
	if e.Time.Unix() != 1716440752 || e.Time.Nanosecond() != 123456789 {
		t.Fatalf("unexpected time %v", e.Time)
	}
}

func TestParseChangeEntryInvalid(t *testing.T) {
	for _, s := range []string{
		"",
		"1716440752.0|ACCESS(1)",          // no suffix
		"1716440752.0|ACCESS(1|(3,88)",    // unclosed
		"1716440752.0|ACCESS 1|(3,88)",    // no parenthesis
		"1716440752.0|acce ss(1)|(3,88)",  // invalid op name
		"1716440752.0|ACCESS(1)x|(3,88)",  // trailer
		"1716440752.0|ACCESS(1)|(3)",      // bad suffix
		"1716440752.0|ACCESS(%ZZ)|(3,88)", // bad escape
		"not-a-time|ACCESS(1)|(3,88)",     // bad timestamp
	} {
		if _, err := ParseChangeEntry(1, s); err == nil {
			t.Fatalf("ParseChangeEntry(%q) should fail", s)
		}
	}
}

func TestChangeEntryValidate(t *testing.T) {
	mustParse := func(s string) *ChangeEntry {
		t.Helper()
		e, err := ParseChangeEntry(1, s)
		if err != nil {
			t.Fatalf("parse %q: %s", s, err)
		}
		return e
	}
	if err := mustParse("1.0|NOSUCHOP(1)|(1,1)").Validate(); err == nil {
		t.Fatal("unknown op should not validate")
	}
	if err := mustParse("1.0|ACCESS(1,2)|(1,1)").Validate(); err == nil {
		t.Fatal("wrong arity should not validate")
	}
	if err := mustParse("1.0|ACCESS(1):9|(1,1)").Validate(); err == nil {
		t.Fatal("unexpected result should not validate")
	}
	if err := mustParse("1.0|UNLINKBATCH(1,a,b,0,true):11|(1,1)").Validate(); err == nil {
		t.Fatal("mismatched batch sizes should not validate")
	}
	e := mustParse("1.0|UNLINKBATCH(1,a,b,0,true):11,12|(1,1)")
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if names := e.BatchNames(); strings.Join(names, ",") != "a,b" {
		t.Fatalf("BatchNames() = %q", names)
	}
}

func TestChangeEntryAccessors(t *testing.T) {
	e, err := ParseChangeEntry(1, "1.0|CREATE(1,f,1000,1000,1,420,18,,Keep,true):1024|(3,88)")
	if err != nil {
		t.Fatal(err)
	}
	if ino, err := e.Ino(0); err != nil || ino != 1 {
		t.Fatalf("Ino(0) = %v, %v", ino, err)
	}
	if name, _ := e.Str(1); name != "f" {
		t.Fatalf("Str(1) = %q", name)
	}
	if mode, err := e.Uint16(5); err != nil || mode != 420 {
		t.Fatalf("Uint16(5) = %v, %v", mode, err)
	}
	if b, err := e.Bool(9); err != nil || !b {
		t.Fatalf("Bool(9) = %v, %v", b, err)
	}
	if ino, err := e.ResultIno(0); err != nil || ino != 1024 {
		t.Fatalf("ResultIno(0) = %v, %v", ino, err)
	}
	if _, err := e.Ino(10); err == nil {
		t.Fatal("out of range should fail")
	}
	if _, err := e.Bool(0); err == nil {
		t.Fatal("Bool on a number should fail")
	}
	if _, err := e.Uint8(2); err == nil {
		t.Fatal("Uint8 overflow should fail")
	}
}

// TestChangelogOpsCovered guards against adding a genLog call site without
// registering the operation here.
func TestChangelogOpsCovered(t *testing.T) {
	if len(changelogOps) != 38 {
		t.Fatalf("expect 38 operations, got %d; update the table and this test together", len(changelogOps))
	}
	for op := range changelogOps {
		if !isChangelogOpName(op) {
			t.Fatalf("invalid operation name %q", op)
		}
	}
}

func BenchmarkParseChangeEntry(b *testing.B) {
	entry := fmt.Sprintf("1716440752.123456789|CREATE(1,%s,1000,1000,1,420,18,,Keep,true):1024|(3,88)", logEncode2("report.txt"))
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := ParseChangeEntry(int64(i), entry); err != nil {
			b.Fatal(err)
		}
	}
}
