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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type oos struct {
	s3client
}

func (s *oos) String() string {
	return fmt.Sprintf("oos://%s/", s.s3client.bucket)
}

func (s *oos) Create() error {
	_, err := s.List("", "", 1)
	if err != nil {
		return fmt.Errorf("please create bucket %s manually", s.s3client.bucket)
	}
	return err
}

func (s *oos) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	objs, err := s.s3client.List(prefix, marker, limit)
	if marker != "" && len(objs) > 0 && objs[0].Key() == marker {
		objs = objs[1:]
	}
	return objs, err
}

func newOOS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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
	endpoint = uri.Host[len(bucket)+1:]
	forcePathStyle := strings.Contains(strings.ToLower(endpoint), "xstore.ctyun.cn")

	awsConfig := &aws.Config{
		Region:           &region,
		Endpoint:         &endpoint,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(!forcePathStyle),
		HTTPClient:       httpClient,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("OOS session: %s", err)
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &oos{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("oos", newOOS)
}
