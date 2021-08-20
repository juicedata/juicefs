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
	_, err := j.s3client.s3.CopyObject(params)
	return err
}

func newJSS(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
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

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, err
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &jss{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("jss", newJSS)
}
