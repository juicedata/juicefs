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
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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
		Bucket:       &j.s3client.bucket,
		Key:          &dst,
		CopySource:   &src,
		StorageClass: types.StorageClass(j.sc),
	}
	_, err := j.s3client.s3.CopyObject(ctx, params)
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
	endpoint = uri.Scheme + "://" + uri.Host[len(bucket)+1:]

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, token)))
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %s", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.Region = region
		options.BaseEndpoint = aws.String(endpoint)
		options.EndpointOptions.DisableHTTPS = !ssl
		options.UsePathStyle = true
		options.HTTPClient = httpClient
	})

	return &jss{s3client{bucket: bucket, s3: client, region: region}}, nil
}

func init() {
	Register("jss", newJSS)
}
