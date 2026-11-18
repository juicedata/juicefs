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

// Package sts implements an in-process STS credential provider for JuiceFS.
//
// It is intentionally configured from the local environment (or a local seed
// file), never from the metadata service. Putting a role ARN, a seed access
// key, or an agent URL into Redis would turn a metadata compromise into remote
// code execution or credential theft.
package sts

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
)

var logger = utils.GetLogger("juicefs")

const (
	CloudAliyun = "aliyun"
	CloudAWS    = "aws"
)

// Config controls how JuiceFS obtains temporary credentials from STS.
type Config struct {
	Enabled bool

	// Cloud is "aliyun" or "aws".
	Cloud string
	// RoleARN is the role to assume. Its policy should be scoped to the
	// bucket/prefix used by the volume.
	RoleARN string

	// SeedFile points to a local file containing the access key used to call
	// AssumeRole. It should only have sts:AssumeRole permission. When it is
	// empty, SeedAccessKey/SeedSecretKey or the cloud SDK default credential
	// chain (instance role, OIDC, ...) is used.
	SeedFile      string
	SeedAccessKey string
	SeedSecretKey string

	// Endpoint is an optional custom STS endpoint, e.g.
	// "sts.cn-hangzhou.aliyuncs.com" or "https://sts.us-east-1.amazonaws.com".
	Endpoint string
	// Region is required by some clouds (AWS) and optional for Aliyun.
	Region string

	// Duration is the requested lifetime of the temporary credentials.
	Duration time.Duration
	// SessionName appears in the cloud audit log and should identify the
	// client (hostname, pid, volume).
	SessionName string
	// Policy is an optional session policy (JSON) used to further restrict the
	// temporary credentials.
	Policy string

	// Fallback enables falling back to the static access key stored in the
	// volume format when STS is unavailable. It should be enabled during a
	// gradual migration and disabled once every client uses STS.
	Fallback bool
	// RefreshMargin is how long before expiration the credentials are
	// refreshed.
	RefreshMargin time.Duration
	// Timeout limits a single STS request.
	Timeout time.Duration
}

// FromEnv builds a Config from JFS_STS_* environment variables. When STS is
// disabled, it returns a disabled Config and no error.
func FromEnv() (Config, error) {
	cfg := Config{
		Cloud:         CloudAliyun,
		Duration:      time.Hour,
		Fallback:      true,
		RefreshMargin: 5 * time.Minute,
		Timeout:       10 * time.Second,
	}

	enabled, err := boolEnv("JFS_STS_ENABLE", false)
	if err != nil {
		return cfg, err
	}
	cfg.Enabled = enabled
	if !enabled {
		return cfg, nil
	}

	if v := strings.TrimSpace(os.Getenv("JFS_STS_CLOUD")); v != "" {
		cfg.Cloud = strings.ToLower(v)
	}
	cfg.RoleARN = strings.TrimSpace(os.Getenv("JFS_STS_ROLE_ARN"))
	cfg.SeedFile = strings.TrimSpace(os.Getenv("JFS_STS_SEED_FILE"))
	cfg.SeedAccessKey = strings.TrimSpace(os.Getenv("JFS_STS_SEED_AK"))
	cfg.SeedSecretKey = strings.TrimSpace(os.Getenv("JFS_STS_SEED_SK"))
	cfg.Endpoint = strings.TrimSpace(os.Getenv("JFS_STS_ENDPOINT"))
	cfg.Region = strings.TrimSpace(os.Getenv("JFS_STS_REGION"))
	cfg.SessionName = strings.TrimSpace(os.Getenv("JFS_STS_SESSION_NAME"))
	cfg.Policy = strings.TrimSpace(os.Getenv("JFS_STS_POLICY"))

	if f := strings.TrimSpace(os.Getenv("JFS_STS_POLICY_FILE")); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return cfg, fmt.Errorf("read JFS_STS_POLICY_FILE %q: %w", f, err)
		}
		cfg.Policy = strings.TrimSpace(string(data))
	}
	if cfg.Policy != "" && !json.Valid([]byte(cfg.Policy)) {
		return cfg, fmt.Errorf("JFS_STS_POLICY is not valid JSON")
	}
	if cfg.SessionName == "" {
		cfg.SessionName = defaultSessionName()
	}
	if cfg.Duration, err = durationEnv("JFS_STS_DURATION", cfg.Duration); err != nil {
		return cfg, err
	}
	if cfg.RefreshMargin, err = durationEnv("JFS_STS_REFRESH_MARGIN", cfg.RefreshMargin); err != nil {
		return cfg, err
	}
	if cfg.Timeout, err = durationEnv("JFS_STS_TIMEOUT", cfg.Timeout); err != nil {
		return cfg, err
	}
	if cfg.Fallback, err = fallbackEnv(); err != nil {
		return cfg, err
	}

	if cfg.SeedFile != "" {
		ak, sk, err := LoadSeedFile(cfg.SeedFile)
		if err != nil {
			return cfg, err
		}
		cfg.SeedAccessKey, cfg.SeedSecretKey = ak, sk
	}

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks the configuration. It is also called by NewProvider.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.RoleARN == "" {
		return fmt.Errorf("JFS_STS_ROLE_ARN is required when JFS_STS_ENABLE is set")
	}
	switch c.Cloud {
	case CloudAliyun, CloudAWS:
	default:
		return fmt.Errorf("unsupported JFS_STS_CLOUD %q (expected %q or %q)", c.Cloud, CloudAliyun, CloudAWS)
	}
	if c.Duration < 15*time.Minute || c.Duration > 12*time.Hour {
		return fmt.Errorf("JFS_STS_DURATION must be between 15m and 12h, got %s", c.Duration)
	}
	if c.RefreshMargin <= 0 || c.RefreshMargin >= c.Duration {
		return fmt.Errorf("JFS_STS_REFRESH_MARGIN must be positive and shorter than JFS_STS_DURATION")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("JFS_STS_TIMEOUT must be positive")
	}
	if (c.SeedAccessKey == "") != (c.SeedSecretKey == "") {
		return fmt.Errorf("JFS_STS_SEED_AK and JFS_STS_SEED_SK must be set together")
	}
	if c.Policy != "" && !json.Valid([]byte(c.Policy)) {
		return fmt.Errorf("session policy is not valid JSON")
	}
	return nil
}

// LoadSeedFile reads an access key pair from a local file. Both JSON and simple
// key=value formats are accepted.
func LoadSeedFile(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read seed file %q: %w", path, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0077 != 0 {
			logger.Warnf("seed file %q is accessible by group/others; restrict it to the JuiceFS user (0600, or 0640 for a dedicated group)", path)
		}
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err == nil {
		ak := firstNonEmpty(m, "access_key_id", "AccessKeyId", "AccessKeyID", "ak")
		sk := firstNonEmpty(m, "access_key_secret", "AccessKeySecret", "AccessKeySECRET", "sk")
		if ak != "" && sk != "" {
			return ak, sk, nil
		}
	}

	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	ak := firstNonEmpty(values, "access_key_id", "AccessKeyId", "AccessKeyID", "ak")
	sk := firstNonEmpty(values, "access_key_secret", "AccessKeySecret", "AccessKeySECRET", "sk")
	if ak == "" || sk == "" {
		return "", "", fmt.Errorf("seed file %q does not contain access_key_id and access_key_secret", path)
	}
	return ak, sk, nil
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}

func boolEnv(name string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}

// durationEnv accepts either a plain number of seconds or a Go duration string
// such as "1h" or "30m".
func durationEnv(name string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

func fallbackEnv() (bool, error) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("JFS_STS_FALLBACK")))
	switch v {
	case "", "static", "true", "on", "1", "yes":
		return true, nil
	case "none", "false", "off", "0", "no", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("JFS_STS_FALLBACK must be 'static' or 'none', got %q", v)
	}
}

func defaultSessionName() string {
	host, _ := os.Hostname()
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := fmt.Sprintf("jfs-%s-%d", b.String(), os.Getpid())
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// hostOnly normalizes an endpoint to a host name, as expected by the Aliyun
// credentials-go provider.
func hostOnly(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	if i := strings.IndexByte(endpoint, '/'); i >= 0 {
		endpoint = endpoint[:i]
	}
	return endpoint
}

// normalizeEndpoint returns a URL suitable for the AWS SDK's BaseEndpoint.
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	return strings.TrimSuffix(endpoint, "/")
}
