package examples

/**
 * This sample demonstrates how to download an cold object
 * from OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"io/ioutil"
	"obs"
	"strings"
	"time"
)

type RestoreObjectSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newRestoreObjectSample(ak, sk, endpoint, bucketName, objectKey, location string) *RestoreObjectSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &RestoreObjectSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample RestoreObjectSample) CreateColdBucket() {
	input := &obs.CreateBucketInput{}
	input.Bucket = sample.bucketName
	input.Location = sample.location
	input.StorageClass = obs.StorageClassCold
	_, err := sample.obsClient.CreateBucket(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create cold bucket:%s successfully!\n", sample.bucketName)
	fmt.Println()
}

func (sample RestoreObjectSample) CreateObject() {
	input := &obs.PutObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.Body = strings.NewReader("Hello OBS")

	_, err := sample.obsClient.PutObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create object:%s successfully!\n", sample.objectKey)
	fmt.Println()
}

func (sample RestoreObjectSample) RestoreObject() {
	input := &obs.RestoreObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.Days = 1
	input.Tier = obs.RestoreTierExpedited

	_, err := sample.obsClient.RestoreObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create object:%s successfully!\n", sample.objectKey)
	fmt.Println()
}

func (sample RestoreObjectSample) GetObject() {
	input := &obs.GetObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey

	output, err := sample.obsClient.GetObject(input)
	if err != nil {
		panic(err)
	}
	defer output.Body.Close()
	fmt.Println("Object content:")
	body, _ := ioutil.ReadAll(output.Body)
	fmt.Println(string(body))
	fmt.Println()
}

func (sample RestoreObjectSample) DeleteObject() {
	input := &obs.DeleteObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	_, err := sample.obsClient.DeleteObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Delete object:%s successfully!\n", input.Key)
	fmt.Println()
}

func RunRestoreObjectSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test-cold"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)

	sample := newRestoreObjectSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new cold bucket for demo")
	sample.CreateColdBucket()

	sample.CreateObject()

	sample.RestoreObject()

	// Wait 6 minutes to get the object
	time.Sleep(time.Duration(6*60) * time.Second)

	sample.GetObject()

	sample.DeleteObject()
}
