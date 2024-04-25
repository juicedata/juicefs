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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type eos struct {
	s3client
}

func (s *eos) String() string {
	return fmt.Sprintf("eos://%s/", s.s3client.bucket)
}

func (s *eos) Limits() Limits {
	return Limits{
		IsSupportMultipartUpload: true,
		IsSupportUploadPartCopy:  true,
		MinPartSize:              4 << 20,
		MaxPartSize:              5 << 30,
		MaxPartCount:             10000,
	}
}

func newEos(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	endpoint = uri.Host[len(bucket)+1:]
	region := "us-east-1"

	if accessKey == "" {
		accessKey = os.Getenv("EOS_ACCESS_KEY")
	}
	if secretKey == "" {
		secretKey = os.Getenv("EOS_SECRET_KEY")
	}
	if token == "" {
		token = os.Getenv("EOS_TOKEN")
	}

	awsConfig := &aws.Config{
		Endpoint:         &endpoint,
		Region:           &region,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(defaultPathStyle()),
		HTTPClient:       httpClient,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, token),
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("aws session: %s", err)
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &eos{s3client{bucket: bucket, s3: s3.New(ses), ses: ses}}, nil
}

func init() {
	Register("eos", newEos)
}
