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
	"fmt"
	"time"

	"github.com/aliyun/credentials-go/credentials/providers"

	"github.com/juicedata/juicefs/pkg/object/credentials"
)

type aliyunClient struct {
	provider *providers.RAMRoleARNCredentialsProvider
}

func newAliyunClient(cfg Config) (*aliyunClient, error) {
	var previous providers.CredentialsProvider
	if cfg.SeedAccessKey != "" {
		p, err := providers.NewStaticAKCredentialsProviderBuilder().
			WithAccessKeyId(cfg.SeedAccessKey).
			WithAccessKeySecret(cfg.SeedSecretKey).
			Build()
		if err != nil {
			return nil, fmt.Errorf("build static seed credentials: %w", err)
		}
		previous = p
	} else {
		// No seed key: let credentials-go use its default chain (environment,
		// OIDC, profile, ECS instance role, ...).
		previous = providers.NewDefaultCredentialsProvider()
	}

	builder := providers.NewRAMRoleARNCredentialsProviderBuilder().
		WithCredentialsProvider(previous).
		WithRoleArn(cfg.RoleARN).
		WithRoleSessionName(cfg.SessionName).
		WithDurationSeconds(int(cfg.Duration.Seconds())).
		WithHttpOptions(&providers.HttpOptions{
			ConnectTimeout: int(cfg.Timeout.Milliseconds()),
			ReadTimeout:    int(cfg.Timeout.Milliseconds()),
		})
	if cfg.Endpoint != "" {
		builder = builder.WithStsEndpoint(hostOnly(cfg.Endpoint))
	}
	if cfg.Region != "" {
		builder = builder.WithStsRegionId(cfg.Region)
	}
	if cfg.Policy != "" {
		builder = builder.WithPolicy(cfg.Policy)
	}

	p, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build Aliyun STS provider: %w", err)
	}
	return &aliyunClient{provider: p}, nil
}

func (c *aliyunClient) AssumeRole(_ context.Context, _ Config) (credentials.Credentials, error) {
	cred, err := c.provider.GetCredentials()
	if err != nil {
		return credentials.Credentials{}, err
	}
	if cred.AccessKeyId == "" || cred.AccessKeySecret == "" {
		return credentials.Credentials{}, fmt.Errorf("Aliyun STS returned empty credentials")
	}

	// credentials-go does not expose the expiration time of the credentials it
	// returns. It refreshes them three minutes before the real expiration, so a
	// three-minute lease is a safe lower bound and also gives us a chance to
	// notice a broken STS endpoint reasonably quickly.
	expires := time.Now().Add(3 * time.Minute)
	return credentials.Credentials{
		AccessKeyID:     cred.AccessKeyId,
		AccessKeySecret: cred.AccessKeySecret,
		SecurityToken:   cred.SecurityToken,
		Expires:         expires,
	}, nil
}
