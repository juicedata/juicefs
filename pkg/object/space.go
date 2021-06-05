// +build !nos3

/*
 * JuiceFS, Copyright (C) 2018 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package object

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type space struct {
	s3client
}

func (s *space) String() string {
	return fmt.Sprintf("space://%s/", s.s3client.bucket)
}

func (s *space) Create() error {
	if _, err := s.List("", "", 1); err == nil {
		return nil
	}
	_, err := s.s3.CreateBucket(&s3.CreateBucketInput{Bucket: &s.bucket})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeBucketAlreadyExists:
				err = nil
			case s3.ErrCodeBucketAlreadyOwnedByYou:
				err = nil
			}
		}
	}
	return err
}

func newSpace(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, _ := url.ParseRequestURI(endpoint)
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[1]
	endpoint = uri.Host[len(bucket)+1:]

	awsConfig := &aws.Config{
		Region:           &region,
		Endpoint:         &endpoint,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(false),
		HTTPClient:       httpClient,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("aws session: %s", err)
	}
	return &space{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("space", newSpace)
}
