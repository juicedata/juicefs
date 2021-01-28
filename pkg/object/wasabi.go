// Copyright (C) 2018-present Juicedata Inc.

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

type wasabi struct {
	s3client
}

func (s *wasabi) String() string {
	return fmt.Sprintf("wasabi://%s", s.s3client.bucket)
}

func (s *wasabi) Create() error {
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

func newWasabi(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := hostParts[2]
	endpoint = uri.Host[len(bucket)+1:]

	awsConfig := &aws.Config{
		Region:           &region,
		Endpoint:         &endpoint,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(false),
		HTTPClient:       httpClient,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
	}

	ses := session.New(awsConfig) //.WithLogLevel(aws.LogDebugWithHTTPBody))
	return &wasabi{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("wasabi", newWasabi)
}
