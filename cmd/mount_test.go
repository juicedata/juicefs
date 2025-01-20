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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/redis/go-redis/v9"
	"github.com/urfave/cli/v2"
)

const testMeta = "redis://127.0.0.1:6379/11"
const testMountPoint = "/tmp/jfs-unit-test"
const testVolume = "test"

// gomonkey may encounter the problem of insufficient permissions under mac, please solve it by viewing this link https://github.com/agiledragon/gomonkey/issues/70
func Test_exposeMetrics(t *testing.T) {
	addr := "redis://127.0.0.1:6379/12"
	client := meta.NewClient(addr, nil)
	format := &meta.Format{
		Name:      "test",
		BlockSize: 4096,
		Capacity:  1 << 30,
		DirStats:  true,
	}
	_ = client.Init(format, true)
	var appCtx *cli.Context
	stringPatches := gomonkey.ApplyMethod(reflect.TypeOf(appCtx), "String", func(_ *cli.Context, arg string) string {
		switch arg {
		case "metrics":
			return "127.0.0.1:9567"
		case "consul":
			return "127.0.0.1:8500"
		case "custom-labels":
			return "key1:value1"
		default:
			return ""
		}
	})
	isSetPatches := gomonkey.ApplyMethod(reflect.TypeOf(appCtx), "IsSet", func(_ *cli.Context, arg string) bool {
		switch arg {
		case "custom-labels":
			return true
		default:
			return false
		}
	})
	defer stringPatches.Reset()
	defer isSetPatches.Reset()
	ResetHttp()
	registerer, registry := wrapRegister(appCtx, "test", "test")
	metricsAddr := exposeMetrics(appCtx, registerer, registry)
	client.InitMetrics(registerer)
	vfs.InitMetrics(registerer)
	u := url.URL{Scheme: "http", Host: metricsAddr, Path: "/metrics"}
	resp, err := http.Get(u.String())
	require.Nil(t, err)
	all, err := io.ReadAll(resp.Body)
	require.Nil(t, err)
	require.NotEmpty(t, all)
	require.Contains(t, string(all), `key1="value1"`)
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

var mountLock sync.Mutex

func mountTemp(t *testing.T, bucket *string, extraFormatOpts []string, extraMountOpts []string) {
	// wait for last mount exit
	for !mountLock.TryLock() {
		time.Sleep(100 * time.Millisecond)
	}

	_ = resetTestMeta()
	testDir := t.TempDir()
	if bucket != nil {
		*bucket = testDir
	}
	formatArgs := []string{"", "format", "--bucket", testDir, testMeta, testVolume}
	if extraFormatOpts != nil {
		formatArgs = append(formatArgs, extraFormatOpts...)
	}
	if err := Main(formatArgs); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()

	os.Setenv("JFS_SUPERVISOR", "test")
	mountArgs := []string{"", "mount", "--enable-xattr", testMeta, testMountPoint, "--attr-cache", "0", "--entry-cache", "0", "--dir-entry-cache", "0", "--no-usage-report"}
	if extraMountOpts != nil {
		mountArgs = append(mountArgs, extraMountOpts...)
	}
	go func() {
		defer mountLock.Unlock()
		if err := Main(mountArgs); err != nil {
			t.Errorf("mount failed: %s", err)
		}
	}()
	time.Sleep(3 * time.Second)
	inode, err := utils.GetFileInode(testMountPoint)
	if err != nil {
		t.Fatalf("get file inode failed: %s", err)
	}
	if inode != 1 {
		t.Fatalf("mount failed: inode of %s got %d, expect 1", testMountPoint, inode)
	} else {
		t.Logf("mount %s success", testMountPoint)
	}
}

func umountTemp(t *testing.T) {
	if err := Main([]string{"", "umount", testMountPoint}); err != nil {
		t.Fatalf("umount failed: %s", err)
	}
}

func TestMount(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	if err := os.WriteFile(fmt.Sprintf("%s/f1.txt", testMountPoint), []byte("test"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}
}

func TestUpdateFstab(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.SkipNow()
	}
	mockFstab, err := os.CreateTemp("/tmp", "fstab")
	if err != nil {
		t.Fatalf("cannot make temp file: %s", err)
	}
	defer os.Remove(mockFstab.Name())

	patches := gomonkey.ApplyFunc(os.Rename, func(src, dest string) error {
		content, err := os.ReadFile(mockFstab.Name())
		if err != nil {
			t.Fatalf("error reading mocked fstab: %s", err)
		}
		rv := "redis://127.0.0.1:6379/11 /tmp/jfs-unit-test juicefs _netdev,enable-xattr,entry-cache=2,max-uploads=3,max_read=99,no-usage-report,writeback 0 0"
		lv := strings.TrimSpace(string(content))
		if lv != rv {
			t.Fatalf("incorrect fstab entry: %s", content)
		}
		return os.Rename(src, dest)
	})
	defer patches.Reset()
	mountArgs := []string{"juicefs", "mount", "--enable-xattr", testMeta, testMountPoint, "--no-usage-report"}
	mountOpts := []string{"--update-fstab", "--writeback", "--entry-cache=2", "--max-uploads", "3", "-o", "max_read=99"}
	patches = gomonkey.ApplyGlobalVar(&os.Args, append(mountArgs, mountOpts...))
	defer patches.Reset()
	mountTemp(t, nil, nil, mountOpts)
	defer umountTemp(t)
}

func TestUmount(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	umountTemp(t)

	inode, err := utils.GetFileInode(testMountPoint)
	if err != nil {
		t.Fatalf("get file inode failed: %s", err)
	}
	if inode == 1 {
		t.Fatalf("umount failed: inode of %s is 1", testMountPoint)
	}
}

func tryMountTemp(t *testing.T, bucket *string, extraFormatOpts []string, extraMountOpts []string) error {
	// wait for last mount exit
	for !mountLock.TryLock() {
		time.Sleep(100 * time.Millisecond)
	}

	_ = resetTestMeta()
	testDir := t.TempDir()
	if bucket != nil {
		*bucket = testDir
	}
	formatArgs := []string{"", "format", "--bucket", testDir, testMeta, testVolume}
	if extraFormatOpts != nil {
		formatArgs = append(formatArgs, extraFormatOpts...)
	}
	if err := Main(formatArgs); err != nil {
		return fmt.Errorf("format failed: %w", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()

	mountArgs := []string{"", "mount", "--enable-xattr", testMeta, testMountPoint, "--attr-cache", "0", "--entry-cache", "0", "--dir-entry-cache", "0", "--no-usage-report"}
	if extraMountOpts != nil {
		mountArgs = append(mountArgs, extraMountOpts...)
	}

	os.Setenv("JFS_SUPERVISOR", "test")
	errChan := make(chan error, 1)
	go func() {
		defer mountLock.Unlock()
		errChan <- Main(mountArgs)
	}()

	select {
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("mount failed: %w", err)
		}
	case <-time.After(3 * time.Second):
	}

	inode, err := utils.GetFileInode(testMountPoint)
	if err != nil {
		return fmt.Errorf("get file inode failed: %w", err)
	}
	if inode != 1 {
		return fmt.Errorf("mount failed: inode of %s is %d, expect 1", testMountPoint, inode)
	}
	t.Logf("mount %s success", testMountPoint)
	return nil
}

func TestMountVersionMatch(t *testing.T) {
	oriVersion := version.Version()
	version.SetVersion("1.1.0")
	defer version.SetVersion(oriVersion)

	err := tryMountTemp(t, nil, nil, nil)
	assert.Nil(t, err)
	umountTemp(t)

	err = tryMountTemp(t, nil, []string{"--enable-acl=true"}, nil)
	assert.Contains(t, err.Error(), "check version")
}

func TestParseUIDGID(t *testing.T) {
	tests := []struct {
		input       string
		defaultUid  uint32
		defaultGid  uint32
		expectedUid uint32
		expectedGid uint32
	}{
		{"1000:1000", 65534, 65534, 1000, 1000},
		{"1000:", 65534, 65534, 1000, 65534},
		{":1000", 65534, 65534, 65534, 1000},
		{"", 65534, 65534, 65534, 65534},
		{"0:1000", 65534, 65534, 65534, 1000},
		{"1000:0", 65534, 65534, 1000, 65534},
	}

	for _, tt := range tests {
		uid, gid := parseUIDGID(tt.input, tt.defaultUid, tt.defaultGid)
		if uid != tt.expectedUid || gid != tt.expectedGid {
			t.Errorf("parseUIDGID(%q) = (%d, %d), want (%d, %d)", tt.input, uid, gid, tt.expectedUid, tt.expectedGid)
		}
	}
}
