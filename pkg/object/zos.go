// Copyright (C) 2018-present Juicedata Inc.

package object

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type zos struct {
	s3client
}

func (s *zos) String() string {
	return fmt.Sprintf("zos://%s", s.s3client.bucket)
}

func (s *zos) Create() error {
	_, err := s.List("", "", 1)
	if err != nil {
		return fmt.Errorf("please create bucket %s manually", s.s3client.bucket)
	}
	return err
}

func (s *zos) List(prefix, marker string, limit int64) ([]Object, error) {
	if limit > 1000 {
		limit = 1000
	}
	objs, err := s.s3client.List(prefix, marker, limit)
	if marker != "" && len(objs) > 0 && objs[0].Key() == marker {
		objs = objs[1:]
	}
	return objs, err
}

func newZos(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	region := "default"
	scheme := strings.ToLower(uri.Scheme)
	customTransport := &http.Transport{}
	if scheme == "https" {
		customTransport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	customHTTPClient := &http.Client{Transport: customTransport}
	endpoint = scheme + "://" + uri.Host[len(bucket)+1:]
	sess, _ := session.NewSession(&aws.Config{
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
		Endpoint:         &endpoint,
		Region:           &region,
		DisableSSL:       aws.Bool(true),
		HTTPClient:       customHTTPClient,
		S3ForcePathStyle: aws.Bool(true),
	})

	return &zos{s3client{bucket, s3.New(sess), sess}}, nil
}

func init() {
	Register("zos", newZos)
}
