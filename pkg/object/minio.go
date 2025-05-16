//go:build !nos3
// +build !nos3

/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithymiddleware "github.com/aws/smithy-go/middleware"
)

type minio struct {
	s3client
	endpoint string
}

func (m *minio) String() string {
	return fmt.Sprintf("minio://%s/%s/", m.endpoint, m.s3client.bucket)
}

func (m *minio) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func newMinio(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	region := uri.Query().Get("region")
	if region == "" {
		region = os.Getenv("MINIO_REGION")
	}
	if region == "" {
		region = awsDefaultRegion
	}
	if accessKey == "" {
		accessKey = os.Getenv("MINIO_ACCESS_KEY")
	}
	if secretKey == "" {
		secretKey = os.Getenv("MINIO_SECRET_KEY")
	}
	var cfg aws.Config
	if accessKey != "" {
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, token)))
	} else {
		cfg, err = config.LoadDefaultConfig(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %s", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.Region = region
		options.BaseEndpoint = aws.String(uri.Scheme + "://" + uri.Host)
		options.EndpointOptions.DisableHTTPS = !ssl
		options.UsePathStyle = defaultPathStyle()
		options.HTTPClient = httpClient
		options.APIOptions = append(options.APIOptions, func(stack *smithymiddleware.Stack) error {
			return v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware(stack)
		})
		options.RetryMaxAttempts = 1
	})
	if len(uri.Path) < 2 {
		return nil, fmt.Errorf("no bucket name provided in %s", endpoint)
	}
	bucket := uri.Path[1:]
	if strings.Contains(bucket, "/") && strings.HasPrefix(bucket, "minio/") {
		bucket = bucket[len("minio/"):]
	}
	bucket = strings.Split(bucket, "/")[0]
	return &minio{s3client{bucket: bucket, s3: client, region: region}, endpoint}, nil
}

func init() {
	Register("minio", newMinio)
}
