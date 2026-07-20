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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/juicedata/juicefs/pkg/meta"
)

// mutate_test_job_number: 3
func getStdout(args []string) ([]byte, error) {
	tmp, err := os.CreateTemp(os.TempDir(), "jfstest-*")
	if err != nil {
		return nil, err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	patch := gomonkey.ApplyGlobalVar(os.Stdout, *tmp)
	defer patch.Reset()

	if err = Main(args); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp.Name())
}

func getStdoutWithInput(args []string, input string) ([]byte, error) {
	tmpOut, err := os.CreateTemp(os.TempDir(), "jfstest-*")
	if err != nil {
		return nil, err
	}
	defer tmpOut.Close()
	defer os.Remove(tmpOut.Name())
	oldStdout := os.Stdout
	os.Stdout = tmpOut
	defer func() {
		os.Stdout = oldStdout
	}()

	if input != "" {
		tmpIn, err := os.CreateTemp(os.TempDir(), "jfstest-input-*")
		if err != nil {
			return nil, err
		}
		defer tmpIn.Close()
		defer os.Remove(tmpIn.Name())
		if _, err = tmpIn.WriteString(input); err != nil {
			return nil, err
		}
		if _, err = tmpIn.Seek(0, 0); err != nil {
			return nil, err
		}
		oldStdin := os.Stdin
		os.Stdin = tmpIn
		defer func() {
			os.Stdin = oldStdin
		}()
	}

	err = Main(args)
	data, readErr := os.ReadFile(tmpOut.Name())
	if err == nil {
		err = readErr
	}
	return data, err
}

func TestConfig(t *testing.T) {
	_ = resetTestMeta()
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	if err := Main([]string{"", "format", testMeta, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	if err := Main([]string{"", "config", testMeta, "--trash-days", "2"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	data, err := getStdout([]string{"", "config", testMeta})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.TrashDays != 2 {
		t.Fatalf("trash-days %d != expect 2", format.TrashDays)
	}

	if err = Main([]string{"", "config", testMeta, "--capacity", "10", "--inodes", "1000000"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	newBucketPath := filepath.Join(t.TempDir(), "newBucket")
	if err = Main([]string{"", "config", testMeta, "--bucket", newBucketPath, "--access-key", "testAK", "--secret-key", "testSK", "--session-token", "token"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if data, err = getStdout([]string{"", "config", testMeta}); err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.Capacity != 10<<30 || format.Inodes != 1000000 ||
		format.Bucket != newBucketPath+"/" || format.AccessKey != "testAK" || format.SecretKey != "removed" || format.SessionToken != "removed" {
		t.Fatalf("unexpect format: %+v", format)
	}

	if err = Main([]string{"", "config", testMeta, "--bucket", "http://localhost:9000/miniofs", "--storage", "minio", "--force"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if data, err = getStdout([]string{"", "config", testMeta}); err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.Bucket != "http://localhost:9000/miniofs" || format.Storage != "minio" {
		t.Fatalf("unexpect format: %+v", format)
	}

	if err = Main([]string{"", "config", testMeta, "--bucket", "http://localhost:9000/miniofs2", "--force"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if data, err = getStdout([]string{"", "config", testMeta}); err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.Bucket != "http://localhost:9000/miniofs2" || format.Storage != "minio" {
		t.Fatalf("unexpect format: %+v", format)
	}
}

func TestConfigMinClientVersion(t *testing.T) {
	writeKerbConf := func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), "krb5.conf")
		if err := os.WriteFile(path, []byte("[libdefaults]\n"), 0644); err != nil {
			t.Fatalf("write kerberos config: %s", err)
		}
		return path
	}

	cases := []struct {
		name                  string
		formatArgs            func(t *testing.T) []string // extra flags for the initial format
		preArgs               []string                    // optional config run before the measured one
		args                  func(t *testing.T) []string // measured config run; nil to only check format result
		input                 string                      // stdin for confirmation prompts
		wantOut               []string                    // substrings expected in the measured output
		notWantOut            []string                    // substrings that must not appear
		wantErr               string                      // substring expected in the measured error
		wantMinVerOutputCount int                         // expected number of min-client-version output lines; 0 skips the check
		wantMinVer            string
		validate              func(t *testing.T, format meta.Format)
	}{
		{
			name:       "tier bumps min client version",
			args:       func(t *testing.T) []string { return []string{"--tier", "1", "--storage-class", "GLACIER", "--force"} },
			wantOut:    []string{"min-client-version: 1.1.0-A -> 1.4.0-A"},
			wantMinVer: "1.4.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if tier := format.Tiers[1]; tier.Sc != "GLACIER" {
					t.Fatalf("tier 1 storage-class %q != expect GLACIER", tier.Sc)
				}
			},
		},
		{
			name:       "acl does not downgrade",
			preArgs:    []string{"--min-client-version", "1.4.0-A", "--force"},
			args:       func(t *testing.T) []string { return []string{"--enable-acl", "--force"} },
			notWantOut: []string{"min-client-version"},
			wantMinVer: "1.4.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if !format.EnableACL {
					t.Fatalf("enable-acl should be true")
				}
			},
		},
		{
			name:       "ranger does not downgrade",
			preArgs:    []string{"--min-client-version", "1.4.0-A", "--force"},
			args:       func(t *testing.T) []string { return []string{"--ranger-rest-url", "http://localhost:6080", "--force"} },
			notWantOut: []string{"min-client-version"},
			wantMinVer: "1.4.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if format.RangerRestUrl != "http://localhost:6080" {
					t.Fatalf("ranger-rest-url %q != expect http://localhost:6080", format.RangerRestUrl)
				}
			},
		},
		{
			name:       "kerberos does not downgrade",
			preArgs:    []string{"--min-client-version", "1.4.0-A", "--force"},
			args:       func(t *testing.T) []string { return []string{"--kerberos-config-file", writeKerbConf(t), "--force"} },
			notWantOut: []string{"min-client-version"},
			wantMinVer: "1.4.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if format.KerbConf != "[libdefaults]\n" {
					t.Fatalf("unexpected kerberos config: %q", format.KerbConf)
				}
			},
		},
		{
			name: "feature overrides explicit lower version",
			args: func(t *testing.T) []string {
				return []string{"--enable-acl", "--min-client-version", "1.1.0-A", "--force"}
			},
			wantOut:    []string{"min-client-version: 1.1.0-A -> 1.2.0-A"},
			wantMinVer: "1.2.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if !format.EnableACL {
					t.Fatalf("enable-acl should be true")
				}
			},
		},
		{
			name: "explicit min version upgrade output once",
			args: func(t *testing.T) []string {
				return []string{"--min-client-version", "1.4.0-A", "--force"}
			},
			wantOut:               []string{"min-client-version: 1.1.0-A -> 1.4.0-A"},
			wantMinVerOutputCount: 1,
			wantMinVer:            "1.4.0-A",
		},
		{
			name:       "explicit min version downgrade is rejected",
			preArgs:    []string{"--min-client-version", "1.4.0-A", "--force"},
			args:       func(t *testing.T) []string { return []string{"--min-client-version", "1.2.0-A", "--force"} },
			wantErr:    "cannot lower min-client-version from 1.4.0-A to 1.2.0-A",
			wantMinVer: "1.4.0-A",
		},
		{
			name:       "kerberos via config with confirmation input",
			args:       func(t *testing.T) []string { return []string{"--kerberos-config-file", writeKerbConf(t)} },
			input:      "y\n",
			wantOut:    []string{"min-client-version: 1.1.0-A -> 1.4.0-A"},
			wantMinVer: "1.4.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if format.KerbConf != "[libdefaults]\n" {
					t.Fatalf("unexpected kerberos config: %q", format.KerbConf)
				}
			},
		},
		{
			name:       "format with kerberos bumps min client version",
			formatArgs: func(t *testing.T) []string { return []string{"--kerberos-config-file", writeKerbConf(t)} },
			wantMinVer: "1.4.0-A",
			validate: func(t *testing.T, format meta.Format) {
				if format.KerbConf != "[libdefaults]\n" {
					t.Fatalf("unexpected kerberos config: %q", format.KerbConf)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
			bucketPath := filepath.Join(t.TempDir(), "testBucket")
			formatCmd := []string{"", "format", metaURL, "--bucket", bucketPath}
			if c.formatArgs != nil {
				formatCmd = append(formatCmd, c.formatArgs(t)...)
			}
			formatCmd = append(formatCmd, testVolume)
			if err := Main(formatCmd); err != nil {
				t.Fatalf("format: %s", err)
			}

			if len(c.preArgs) > 0 {
				if err := Main(append([]string{"", "config", metaURL}, c.preArgs...)); err != nil {
					t.Fatalf("pre config: %s", err)
				}
			}

			if c.args != nil {
				args := append([]string{"", "config", metaURL}, c.args(t)...)
				var out []byte
				var err error
				if c.input != "" {
					out, err = getStdoutWithInput(args, c.input)
				} else {
					out, err = getStdout(args)
				}
				if c.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), c.wantErr) {
						t.Fatalf("config error %q does not contain %q", err, c.wantErr)
					}
				} else if err != nil {
					t.Fatalf("config: %s", err)
				}
				for _, s := range c.wantOut {
					if !strings.Contains(string(out), s) {
						t.Fatalf("missing %q in output: %s", s, out)
					}
				}
				for _, s := range c.notWantOut {
					if strings.Contains(string(out), s) {
						t.Fatalf("unexpected %q in output: %s", s, out)
					}
				}
				if c.wantMinVerOutputCount > 0 {
					if count := strings.Count(string(out), "min-client-version:"); count != c.wantMinVerOutputCount {
						t.Fatalf("min-client-version output count %d != expect %d: %s", count, c.wantMinVerOutputCount, out)
					}
				}
			}

			data, err := getStdout([]string{"", "config", metaURL})
			if err != nil {
				t.Fatalf("getStdout: %s", err)
			}
			var format meta.Format
			if err = json.Unmarshal(data, &format); err != nil {
				t.Fatalf("json unmarshal: %s", err)
			}
			if format.MinClientVersion != c.wantMinVer {
				t.Fatalf("min-client-version %q != expect %q", format.MinClientVersion, c.wantMinVer)
			}
			if c.validate != nil {
				c.validate(t, format)
			}
		})
	}
}
