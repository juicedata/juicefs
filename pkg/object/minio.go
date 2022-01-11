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

type minio struct {
	s3client
}

func (m *minio) String() string {
	return fmt.Sprintf("minio://%s/%s/", *m.s3client.ses.Config.Endpoint, m.s3client.bucket)
}

func newMinio(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	awsConfig := &aws.Config{
		Region:           aws.String(awsDefaultRegion),
		Endpoint:         &uri.Host,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(true),
		HTTPClient:       httpClient,
	}
	if accessKey == "" {
		accessKey = os.Getenv("MINIO_ACCESS_KEY")
	}
	if secretKey == "" {
		secretKey = os.Getenv("MINIO_SECRET_KEY")
	}
	if accessKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(accessKey, secretKey, "")
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, err
	}
	ses.Handlers.Build.PushFront(disableSha256Func)

	if len(uri.Path) < 2 {
		return nil, fmt.Errorf("no bucket name provided in %s: %s", endpoint, err)
	}
	bucket := uri.Path[1:]
	if strings.Contains(bucket, "/") && strings.HasPrefix(bucket, "minio/") {
		bucket = bucket[len("minio/"):]
	}
	bucket = strings.Split(bucket, "/")[0]
	return &minio{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("minio", newMinio)
}
