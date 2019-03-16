package examples

/**
 * This sample demonstrates how to list objects under a specified folder of a bucket
 * from OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
	"strconv"
	"strings"
)

type ListObjectsInFolderSample struct {
	bucketName string
	location   string
	obsClient  *obs.ObsClient
}

func newListObjectsInFolderSample(ak, sk, endpoint, bucketName, location string) *ListObjectsInFolderSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &ListObjectsInFolderSample{obsClient: obsClient, bucketName: bucketName, location: location}
}

func (sample ListObjectsInFolderSample) CreateBucket() {
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

func (sample ListObjectsInFolderSample) PrepareFoldersAndObjects() {

	keyPrefix := "MyObjectKey"
	folderPrefix := "src"
	subFolderPrefix := "test"

	input := &obs.PutObjectInput{}
	input.Bucket = sample.bucketName

	// First prepare folders and sub folders
	for i := 0; i < 5; i++ {
		key := folderPrefix + strconv.Itoa(i) + "/"
		input.Key = key
		sample.obsClient.PutObject(input)
		for j := 0; j < 3; j++ {
			subKey := key + subFolderPrefix + strconv.Itoa(j) + "/"
			input.Key = subKey
			sample.obsClient.PutObject(input)
		}
	}

	// Insert 2 objects in each folder
	input.Body = strings.NewReader("Hello OBS")
	listObjectsInput := &obs.ListObjectsInput{}
	listObjectsInput.Bucket = sample.bucketName
	output, _ := sample.obsClient.ListObjects(listObjectsInput)
	for _, content := range output.Contents {
		for i := 0; i < 2; i++ {
			objectKey := content.Key + keyPrefix + strconv.Itoa(i)
			input.Key = objectKey
			sample.obsClient.PutObject(input)
		}
	}

	// Insert 2 objects in root path
	input.Key = keyPrefix + strconv.Itoa(0)
	sample.obsClient.PutObject(input)
	input.Key = keyPrefix + strconv.Itoa(1)
	sample.obsClient.PutObject(input)

	fmt.Println("Prepare folders and objects finished")
	fmt.Println()
}

func (sample ListObjectsInFolderSample) ListObjectsInFolders() {
	fmt.Println("List objects in folder src0/")
	input := &obs.ListObjectsInput{}
	input.Bucket = sample.bucketName
	input.Prefix = "src0/"
	output, err := sample.obsClient.ListObjects(input)
	if err != nil {
		panic(err)
	}
	for index, val := range output.Contents {
		fmt.Printf("Content[%d]-ETag:%s, Key:%s, Size:%d\n",
			index, val.ETag, val.Key, val.Size)
	}

	fmt.Println()

	fmt.Println("List objects in sub folder src0/test0/")

	input.Prefix = "src0/test0/"
	output, err = sample.obsClient.ListObjects(input)
	if err != nil {
		panic(err)
	}
	for index, val := range output.Contents {
		fmt.Printf("Content[%d]-ETag:%s, Key:%s, Size:%d\n",
			index, val.ETag, val.Key, val.Size)
	}

	fmt.Println()
}

func (sample ListObjectsInFolderSample) listObjectsByPrefixes(commonPrefixes []string) {
	input := &obs.ListObjectsInput{}
	input.Bucket = sample.bucketName
	input.Delimiter = "/"
	for _, prefix := range commonPrefixes {
		input.Prefix = prefix
		output, err := sample.obsClient.ListObjects(input)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Folder %s:\n", prefix)
		for index, val := range output.Contents {
			fmt.Printf("Content[%d]-ETag:%s, Key:%s, Size:%d\n",
				index, val.ETag, val.Key, val.Size)
		}
		fmt.Println()
		sample.listObjectsByPrefixes(output.CommonPrefixes)
	}
}

func (sample ListObjectsInFolderSample) ListObjectsGroupByFolder() {
	fmt.Println("List objects group by folder")
	input := &obs.ListObjectsInput{}
	input.Bucket = sample.bucketName
	input.Delimiter = "/"
	output, err := sample.obsClient.ListObjects(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Root path:")
	for index, val := range output.Contents {
		fmt.Printf("Content[%d]-ETag:%s, Key:%s, Size:%d\n",
			index, val.ETag, val.Key, val.Size)
	}
	fmt.Println()
	sample.listObjectsByPrefixes(output.CommonPrefixes)
}

func (sample ListObjectsInFolderSample) BatchDeleteObjects() {
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
	_, err = sample.obsClient.DeleteObjects(deleteObjectsInput)
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete objects successfully!")
	fmt.Println()
}

func RunListObjectsInFolderSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		location   = "yourbucketlocation"
	)

	sample := newListObjectsInFolderSample(ak, sk, endpoint, bucketName, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	// First prepare folders and objects
	sample.PrepareFoldersAndObjects()

	// List objects in folders
	sample.ListObjectsInFolders()

	// List all objects group by folder
	sample.ListObjectsGroupByFolder()

	sample.BatchDeleteObjects()
}
