package examples

/**
 * This sample demonstrates how to set/get self-defined metadata for object
 * on OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
	"strings"
)

type ObjectMetaSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newObjectMetaSample(ak, sk, endpoint, bucketName, objectKey, location string) *ObjectMetaSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &ObjectMetaSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample ObjectMetaSample) CreateBucket() {
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

func (sample ObjectMetaSample) SetObjectMeta() {
	input := &obs.PutObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.Body = strings.NewReader("Hello OBS")
	// Setting object mime type
	input.ContentType = "text/plain"
	// Setting self-defined metadata
	input.Metadata = map[string]string{"meta1": "value1", "meta2": "value2"}
	_, err := sample.obsClient.PutObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set object meatdata successfully!")
	fmt.Println()
}

func (sample ObjectMetaSample) GetObjectMeta() {
	input := &obs.GetObjectMetadataInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	output, err := sample.obsClient.GetObjectMetadata(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Object content-type:%s\n", output.ContentType)
	for key, val := range output.Metadata {
		fmt.Printf("%s:%s\n", key, val)
	}
	fmt.Println()
}
func (sample ObjectMetaSample) DeleteObject() {
	input := &obs.DeleteObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey

	_, err := sample.obsClient.DeleteObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Delete object:%s successfully!\n", sample.objectKey)
	fmt.Println()
}

func RunObjectMetaSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)
	sample := newObjectMetaSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	sample.SetObjectMeta()

	sample.GetObjectMeta()

	sample.DeleteObject()
}
