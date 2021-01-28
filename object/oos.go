// Copyright (C) 2018-present Juicedata Inc.

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
	return fmt.Sprintf("oos://%s", s.s3client.bucket)
}

func (s *oos) Create() error {
	_, err := s.List("", "", 1)
	if err != nil {
		return fmt.Errorf("please create bucket %s manually", s.s3client.bucket)
	}
	return err
}

func (s *oos) List(prefix, marker string, limit int64) ([]*Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	objs, err := s.s3client.List(prefix, marker, limit)
	if marker != "" && len(objs) > 0 && objs[0].Key == marker {
		objs = objs[1:]
	}
	return objs, err
}

func newOOS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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

	ses := session.New(awsConfig)
	return &oos{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("oos", newOOS)
}
