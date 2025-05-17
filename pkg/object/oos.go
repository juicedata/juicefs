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
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithymiddleware "github.com/aws/smithy-go/middleware"
)

type oos struct {
	s3client
}

func (s *oos) String() string {
	return fmt.Sprintf("oos://%s/", s.s3client.bucket)
}

func (s *oos) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func (s *oos) Create() error {
	_, _, _, err := s.List("", "", "", "", 1, true)
	if err != nil {
		return fmt.Errorf("please create bucket %s manually", s.s3client.bucket)
	}
	return err
}

func (s *oos) List(prefix, start, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	if limit > 1000 {
		limit = 1000
	}
	objs, hasMore, nextMarker, err := s.s3client.List(prefix, start, token, delimiter, limit, followLink)
	if start != "" && len(objs) > 0 && objs[0].Key() == start {
		objs = objs[1:]
	}
	return objs, hasMore, nextMarker, err
}

func newOOS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[1][4:]
	endpoint = uri.Scheme + "://" + uri.Host[len(bucket)+1:]
	forcePathStyle := !strings.Contains(strings.ToLower(endpoint), "xstore.ctyun.cn")

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, token)))
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %s", err)
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.EndpointOptions.DisableHTTPS = !ssl
		options.Region = region
		options.UsePathStyle = forcePathStyle
		options.HTTPClient = httpClient
		options.BaseEndpoint = aws.String(endpoint)
		options.APIOptions = append(options.APIOptions, func(stack *smithymiddleware.Stack) error {
			return v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware(stack)
		})
		options.RetryMaxAttempts = 1
	})
	return &oos{s3client{bucket: bucket, s3: client, region: region}}, nil
}

func init() {
	Register("oos", newOOS)
}
