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

type jss struct {
	s3client
}

func (j *jss) String() string {
	return fmt.Sprintf("jss://%s/", j.s3client.bucket)
}

func (j *jss) Copy(dst, src string) error {
	src = "/" + j.s3client.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &j.s3client.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	if j.sc != "" {
		params.SetStorageClass(j.sc)
	}
	_, err := j.s3client.s3.CopyObject(params)
	return err
}

func newJSS(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = fmt.Sprintf("https://%s", endpoint)
	}
	uri, _ := url.ParseRequestURI(endpoint)
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[2]
	endpoint = uri.Host[len(bucket)+1:]

	awsConfig := &aws.Config{
		Region:           &region,
		Endpoint:         &endpoint,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(true),
		HTTPClient:       httpClient,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, token),
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, err
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &jss{s3client{bucket: bucket, s3: s3.New(ses), ses: ses}}, nil
}

func init() {
	Register("jss", newJSS)
}
