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

package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/juicedata/juicefs/pkg/meta"

	"github.com/agiledragon/gomonkey/v2"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/urfave/cli/v2"
)

func Test_exposeMetrics(t *testing.T) {
	Convey("Test_exposeMetrics", t, func() {
		Convey("Test_exposeMetrics", func() {
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

			metricsAddr := exposeMetrics(client, appCtx)

			u := url.URL{Scheme: "http", Host: metricsAddr, Path: "/metrics"}
			resp, err := http.Get(u.String())
			So(err, ShouldBeNil)
			all, err := ioutil.ReadAll(resp.Body)
			So(err, ShouldBeNil)
			So(string(all), ShouldNotBeBlank)
		})
	})
}

func MountTmp(metaUrl, mountpoint string) {
	formatArgs := []string{"", "format", "--storage", "file", "--bucket", "/tmp/testMountDir", metaUrl, "test"}
	Main(formatArgs)

	mountArgs := []string{"", "mount", metaUrl, mountpoint}
	go Main(mountArgs)
	time.Sleep(2 * time.Second)
}
func CleanRedis(metaUrl string) {
	opt, _ := redis.ParseURL(metaUrl)
	rdb := redis.NewClient(opt)
	rdb.FlushDB(context.Background())
}

func TestMount(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	MountTmp(metaUrl, mountpoint)
	err := ioutil.WriteFile(fmt.Sprintf("%s/f1.txt", mountpoint), []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Test mount failed: %v", err)
	}

	defer CleanRedis(metaUrl)
}
