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

package utils

import (
	"strings"
	"testing"
	"time"
)

//mutate_test_job_number: 2
//checksum 9cb13bb28aa7918edaf4f0f4ca92eea5
//checksum 05debda2840d31bac0ab5c20c5510591
func TestMin(t *testing.T) {
	assertEqual(t, Min(1, 2), 1)
	assertEqual(t, Min(-1, -2), -2)
	assertEqual(t, Min(0, 0), 0)
}

func TestExists(t *testing.T) {
	assertEqual(t, Exists("/"), true)
	assertEqual(t, Exists("/not_exist_path"), false)
}

func TestSplitDir(t *testing.T) {
	assertEqual(t, SplitDir("/a:/b"), []string{"/a", "/b"})
	assertEqual(t, SplitDir("a,/b"), []string{"a", "/b"})
	assertEqual(t, SplitDir("/a;b"), []string{"/a;b"})
	assertEqual(t, SplitDir("a/b"), []string{"a/b"})
}

func TestGetInode(t *testing.T) {
	_, err := GetFileInode("")
	if err == nil {
		t.Fatalf("invalid path should fail")
	}
	ino, err := GetFileInode("/")
	if err != nil {
		t.Fatalf("get file inode: %s", err)
	} else if ino > 2 {
		t.Fatalf("inode of root should be 1/2, but got %d", ino)
	}
}

func TestLocalIp(t *testing.T) {
	_, err := GetLocalIp("127.0.0.1")
	if err == nil {
		t.Fatalf("should fail with invalid address")
	}
	ip, err := GetLocalIp("127.0.0.1:22")
	if err != nil {
		t.Fatalf("get local ip: %s", err)
	}
	if ip != "127.0.0.1" {
		t.Fatalf("local ip should be 127.0.0.1, bug got %s", ip)
	}
}

func TestTimeout(t *testing.T) {
	err := WithTimeout(func() error {
		return nil
	}, time.Millisecond*10)
	if err != nil {
		t.Fatalf("fast function should return nil")
	}
	err = WithTimeout(func() error {
		time.Sleep(time.Millisecond * 100)
		return nil
	}, time.Millisecond*10)
	if err == nil || !strings.HasPrefix(err.Error(), "timeout after") {
		t.Fatalf("slow function should  be timeout: %s", err)
	}
}

func TestRemovePassword(t *testing.T) {
	testCase := []struct {
		uri      string
		expected string
	}{
		{"redis://:password@localhost:6379/0",
			"redis://:****@localhost:6379/0",
		},
		{"redis://:pass@word@localhost:6379/0",
			"redis://:****@localhost:6379/0",
		},
		{":password@localhost:6379/0",
			":****@localhost:6379/0",
		},
		{"oss://ak:sk@zhijian-test2.oss-cn-hangzhou.aliyuncs.com",
			"oss://ak:****@zhijian-test2.oss-cn-hangzhou.aliyuncs.com",
		},
		{"/tmp/file",
			"/tmp/file",
		},
		{"file:///tmp/file",
			"file:///tmp/file",
		},
		{"sftp:///tmp/file",
			"sftp:///tmp/file",
		},
	}
	for _, tc := range testCase {
		assertEqual(t, RemovePassword(tc.uri), tc.expected)
	}
}
