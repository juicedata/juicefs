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
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type eos struct {
	s3client
}

func (s *eos) String() string {
	return fmt.Sprintf("eos://%s/", s.s3client.bucket)
}

func newEos(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	hostParts := strings.Split(uri.Host, ".")
	bucket := hostParts[0]
	endpoint = uri.Host[len(bucket)+1:]
	region := "us-east-1"

	if accessKey == "" {
		accessKey = os.Getenv("EOS_ACCESS_KEY")
	}
	if secretKey == "" {
		secretKey = os.Getenv("EOS_SECRET_KEY")
	}

	awsConfig := &aws.Config{
		Endpoint:         &endpoint,
		Region:           &region,
		DisableSSL:       aws.Bool(!ssl),
		S3ForcePathStyle: aws.Bool(false),
		HTTPClient:       httpClient,
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
	}

	ses, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("aws session: %s", err)
	}
	ses.Handlers.Build.PushFront(disableSha256Func)
	return &eos{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("eos", newEos)
}
