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

package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

// nolint:errcheck
func TestUsageReport(t *testing.T) {
	// invalid addr
	reportUrl = "http://127.0.0.1/report-usage"
	m := meta.NewClient("memkv://", nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = m.Init(format, true)
	go ReportUsage(m, "unittest")
	// wait for it to report to unavailable address, it should not panic.
	time.Sleep(time.Millisecond * 100)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	mux := http.NewServeMux()
	var u usage
	done := make(chan bool)
	mux.HandleFunc("/report-usage", func(rw http.ResponseWriter, r *http.Request) {
		d, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(d, &u)
		_, _ = rw.Write([]byte("OK"))
		done <- true
	})
	go http.Serve(l, mux)

	addr := l.Addr().String()
	reportUrl = fmt.Sprintf("http://%s/report-usage", addr)
	go ReportUsage(m, "unittest")

	deadline := time.NewTimer(time.Second * 3)
	select {
	case <-done:
		if u.MetaEngine != "memkv" {
			t.Fatalf("unexpected meta engine: %s", u.MetaEngine)
		}
		if u.Version != "unittest" {
			t.Fatalf("unexpected version: %s", u.Version)
		}
	case <-deadline.C:
		t.Fatalf("no report after 3 seconds")
	}
	time.Sleep(time.Millisecond * 100) // wait for the client to finish
}
