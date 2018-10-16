// +build integration

// Package s3manager provides
package s3manager

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/internal/test/integration"
	"github.com/ks3sdklib/aws-sdk-go/service/s3"
	"github.com/ks3sdklib/aws-sdk-go/service/s3/s3manager"
	"github.com/stretchr/testify/assert"
)

var integBuf12MB = make([]byte, 1024*1024*12)
var integMD512MB = fmt.Sprintf("%x", md5.Sum(integBuf12MB))
var bucketName *string
var _ = integration.Imported

func TestMain(m *testing.M) {
	setup()
	defer teardown() // only called if we panic
	result := m.Run()
	teardown()
	os.Exit(result)
}

func setup() {
	// Create a bucket for testing
	svc := s3.New(nil)
	bucketName = aws.String(
		fmt.Sprintf("aws-sdk-go-integration-%d-%s", time.Now().Unix(), integration.UniqueID()))

	for i := 0; i < 10; i++ {
		_, err := svc.CreateBucket(&s3.CreateBucketInput{Bucket: bucketName})
		if err == nil {
			break
		}
	}

	for {
		_, err := svc.HeadBucket(&s3.HeadBucketInput{Bucket: bucketName})
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
}

// Delete the bucket
func teardown() {
	svc := s3.New(nil)

	objs, _ := svc.ListObjects(&s3.ListObjectsInput{Bucket: bucketName})
	for _, o := range objs.Contents {
		svc.DeleteObject(&s3.DeleteObjectInput{Bucket: bucketName, Key: o.Key})
	}

	uploads, _ := svc.ListMultipartUploads(&s3.ListMultipartUploadsInput{Bucket: bucketName})
	for _, u := range uploads.Uploads {
		svc.AbortMultipartUpload(&s3.AbortMultipartUploadInput{
			Bucket:   bucketName,
			Key:      u.Key,
			UploadID: u.UploadID,
		})
	}

	svc.DeleteBucket(&s3.DeleteBucketInput{Bucket: bucketName})
}

type dlwriter struct {
	buf []byte
}

func newDLWriter(size int) *dlwriter {
	return &dlwriter{buf: make([]byte, size)}
}

func (d dlwriter) WriteAt(p []byte, pos int64) (n int, err error) {
	if pos > int64(len(d.buf)) {
		return 0, io.EOF
	}

	written := 0
	for i, b := range p {
		if i >= len(d.buf) {
			break
		}
		d.buf[pos+int64(i)] = b
		written++
	}
	return written, nil
}

func validate(t *testing.T, key string, md5value string) {
	mgr := s3manager.NewDownloader(nil)
	params := &s3.GetObjectInput{Bucket: bucketName, Key: &key}

	w := newDLWriter(1024 * 1024 * 20)
	n, err := mgr.Download(w, params)
	assert.NoError(t, err)
	assert.Equal(t, md5value, fmt.Sprintf("%x", md5.Sum(w.buf[0:n])))
}

func TestUploadConcurrently(t *testing.T) {
	key := "12mb-1"
	mgr := s3manager.NewUploader(nil)
	out, err := mgr.Upload(&s3manager.UploadInput{
		Bucket: bucketName,
		Key:    &key,
		Body:   bytes.NewReader(integBuf12MB),
	})

	assert.NoError(t, err)
	assert.NotEqual(t, "", out.UploadID)
	assert.Regexp(t, `^https?://.+/`+key+`$`, out.Location)

	validate(t, key, integMD512MB)
}

func TestUploadFailCleanup(t *testing.T) {
	svc := s3.New(nil)

	// Break checksum on 2nd part so it fails
	part := 0
	svc.Handlers.Build.PushBack(func(r *aws.Request) {
		if r.Operation.Name == "UploadPart" {
			if part == 1 {
				r.HTTPRequest.Header.Set("X-Amz-Content-Sha256", "000")
			}
			part++
		}
	})

	key := "12mb-leave"
	mgr := s3manager.NewUploader(&s3manager.UploadOptions{
		S3:                svc,
		LeavePartsOnError: false,
	})
	_, err := mgr.Upload(&s3manager.UploadInput{
		Bucket: bucketName,
		Key:    &key,
		Body:   bytes.NewReader(integBuf12MB),
	})
	assert.Error(t, err)
	uploadID := ""
	if merr, ok := err.(s3manager.MultiUploadFailure); ok {
		uploadID = merr.UploadID()
	}
	assert.NotEmpty(t, uploadID)

	_, err = svc.ListParts(&s3.ListPartsInput{
		Bucket: bucketName, Key: &key, UploadID: &uploadID})
	assert.Error(t, err)
}
