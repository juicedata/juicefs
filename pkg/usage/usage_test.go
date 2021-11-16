/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package usage

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	m := meta.NewClient("memkv://", &meta.Config{})
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
		d, _ := ioutil.ReadAll(r.Body)
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
