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

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/go-redis/redis/v8"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/urfave/cli/v2"
)

const testMeta = "redis://127.0.0.1:6379/11"
const testMountPoint = "/tmp/jfs-unit-test"
const testVolume = "test"

// gomonkey may encounter the problem of insufficient permissions under mac, please solve it by viewing this link https://github.com/agiledragon/gomonkey/issues/70
func Test_exposeMetrics(t *testing.T) {
	Convey("Test_exposeMetrics", t, func() {
		Convey("Test_exposeMetrics", func() {
			addr := "redis://127.0.0.1:6379/12"
			client := meta.NewClient(addr, &meta.Config{})
			format := meta.Format{
				Name:      "test",
				BlockSize: 4096,
				Capacity:  1 << 30,
			}
			_ = client.Init(format, true)
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
			ResetHttp()
			registerer, registry := wrapRegister("test", "test")
			metricsAddr := exposeMetrics(appCtx, client, registerer, registry)

			u := url.URL{Scheme: "http", Host: metricsAddr, Path: "/metrics"}
			resp, err := http.Get(u.String())
			So(err, ShouldBeNil)
			all, err := io.ReadAll(resp.Body)
			So(err, ShouldBeNil)
			So(string(all), ShouldNotBeBlank)
		})
	})
}

func ResetHttp() {
	http.DefaultServeMux = http.NewServeMux()
}

func resetTestMeta() *redis.Client { // using Redis
	opt, _ := redis.ParseURL(testMeta)
	rdb := redis.NewClient(opt)
	_ = rdb.FlushDB(context.Background())
	return rdb
}

func mountTemp(t *testing.T, bucket *string, trash bool) {
	_ = resetTestMeta()
	testDir := t.TempDir()
	if bucket != nil {
		*bucket = testDir
	}
	args := []string{"", "format", "--bucket", testDir, testMeta, testVolume}
	if !trash {
		args = append(args, "--trash-days=0")
	}
	if err := Main(args); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()

	go func() {
		if err := Main([]string{"", "mount", "--enable-xattr", testMeta, testMountPoint, "--no-usage-report"}); err != nil {
			t.Errorf("mount failed: %s", err)
		}
	}()
	time.Sleep(2 * time.Second)
}

func umountTemp(t *testing.T) {
	if err := Main([]string{"", "umount", testMountPoint}); err != nil {
		t.Fatalf("umount failed: %s", err)
	}
}

func TestMount(t *testing.T) {
	mountTemp(t, nil, true)
	defer umountTemp(t)

	if err := os.WriteFile(fmt.Sprintf("%s/f1.txt", testMountPoint), []byte("test"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}
}

func TestUmount(t *testing.T) {
	mountTemp(t, nil, true)
	umountTemp(t)

	inode, err := utils.GetFileInode(testMountPoint)
	if err != nil {
		t.Fatalf("get file inode failed: %s", err)
	}
	if inode == 1 {
		t.Fatalf("umount failed: inode of %s is 1", testMountPoint)
	}
}
