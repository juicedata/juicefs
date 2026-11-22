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
	"fmt"
	"regexp"
	"sync"

	"github.com/juicedata/juicefs/pkg/object/credentials"
)

// Client is the cloud-specific part of the STS provider. It is kept as an
// interface so that the provider can be tested without contacting a cloud.
type Client interface {
	AssumeRole(ctx context.Context, cfg Config) (credentials.Credentials, error)
}

// Provider obtains temporary credentials by calling AssumeRole.
type Provider struct {
	client Client
	cfg    Config
	mu     sync.Mutex
}

// NewProvider builds a provider for the configured cloud.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.SessionName == "" {
		cfg.SessionName = defaultSessionName()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Provider{client: client, cfg: cfg}, nil
}

func (p *Provider) Get(ctx context.Context) (credentials.Credentials, error) {
	if p.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.Timeout)
		defer cancel()
	}

	// Some cloud SDK credential providers keep mutable internal state and are
	// not safe for concurrent use.
	p.mu.Lock()
	defer p.mu.Unlock()

	creds, err := p.client.AssumeRole(ctx, p.cfg)
	if err != nil {
		return credentials.Credentials{}, fmt.Errorf("assume role %s: %w", p.cfg.RoleARN, sanitizeError(err))
	}
	return creds, nil
}

var (
	urlQueryPattern  = regexp.MustCompile(`(https?://[^\s"?]+)\?[^\s"]*`)
	accessKeyPattern = regexp.MustCompile(`(AccessKeyId=)[^&\s"]+`)
)

// sanitizeError removes credentials and signatures that some cloud SDKs put
// into error messages (usually in request URLs) before the error is logged.
func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	msg := urlQueryPattern.ReplaceAllString(err.Error(), `$1?<redacted>`)
	msg = accessKeyPattern.ReplaceAllString(msg, `${1}<redacted>`)
	return errors.New(msg)
}

// NewClient creates the cloud-specific AssumeRole client.
func NewClient(cfg Config) (Client, error) {
	switch cfg.Cloud {
	case CloudAliyun:
		return newAliyunClient(cfg)
	case CloudAWS:
		return newAWSClient(cfg)
	default:
		return nil, fmt.Errorf("unsupported STS cloud %q", cfg.Cloud)
	}
}

// NewProviderFromEnv builds a provider from JFS_STS_* environment variables.
// When the format still contains static credentials, they are used as a
// fallback. A disabled configuration returns (nil, nil).
func NewProviderFromEnv(fallbackAccessKey, fallbackSecretKey, fallbackToken string) (credentials.Provider, error) {
	cfg, err := FromEnv()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}

	p, err := NewProvider(cfg)
	if err != nil {
		return nil, err
	}

	var secondary credentials.Provider
	if fallbackAccessKey != "" && fallbackSecretKey != "" && fallbackSecretKey != "removed" {
		if fallbackToken == "removed" {
			fallbackToken = ""
		}
		secondary = credentials.Static{Credentials: credentials.Credentials{
			AccessKeyID:     fallbackAccessKey,
			AccessKeySecret: fallbackSecretKey,
			SecurityToken:   fallbackToken,
		}}
	}
	if cfg.Fallback && secondary == nil {
		logger.Warnf("STS fallback is enabled but no static credentials are available, fallback is disabled")
	}

	return credentials.NewFallback(credentials.NewCached(p, cfg.RefreshMargin), secondary, cfg.Fallback), nil
}
