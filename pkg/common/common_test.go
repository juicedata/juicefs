/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

package common

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"

	. "github.com/smartystreets/goconvey/convey"
)

func TestExposeMetrics(t *testing.T) {
	Convey("TestExposeMetrics", t, func() {
		Convey("TestExposeMetrics", func() {
			addr := "redis://127.0.0.1:6379/10"
			var conf = meta.Config{MaxDeletes: 1}
			client := meta.NewClient(addr, &conf)
			var appCtx *cli.Context
			stringPatches := gomonkey.ApplyMethod(reflect.TypeOf(appCtx), "String", func(_ *cli.Context, arg string) string {
				switch arg {
				case "metrics":
					return "127.0.0.1:9567"
				case "consul":
					return "127.0.0.1:8500"
				default:
					return ""
				}
			})
			isSetPatches := gomonkey.ApplyMethod(reflect.TypeOf(appCtx), "IsSet", func(_ *cli.Context, _ string) bool {
				return false
			})
			defer stringPatches.Reset()
			defer isSetPatches.Reset()

			http.DefaultServeMux = http.NewServeMux()
			prometheus.DefaultRegisterer = prometheus.NewRegistry()

			metricsAddr := ExposeMetrics(client, appCtx)

			u := url.URL{Scheme: "http", Host: metricsAddr, Path: "/metrics"}
			resp, err := http.Get(u.String())
			So(err, ShouldBeNil)
			all, err := ioutil.ReadAll(resp.Body)
			So(err, ShouldBeNil)
			So(string(all), ShouldNotBeBlank)
		})
	})
}

func TestCreateStorage(t *testing.T) {
	format := meta.Format{
		Name:    "test",
		UUID:    uuid.New().String(),
		Storage: "file",
	}
	storage, err := CreateStorage(&format)
	if err != nil {
		t.Fatalf("create storage error: %v", err)
	}
	content := "test content"
	err = storage.Put("test.txt", bytes.NewReader([]byte(content)))
	if err != nil {
		t.Fatalf("storage put error: %v", err)
	}

	r, err := storage.Get("test.txt", 0, -1)
	if err != nil {
		t.Fatalf("storage put error: %v", err)
	}
	defer r.Close()

	c, err := ioutil.ReadAll(r)
	if string(c) != content {
		t.Fatalf("create storage error: %v", err)
	}
}
