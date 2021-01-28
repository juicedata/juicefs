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
	return *m.s3client.ses.Config.Endpoint
}

func (m *minio) Create() error {
	return m.s3client.Create()
}

func newMinio(endpoint, accessKey, secretKey string) (ObjectStorage, error) {
	uri, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Invalid endpoint %s: %s", endpoint, err)
	}
	ssl := strings.ToLower(uri.Scheme) == "https"
	awsConfig := &aws.Config{
		Region:           aws.String("us-east-1"),
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

	ses := session.New(awsConfig) //.WithLogLevel(aws.LogDebugWithHTTPBody))
	bucket := uri.Path[1:]
	for strings.HasSuffix(bucket, "/") {
		bucket = bucket[:len(bucket)-1]
	}
	if strings.Contains(bucket, "/") && strings.HasPrefix(bucket, "minio/") {
		bucket = bucket[len("minio/"):]
	}
	return &minio{s3client{bucket, s3.New(ses), ses}}, nil
}

func init() {
	Register("minio", newMinio)
}
