/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

const gatewayVolume = "gateway-volume"
const gatewayAddr = "localhost:9008"
const webdavVolume = "webdav-volume"
const webdavAddr = "localhost:9009"

func hasNonInteractiveSudo() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}

func mountIntegrationTemp(t *testing.T, extraMountOpts []string) {
	for !mountLock.TryLock() {
		time.Sleep(100 * time.Millisecond)
	}

	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "mount.db")
	testDir := t.TempDir()
	if err := Main([]string{"", "format", "--bucket", testDir, metaURL, testVolume}); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	ResetHttp()

	os.Setenv("JFS_SUPERVISOR", "test")
	mountArgs := []string{"", "mount", "--enable-xattr", metaURL, testMountPoint, "--attr-cache", "0", "--entry-cache", "0", "--dir-entry-cache", "0", "--no-usage-report"}
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
	}
}

func startGateway(t *testing.T) {
	gatewayMeta := "sqlite3://" + filepath.Join(t.TempDir(), "gateway.db")
	testDir := t.TempDir()
	if err := Main([]string{"", "format", "--bucket", testDir, gatewayMeta, gatewayVolume}); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()
	os.Setenv("MINIO_ROOT_USER", "testUser")
	os.Setenv("MINIO_ROOT_PASSWORD", "testUserPassword")

	go func() {
		if err := Main([]string{"", "gateway", gatewayMeta, gatewayAddr, "--multi-buckets", "--keep-etag", "--object-tag", "--no-usage-report"}); err != nil {
			t.Errorf("gateway failed: %s", err)
		}
	}()
	time.Sleep(2 * time.Second)
}

func startWebdav(t *testing.T) {
	webdavMeta := "sqlite3://" + filepath.Join(t.TempDir(), "webdav.db")
	testDir := t.TempDir()
	if err := Main([]string{"", "format", "--bucket", testDir, webdavMeta, webdavVolume}); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()

	go func() {
		os.Setenv("WEBDAV_USER", "root")
		os.Setenv("WEBDAV_PASSWORD", "1234")
		if err := Main([]string{"", "webdav", webdavMeta, webdavAddr, "--no-usage-report"}); err != nil {
			t.Errorf("gateway failed: %s", err)
		}
	}()
	time.Sleep(2 * time.Second)
}

func TestIntegration(t *testing.T) {
	startGateway(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory failed: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir("../integration"); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}

	runMakeTargets := func(targets ...string) {
		t.Helper()
		makeCmd := exec.Command("make", targets...)
		out, err := makeCmd.CombinedOutput()
		t.Logf("make %v output:\n%s\n", targets, string(out))
		if err != nil {
			t.Fatalf("make %v failed: %v", targets, err)
		}
	}

	runMakeTargets("s3test")

	if !hasNonInteractiveSudo() {
		t.Log("skipping ioctl/webdav integration: non-interactive sudo unavailable")
		return
	}

	mountIntegrationTemp(t, []string{"--enable-ioctl"})
	defer umountTemp(t)
	runMakeTargets("ioctl")

	if _, err := os.Stat("/home/travis/.m2/litmus-0.13"); err != nil {
		t.Logf("skipping webdav integration: litmus not available: %v", err)
		return
	}

	startWebdav(t)
	runMakeTargets("webdav")
}
