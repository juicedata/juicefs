//go:build !nosftp
// +build !nosftp

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

package object

import (
	"os"
	"strings"
	"testing"
)

func TestSftp(t *testing.T) { //skip mutate
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	b, _ := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	testStorage(t, b)
}

func TestSftp2(t *testing.T) { //skip mutate
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	sftp, err := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	if err != nil {
		t.Fatalf("sftp: %s", err)
	}
	testFileSystem(t, sftp)
}

func TestParseSftpEndpoint(t *testing.T) {
	tests := []struct {
		name               string
		endpoint           string
		wantHost, wantPort string
		wantRoot           string
		wantErr            string
	}{
		{
			name:     "default port with timestamp colons",
			endpoint: "host:/path/T05:53:21",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "explicit port with timestamp colons",
			endpoint: "host:2022:/path/T05:53:21",
			wantHost: "host",
			wantPort: "2022",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "IPv6 explicit port with timestamp colons",
			endpoint: "[2001:db8::1]:2022:/path/T05:53:21",
			wantHost: "2001:db8::1",
			wantPort: "2022",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "IPv6 default port with timestamp colons",
			endpoint: "[2001:db8::1]:/path/T05:53:21",
			wantHost: "2001:db8::1",
			wantPort: "22",
			wantRoot: "/path/T05:53:21",
		},
		{
			name:     "relative path remains supported",
			endpoint: "host:backup/path",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "backup/path",
		},
		{
			name:     "relative path with timestamp colons",
			endpoint: "host:T05:53:21",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "T05:53:21",
		},
		{
			name:     "relative path with ISO timestamp colons",
			endpoint: "host:2026-07-23T05:53:21",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "2026-07-23T05:53:21",
		},
		{
			name:     "explicit port with relative path",
			endpoint: "host:2022:backup/path",
			wantHost: "host",
			wantPort: "2022",
			wantRoot: "backup/path",
		},
		{
			name:     "relative path with symbols",
			endpoint: "host:!@#$%^&*()_+-=[]{}|;,.<>?",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "!@#$%^&*()_+-=[]{}|;,.<>?",
		},
		{
			name:     "relative path starts with colon",
			endpoint: "host::backup/path",
			wantHost: "host",
			wantPort: "22",
			wantRoot: ":backup/path",
		},
		{
			name:     "numeric relative path",
			endpoint: "host:2022",
			wantHost: "host",
			wantPort: "22",
			wantRoot: "2022",
		},
		{
			name:     "empty path",
			endpoint: "host:",
			wantErr:  "missing path",
		},
		{
			name:     "explicit port with empty path",
			endpoint: "host:2022:",
			wantErr:  "missing path",
		},
		{
			name:     "IPv6 default port with empty path",
			endpoint: "[2001:db8::1]:",
			wantErr:  "missing path",
		},
		{
			name:     "missing port path separator",
			endpoint: "host:2022/path",
			wantErr:  "missing colon between port and path",
		},
		{
			name:     "IPv6 missing port path separator",
			endpoint: "[2001:db8::1]:2022/path",
			wantErr:  "missing colon between port and path",
		},
		{
			name:     "console endpoint missing port path separator",
			endpoint: "172.28.39.219:22/root/bak/jfs-console-dump-2026-07-23T05:53:21.json.gz.gpg",
			wantErr:  "missing colon between port and path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, port, root, err := parseSftpEndpoint(test.endpoint)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseSftpEndpoint(%q) error = %v, want error containing %q", test.endpoint, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if host != test.wantHost || port != test.wantPort || root != test.wantRoot {
				t.Fatalf("parseSftpEndpoint(%q) = (%q, %q, %q), want (%q, %q, %q)",
					test.endpoint, host, port, root, test.wantHost, test.wantPort, test.wantRoot)
			}
		})
	}
}
