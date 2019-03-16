package examples

/**
 * This sample demonstrates how to upload multiparts to OBS
 * using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
	"strings"
)

type SimpleMultipartUploadSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newSimpleMultipartUploadSample(ak, sk, endpoint, bucketName, objectKey, location string) *SimpleMultipartUploadSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &SimpleMultipartUploadSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample SimpleMultipartUploadSample) CreateBucket() {
	input := &obs.CreateBucketInput{}
	input.Bucket = sample.bucketName
	input.Location = sample.location
	_, err := sample.obsClient.CreateBucket(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create bucket:%s successfully!\n", sample.bucketName)
	fmt.Println()
}

func (sample SimpleMultipartUploadSample) InitiateMultipartUpload() string {
	input := &obs.InitiateMultipartUploadInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	output, err := sample.obsClient.InitiateMultipartUpload(input)
	if err != nil {
		panic(err)
	}
	return output.UploadId
}

func (sample SimpleMultipartUploadSample) UploadPart(uploadId string) (string, int) {
	input := &obs.UploadPartInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.UploadId = uploadId
	input.PartNumber = 1
	input.Body = strings.NewReader("Hello OBS")
	output, err := sample.obsClient.UploadPart(input)
	if err != nil {
		panic(err)
	}
	return output.ETag, output.PartNumber
}

func (sample SimpleMultipartUploadSample) CompleteMultipartUpload(uploadId, etag string, partNumber int) {
	input := &obs.CompleteMultipartUploadInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.UploadId = uploadId
	input.Parts = []obs.Part{
		obs.Part{PartNumber: partNumber, ETag: etag},
	}
	_, err := sample.obsClient.CompleteMultipartUpload(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Upload object %s successfully!\n", sample.objectKey)
}

func RunSimpleMultipartUploadSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)
	sample := newSimpleMultipartUploadSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	// Step 1: initiate multipart upload
	fmt.Println("Step 1: initiate multipart upload")
	uploadId := sample.InitiateMultipartUpload()

	// Step 2: upload a part
	fmt.Println("Step 2: upload a part")

	etag, partNumber := sample.UploadPart(uploadId)

	// Step 3: complete multipart upload
	fmt.Println("Step 3: complete multipart upload")
	sample.CompleteMultipartUpload(uploadId, etag, partNumber)

}
