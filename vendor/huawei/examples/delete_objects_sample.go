package examples

/**
 * This sample demonstrates how to delete objects under specified bucket
 * from OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
	"strconv"
	"strings"
)

type DeleteObjectsSample struct {
	bucketName string
	location   string
	obsClient  *obs.ObsClient
}

func newDeleteObjectsSample(ak, sk, endpoint, bucketName, location string) *DeleteObjectsSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &DeleteObjectsSample{obsClient: obsClient, bucketName: bucketName, location: location}
}

func (sample DeleteObjectsSample) CreateBucket() {
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

func (sample DeleteObjectsSample) BatchPutObjects() {
	content := "Thank you for using Object Storage Service"
	keyPrefix := "MyObjectKey"

	input := &obs.PutObjectInput{}
	input.Bucket = sample.bucketName
	input.Body = strings.NewReader(content)
	for i := 0; i < 100; i++ {
		input.Key = keyPrefix + strconv.Itoa(i)
		_, err := sample.obsClient.PutObject(input)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Succeed to put object %s\n", input.Key)
	}
}

func (sample DeleteObjectsSample) BatchDeleteObjects() {
	input := &obs.ListObjectsInput{}
	input.Bucket = sample.bucketName
	output, err := sample.obsClient.ListObjects(input)
	if err != nil {
		panic(err)
	}
	objects := make([]obs.ObjectToDelete, 0, len(output.Contents))
	for _, content := range output.Contents {
		objects = append(objects, obs.ObjectToDelete{Key: content.Key})
	}
	deleteObjectsInput := &obs.DeleteObjectsInput{}
	deleteObjectsInput.Bucket = sample.bucketName
	deleteObjectsInput.Objects = objects[:]
	deleteObjectsOutput, err := sample.obsClient.DeleteObjects(deleteObjectsInput)
	if err != nil {
		panic(err)
	}
	for _, deleted := range deleteObjectsOutput.Deleteds {
		fmt.Printf("Delete %s successfully\n", deleted.Key)
	}
	fmt.Println()
	for _, deleteError := range deleteObjectsOutput.Errors {
		fmt.Printf("Delete %s failed, code:%s, message:%s\n", deleteError.Key, deleteError.Code, deleteError.Message)
	}
	fmt.Println()
}

func RunDeleteObjectsSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		location   = "yourbucketlocation"
	)
	sample := newDeleteObjectsSample(ak, sk, endpoint, bucketName, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	// Batch put objects into the bucket
	sample.BatchPutObjects()

	// Delete all objects uploaded recently under the bucket
	sample.BatchDeleteObjects()
}
