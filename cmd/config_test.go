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

func TestConfigTierMinClientVersion(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	out, err := getStdout([]string{"", "config", metaURL, "--tier", "1", "--storage-class", "GLACIER", "--force"})
	if err != nil {
		t.Fatalf("config tier: %s", err)
	}
	if !strings.Contains(string(out), "min-client-version: 1.1.0-A -> 1.4.0-A") {
		t.Fatalf("min client version change is missing in output: %s", out)
	}

	data, err := getStdout([]string{"", "config", metaURL})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.MinClientVersion != "1.4.0-A" {
		t.Fatalf("min-client-version %q != expect 1.4.0-A", format.MinClientVersion)
	}
	if tier := format.Tiers[1]; tier.Sc != "GLACIER" {
		t.Fatalf("tier 1 storage-class %q != expect GLACIER", tier.Sc)
	}
}

func TestConfigTierZeroDoesNotChangeMinClientVersion(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	out, err := getStdout([]string{"", "config", metaURL, "--tier", "0", "--storage-class", "STANDARD_IA", "--force"})
	if err != nil {
		t.Fatalf("config tier 0: %s", err)
	}
	if strings.Contains(string(out), "min-client-version") {
		t.Fatalf("tier 0 should not change min-client-version: %s", out)
	}

	data, err := getStdout([]string{"", "config", metaURL})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.MinClientVersion != "1.1.0-A" {
		t.Fatalf("min-client-version %q != expect 1.1.0-A", format.MinClientVersion)
	}
	if tier := format.Tiers[0]; tier.Sc != "STANDARD_IA" {
		t.Fatalf("tier 0 storage-class %q != expect STANDARD_IA", tier.Sc)
	}
}

func TestConfigAutoMinClientVersionRequiresConfirmation(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	out, err := getStdoutWithInput([]string{"", "config", metaURL, "--enable-acl"}, "n\n")
	if err == nil || err.Error() != "Aborted." {
		t.Fatalf("config should be aborted, got output %q error %v", out, err)
	}
	if !strings.Contains(string(out), "Clients below version 1.2.0-A will be rejected after modification.") {
		t.Fatalf("missing min client version confirmation warning: %s", out)
	}

	data, err := getStdout([]string{"", "config", metaURL})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.EnableACL {
		t.Fatalf("enable-acl should not be changed after abort")
	}
	if format.MinClientVersion != "1.1.0-A" {
		t.Fatalf("min-client-version %q != expect 1.1.0-A", format.MinClientVersion)
	}
}

func TestConfigAutoMinClientVersionDoesNotDowngrade(t *testing.T) {
	cases := []struct {
		name     string
		args     func(t *testing.T) []string
		validate func(t *testing.T, format meta.Format)
	}{
		{
			name: "acl",
			args: func(t *testing.T) []string {
				return []string{"--enable-acl"}
			},
			validate: func(t *testing.T, format meta.Format) {
				if !format.EnableACL {
					t.Fatalf("enable-acl should be true")
				}
			},
		},
		{
			name: "ranger",
			args: func(t *testing.T) []string {
				return []string{"--ranger-rest-url", "http://localhost:6080"}
			},
			validate: func(t *testing.T, format meta.Format) {
				if format.RangerRestUrl != "http://localhost:6080" {
					t.Fatalf("ranger-rest-url %q != expect http://localhost:6080", format.RangerRestUrl)
				}
			},
		},
		{
			name: "kerberos",
			args: func(t *testing.T) []string {
				path := filepath.Join(t.TempDir(), "krb5.conf")
				if err := os.WriteFile(path, []byte("[libdefaults]\n"), 0644); err != nil {
					t.Fatalf("write kerberos config: %s", err)
				}
				return []string{"--kerberos-config-file", path}
			},
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
			if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, testVolume}); err != nil {
				t.Fatalf("format: %s", err)
			}
			if err := Main([]string{"", "config", metaURL, "--min-client-version", "1.4.0-A", "--force"}); err != nil {
				t.Fatalf("config min-client-version: %s", err)
			}

			args := append([]string{"", "config", metaURL}, c.args(t)...)
			args = append(args, "--force")
			out, err := getStdout(args)
			if err != nil {
				t.Fatalf("config %s: %s", c.name, err)
			}
			if strings.Contains(string(out), "min-client-version") {
				t.Fatalf("min-client-version should not be downgraded: %s", out)
			}

			data, err := getStdout([]string{"", "config", metaURL})
			if err != nil {
				t.Fatalf("getStdout: %s", err)
			}
			var format meta.Format
			if err = json.Unmarshal(data, &format); err != nil {
				t.Fatalf("json unmarshal: %s", err)
			}
			if format.MinClientVersion != "1.4.0-A" {
				t.Fatalf("min-client-version %q != expect 1.4.0-A", format.MinClientVersion)
			}
			c.validate(t, format)
		})
	}
}

func TestConfigFeatureMinClientVersionOverridesExplicitLowerVersion(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	out, err := getStdout([]string{"", "config", metaURL, "--enable-acl", "--min-client-version", "1.1.0-A", "--force"})
	if err != nil {
		t.Fatalf("config acl with lower min-client-version: %s", err)
	}
	if !strings.Contains(string(out), "min-client-version: 1.1.0-A -> 1.2.0-A") {
		t.Fatalf("missing final min-client-version change: %s", out)
	}

	data, err := getStdout([]string{"", "config", metaURL})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if !format.EnableACL {
		t.Fatalf("enable-acl should be true")
	}
	if format.MinClientVersion != "1.2.0-A" {
		t.Fatalf("min-client-version %q != expect 1.2.0-A", format.MinClientVersion)
	}
}

func TestConfigKerberosMinClientVersionWithConfirmation(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	path := filepath.Join(t.TempDir(), "krb5.conf")
	if err := os.WriteFile(path, []byte("[libdefaults]\n"), 0644); err != nil {
		t.Fatalf("write kerberos config: %s", err)
	}
	out, err := getStdoutWithInput([]string{"", "config", metaURL, "--kerberos-config-file", path}, "y\n")
	if err != nil {
		t.Fatalf("config kerberos: %s", err)
	}
	if !strings.Contains(string(out), "Clients below version 1.4.0-A will be rejected after modification.") {
		t.Fatalf("missing min client version confirmation warning: %s", out)
	}
	if !strings.Contains(string(out), "min-client-version: 1.1.0-A -> 1.4.0-A") {
		t.Fatalf("missing min client version change: %s", out)
	}

	data, err := getStdout([]string{"", "config", metaURL})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.MinClientVersion != "1.4.0-A" {
		t.Fatalf("min-client-version %q != expect 1.4.0-A", format.MinClientVersion)
	}
	if format.KerbConf != "[libdefaults]\n" {
		t.Fatalf("unexpected kerberos config: %q", format.KerbConf)
	}
}

func TestFormatKerberosMinClientVersion(t *testing.T) {
	metaURL := "sqlite3://" + filepath.Join(t.TempDir(), "test.db")
	bucketPath := filepath.Join(t.TempDir(), "testBucket")
	path := filepath.Join(t.TempDir(), "krb5.conf")
	if err := os.WriteFile(path, []byte("[libdefaults]\n"), 0644); err != nil {
		t.Fatalf("write kerberos config: %s", err)
	}
	if err := Main([]string{"", "format", metaURL, "--bucket", bucketPath, "--kerberos-config-file", path, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	data, err := getStdout([]string{"", "config", metaURL})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.MinClientVersion != "1.4.0-A" {
		t.Fatalf("min-client-version %q != expect 1.4.0-A", format.MinClientVersion)
	}
	if format.KerbConf != "[libdefaults]\n" {
		t.Fatalf("unexpected kerberos config: %q", format.KerbConf)
	}
}
