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

package sts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var stsEnv = []string{
	"JFS_STS_ENABLE",
	"JFS_STS_CLOUD",
	"JFS_STS_ROLE_ARN",
	"JFS_STS_SEED_FILE",
	"JFS_STS_SEED_AK",
	"JFS_STS_SEED_SK",
	"JFS_STS_ENDPOINT",
	"JFS_STS_REGION",
	"JFS_STS_DURATION",
	"JFS_STS_SESSION_NAME",
	"JFS_STS_POLICY",
	"JFS_STS_POLICY_FILE",
	"JFS_STS_FALLBACK",
	"JFS_STS_REFRESH_MARGIN",
	"JFS_STS_TIMEOUT",
}

func clearSTSEnv(t *testing.T) {
	t.Helper()
	for _, name := range stsEnv {
		t.Setenv(name, "")
	}
}

func TestLoadSeedFileJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(path, []byte(`{"access_key_id":"ak","access_key_secret":"sk"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ak, sk, err := LoadSeedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ak != "ak" || sk != "sk" {
		t.Fatalf("unexpected seed: %q %q", ak, sk)
	}
}

func TestLoadSeedFileKeyValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.conf")
	data := "# seed credentials\naccess_key_id = ak\naccess_key_secret = 'sk'\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	ak, sk, err := LoadSeedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ak != "ak" || sk != "sk" {
		t.Fatalf("unexpected seed: %q %q", ak, sk)
	}
}

func TestConfigDisabled(t *testing.T) {
	clearSTSEnv(t)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("STS should be disabled by default")
	}
}

func TestConfigFromEnv(t *testing.T) {
	clearSTSEnv(t)
	t.Setenv("JFS_STS_ENABLE", "true")
	t.Setenv("JFS_STS_ROLE_ARN", "acs:ram::1234567890:role/juicefs")
	t.Setenv("JFS_STS_SEED_AK", "ak")
	t.Setenv("JFS_STS_SEED_SK", "sk")
	t.Setenv("JFS_STS_DURATION", "1h")
	t.Setenv("JFS_STS_FALLBACK", "static")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Cloud != CloudAliyun {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Duration != time.Hour || !cfg.Fallback {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.SessionName == "" {
		t.Fatal("session name should have a default")
	}
}

func TestConfigDurationSeconds(t *testing.T) {
	clearSTSEnv(t)
	t.Setenv("JFS_STS_ENABLE", "true")
	t.Setenv("JFS_STS_ROLE_ARN", "acs:ram::1234567890:role/juicefs")
	t.Setenv("JFS_STS_SEED_AK", "ak")
	t.Setenv("JFS_STS_SEED_SK", "sk")
	t.Setenv("JFS_STS_DURATION", "1800")
	t.Setenv("JFS_STS_FALLBACK", "none")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Duration != 30*time.Minute {
		t.Fatalf("unexpected duration: %s", cfg.Duration)
	}
	if cfg.Fallback {
		t.Fatal("fallback should be disabled")
	}
}

func TestConfigValidate(t *testing.T) {
	base := Config{
		Enabled:       true,
		Cloud:         CloudAliyun,
		RoleARN:       "role",
		Duration:      time.Hour,
		RefreshMargin: 5 * time.Minute,
		Timeout:       10 * time.Second,
	}

	if err := base.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	broken := base
	broken.RoleARN = ""
	if err := broken.Validate(); err == nil {
		t.Fatal("missing role ARN should be rejected")
	}

	broken = base
	broken.Cloud = "unknown"
	if err := broken.Validate(); err == nil {
		t.Fatal("unknown cloud should be rejected")
	}

	broken = base
	broken.Duration = time.Minute
	if err := broken.Validate(); err == nil {
		t.Fatal("too short duration should be rejected")
	}

	broken = base
	broken.RefreshMargin = time.Hour
	if err := broken.Validate(); err == nil {
		t.Fatal("refresh margin >= duration should be rejected")
	}

	broken = base
	broken.SeedAccessKey = "ak"
	if err := broken.Validate(); err == nil {
		t.Fatal("seed AK without seed SK should be rejected")
	}
}

func TestConfigInvalidPolicy(t *testing.T) {
	clearSTSEnv(t)
	t.Setenv("JFS_STS_ENABLE", "true")
	t.Setenv("JFS_STS_ROLE_ARN", "acs:ram::1234567890:role/juicefs")
	t.Setenv("JFS_STS_SEED_AK", "ak")
	t.Setenv("JFS_STS_SEED_SK", "sk")
	t.Setenv("JFS_STS_POLICY", "not-json")

	if _, err := FromEnv(); err == nil {
		t.Fatal("invalid policy JSON should be rejected")
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"https://sts.aliyuncs.com/":           "sts.aliyuncs.com",
		"http://sts.cn-hangzhou.aliyuncs.com": "sts.cn-hangzhou.aliyuncs.com",
		"sts.aliyuncs.com":                    "sts.aliyuncs.com",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
