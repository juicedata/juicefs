//go:build !nos3
// +build !nos3

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

type scw struct {
	s3client
}

func (s *scw) String() string {
	return fmt.Sprintf("scw://%s/", s.s3client.bucket)
}

func (s *scw) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              5 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             1000,
	}
}

func newScw(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
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
	region := hostParts[2]
	endpoint = uri.Scheme + "://" + uri.Host[len(bucket)+1:]
	if accessKey == "" {
		accessKey = os.Getenv("SCW_ACCESS_KEY")
	}
	if secretKey == "" {
		secretKey = os.Getenv("SCW_SECRET_KEY")
	}

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, token)))
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %s", err)
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.Region = region
		options.BaseEndpoint = aws.String(endpoint)
		options.EndpointOptions.DisableHTTPS = !ssl
		options.UsePathStyle = false
		options.HTTPClient = httpClient
		options.APIOptions = append(options.APIOptions, func(stack *smithymiddleware.Stack) error {
			return v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware(stack)
		})
		options.RetryMaxAttempts = 1
	})
	return &scw{s3client{bucket: bucket, s3: client, region: region}}, nil
}

func init() {
	Register("scw", newScw)
}
