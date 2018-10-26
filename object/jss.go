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

type jss struct {
	s3client
}

func (j *jss) String() string {
	return fmt.Sprintf("jss://%s", j.s3client.bucket)
}

func (j *jss) Copy(dst, src string) error {
	src = "/" + j.s3client.bucket + "/" + src
	params := &s3.CopyObjectInput{
		Bucket:     &j.s3client.bucket,
		Key:        &dst,
		CopySource: &src,
	}
	_, err := j.s3client.s3.CopyObject(params)
	return err
}

func newJSS(endpoint, accessKey, secretKey string) ObjectStorage {
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
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
	}

	ses := session.New(awsConfig) //.WithLogLevel(aws.LogDebugWithHTTPBody))
	return &jss{s3client{bucket, s3.New(ses), ses}}
}

func init() {
	register("jss", newJSS)
}
