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
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object/credentials"
)

type fakeClient struct {
	creds credentials.Credentials
	err   error
	calls int
}

func (f *fakeClient) AssumeRole(context.Context, Config) (credentials.Credentials, error) {
	f.calls++
	return f.creds, f.err
}

func testConfig() Config {
	return Config{
		Enabled:       true,
		Cloud:         CloudAliyun,
		RoleARN:       "acs:ram::1234567890:role/juicefs",
		Duration:      time.Hour,
		RefreshMargin: 5 * time.Minute,
		Timeout:       time.Second,
	}
}

func TestProviderGet(t *testing.T) {
	client := &fakeClient{creds: credentials.Credentials{
		AccessKeyID: "ak",
		Expires:     time.Now().Add(time.Hour),
	}}
	p := &Provider{client: client, cfg: testConfig()}

	got, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "ak" || client.calls != 1 {
		t.Fatalf("unexpected result: %+v, calls=%d", got, client.calls)
	}
}

func TestProviderError(t *testing.T) {
	client := &fakeClient{err: errors.New("sts is down")}
	p := &Provider{client: client, cfg: testConfig()}

	if _, err := p.Get(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestNewProviderInvalidConfig(t *testing.T) {
	cfg := testConfig()
	cfg.RoleARN = ""
	if _, err := NewProvider(cfg); err == nil {
		t.Fatal("expected an error for an empty role ARN")
	}
}

func TestNewProviderAliyun(t *testing.T) {
	cfg := testConfig()
	cfg.SeedAccessKey = "ak"
	cfg.SeedSecretKey = "sk"
	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.client == nil {
		t.Fatal("provider was not created")
	}
}

func TestNewProviderAWS(t *testing.T) {
	cfg := testConfig()
	cfg.Cloud = CloudAWS
	cfg.Region = "us-east-1"
	cfg.SeedAccessKey = "ak"
	cfg.SeedSecretKey = "sk"
	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.client == nil {
		t.Fatal("provider was not created")
	}
}

func TestNewProviderUnsupportedCloud(t *testing.T) {
	cfg := testConfig()
	cfg.Cloud = "unknown"
	if _, err := NewProvider(cfg); err == nil {
		t.Fatal("expected an error for an unsupported cloud")
	}
}

func TestNewProviderFromEnvDisabled(t *testing.T) {
	clearSTSEnv(t)
	p, err := NewProviderFromEnv("static-ak", "static-sk", "")
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatal("expected no provider when STS is disabled")
	}
}

func TestNewProviderFromEnvEnabled(t *testing.T) {
	clearSTSEnv(t)
	t.Setenv("JFS_STS_ENABLE", "true")
	t.Setenv("JFS_STS_ROLE_ARN", "acs:ram::1234567890:role/juicefs")
	t.Setenv("JFS_STS_SEED_AK", "ak")
	t.Setenv("JFS_STS_SEED_SK", "sk")

	p, err := NewProviderFromEnv("static-ak", "static-sk", "")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("expected a provider")
	}
}

func TestSanitizeError(t *testing.T) {
	err := errors.New(`Post "https://sts.aliyuncs.com/?AccessKeyId=LTAI123&Action=AssumeRole&Signature=abc": connection refused`)
	got := sanitizeError(err).Error()
	if strings.Contains(got, "LTAI123") || strings.Contains(got, "Signature=abc") {
		t.Fatalf("credentials were not redacted: %s", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("expected redaction marker, got: %s", got)
	}

	got = sanitizeError(errors.New("failed with AccessKeyId=LTAI123&foo=bar")).Error()
	if strings.Contains(got, "LTAI123") {
		t.Fatalf("access key was not redacted: %s", got)
	}
}
