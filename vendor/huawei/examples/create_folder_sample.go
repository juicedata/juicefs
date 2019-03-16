package examples

/**
 * This sample demonstrates how to create an empty folder under
 * specified bucket to OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
)

type CreateFolderSample struct {
	bucketName string
	location   string
	obsClient  *obs.ObsClient
}

func newCreateFolderSample(ak, sk, endpoint, bucketName, location string) *CreateFolderSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &CreateFolderSample{obsClient: obsClient, bucketName: bucketName, location: location}
}

func (sample CreateFolderSample) CreateBucket() {
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

func RunCreateFolderSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		location   = "yourbucketlocation"
	)
	sample := newCreateFolderSample(ak, sk, endpoint, bucketName, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	keySuffixWithSlash1 := "MyObjectKey1/"
	keySuffixWithSlash2 := "MyObjectKey2/"

	// Create two empty folder without request body, note that the key must be suffixed with a slash
	var input = &obs.PutObjectInput{}
	input.Bucket = bucketName
	input.Key = keySuffixWithSlash1

	_, err := sample.obsClient.PutObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create empty folder:%s successfully!\n", keySuffixWithSlash1)
	fmt.Println()

	input.Key = keySuffixWithSlash2
	_, err = sample.obsClient.PutObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create empty folder:%s successfully!\n", keySuffixWithSlash2)
	fmt.Println()

	// Verify whether the size of the empty folder is zero
	var input2 = &obs.GetObjectMetadataInput{}
	input2.Bucket = bucketName
	input2.Key = keySuffixWithSlash1
	output, err := sample.obsClient.GetObjectMetadata(input2)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Size of the empty folder %s is %d \n", keySuffixWithSlash1, output.ContentLength)
	fmt.Println()

	input2.Key = keySuffixWithSlash2
	output, err = sample.obsClient.GetObjectMetadata(input2)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Size of the empty folder %s is %d \n", keySuffixWithSlash2, output.ContentLength)
	fmt.Println()

}
