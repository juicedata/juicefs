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

	"github.com/prometheus/client_golang/prometheus"

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
			ResetPrometheus()
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

func ResetPrometheus() {
	http.DefaultServeMux = http.NewServeMux()
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
}

func MountTmp(metaUrl, mountpoint string) error {
	ResetRedis(metaUrl)
	formatArgs := []string{"", "format", "--storage", "file", "--bucket", "/tmp/testMountDir", metaUrl, "test"}
	err := Main(formatArgs)
	if err != nil {
		return err
	}
	mountArgs := []string{"", "mount", metaUrl, mountpoint}

	//Must be reset, otherwise panic will appear
	ResetPrometheus()

	go func() {
		err := Main(mountArgs)
		if err != nil {
			fmt.Printf("mount failed: %v", err)
		}
	}()
	time.Sleep(2 * time.Second)
	return nil
}
func ResetRedis(metaUrl string) {
	opt, _ := redis.ParseURL(metaUrl)
	rdb := redis.NewClient(opt)
	rdb.FlushDB(context.Background())
}

func TestMount(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	defer ResetRedis(metaUrl)
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	defer func(mountpoint string) {
		err := UmountTmp(mountpoint)
		if err != nil {
			t.Fatalf("umount failed: %v", err)
		}
	}(mountpoint)

	err := ioutil.WriteFile(fmt.Sprintf("%s/f1.txt", mountpoint), []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Test mount failed: %v", err)
	}

}
