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
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/juicedata/juicefs/pkg/object/credentials"
)

type awsClient struct {
	client *awssts.Client
}

func newAWSClient(cfg Config) (*awsClient, error) {
	region := cfg.Region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if cfg.SeedAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			awscreds.NewStaticCredentialsProvider(cfg.SeedAccessKey, cfg.SeedSecretKey, "")))
	}
	if cfg.Endpoint != "" {
		opts = append(opts, awsconfig.WithBaseEndpoint(normalizeEndpoint(cfg.Endpoint)))
	}

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &awsClient{client: awssts.NewFromConfig(awsCfg)}, nil
}

func (c *awsClient) AssumeRole(ctx context.Context, cfg Config) (credentials.Credentials, error) {
	input := &awssts.AssumeRoleInput{
		RoleArn:         aws.String(cfg.RoleARN),
		RoleSessionName: aws.String(cfg.SessionName),
		DurationSeconds: aws.Int32(int32(cfg.Duration.Seconds())),
	}
	if cfg.Policy != "" {
		input.Policy = aws.String(cfg.Policy)
	}

	out, err := c.client.AssumeRole(ctx, input)
	if err != nil {
		return credentials.Credentials{}, err
	}
	if out.Credentials == nil {
		return credentials.Credentials{}, fmt.Errorf("empty credentials in AssumeRole response")
	}

	if out.Credentials.Expiration == nil {
		return credentials.Credentials{}, fmt.Errorf("empty expiration in AssumeRole response")
	}
	accessKeyID := aws.ToString(out.Credentials.AccessKeyId)
	secretKey := aws.ToString(out.Credentials.SecretAccessKey)
	if accessKeyID == "" || secretKey == "" {
		return credentials.Credentials{}, fmt.Errorf("empty credentials in AssumeRole response")
	}
	return credentials.Credentials{
		AccessKeyID:     accessKeyID,
		AccessKeySecret: secretKey,
		SecurityToken:   aws.ToString(out.Credentials.SessionToken),
		Expires:         *out.Credentials.Expiration,
	}, nil
}
