package examples

/**
 * This sample demonstrates how to list versions under specified bucket
 * from OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
	"strconv"
	"strings"
)

type ListVersionsSample struct {
	bucketName string
	location   string
	obsClient  *obs.ObsClient
}

func newListVersionsSample(ak, sk, endpoint, bucketName, location string) *ListVersionsSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &ListVersionsSample{obsClient: obsClient, bucketName: bucketName, location: location}
}

func (sample ListVersionsSample) CreateBucket() {
	input := &obs.CreateBucketInput{}
	input.Bucket = sample.bucketName
	input.Location = sample.location
	_, err := sample.obsClient.CreateBucket(input)
	if err != nil {
		panic(err)
	}

	setBucketVersioningInput := &obs.SetBucketVersioningInput{}
	setBucketVersioningInput.Bucket = sample.bucketName
	setBucketVersioningInput.Status = obs.VersioningStatusEnabled
	_, err = sample.obsClient.SetBucketVersioning(setBucketVersioningInput)

	if err != nil {
		panic(err)
	}
	fmt.Printf("Create bucket:%s successfully!\n", sample.bucketName)
	fmt.Println()
}

func (sample ListVersionsSample) PrepareFoldersAndObjects() {

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
	listVersionsInput := &obs.ListVersionsInput{}
	listVersionsInput.Bucket = sample.bucketName
	output, _ := sample.obsClient.ListVersions(listVersionsInput)
	for _, version := range output.Versions {
		for i := 0; i < 2; i++ {
			objectKey := version.Key + keyPrefix + strconv.Itoa(i)
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

func (sample ListVersionsSample) ListVersionsInFolders() {
	fmt.Println("List versions in folder src0/")
	input := &obs.ListVersionsInput{}
	input.Bucket = sample.bucketName
	input.Prefix = "src0/"
	output, err := sample.obsClient.ListVersions(input)
	if err != nil {
		panic(err)
	}
	for index, val := range output.Versions {
		fmt.Printf("Version[%d]-ETag:%s, Key:%s, Size:%d, VersionId:%s\n",
			index, val.ETag, val.Key, val.Size, val.VersionId)
	}

	fmt.Println()

	fmt.Println("List versions in sub folder src0/test0/")

	input.Prefix = "src0/test0/"
	output, err = sample.obsClient.ListVersions(input)
	if err != nil {
		panic(err)
	}
	for index, val := range output.Versions {
		fmt.Printf("Version[%d]-ETag:%s, Key:%s, Size:%d, VersionId:%s\n",
			index, val.ETag, val.Key, val.Size, val.VersionId)
	}

	fmt.Println()
}

func (sample ListVersionsSample) ListVersionsByPage() {

	pageSize := 10
	pageNum := 1
	input := &obs.ListVersionsInput{}
	input.Bucket = sample.bucketName
	input.MaxKeys = pageSize

	for {
		output, err := sample.obsClient.ListVersions(input)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Page:%d\n", pageNum)
		for index, val := range output.Versions {
			fmt.Printf("Version[%d]-ETag:%s, Key:%s, Size:%d, VersionId:%s\n",
				index, val.ETag, val.Key, val.Size, val.VersionId)
		}
		if output.IsTruncated {
			input.KeyMarker = output.NextKeyMarker
			input.VersionIdMarker = output.NextVersionIdMarker
			pageNum++
		} else {
			break
		}
	}

	fmt.Println()
}

func (sample ListVersionsSample) listVersionsByPrefixes(commonPrefixes []string) {
	input := &obs.ListVersionsInput{}
	input.Bucket = sample.bucketName
	input.Delimiter = "/"
	for _, prefix := range commonPrefixes {
		input.Prefix = prefix
		output, err := sample.obsClient.ListVersions(input)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Folder %s:\n", prefix)
		for index, val := range output.Versions {
			fmt.Printf("Version[%d]-ETag:%s, Key:%s, Size:%d, VersionId:%s\n",
				index, val.ETag, val.Key, val.Size, val.VersionId)
		}
		fmt.Println()
		sample.listVersionsByPrefixes(output.CommonPrefixes)
	}
}

func (sample ListVersionsSample) ListVersionsGroupByFolder() {
	fmt.Println("List versions group by folder")
	input := &obs.ListVersionsInput{}
	input.Bucket = sample.bucketName
	input.Delimiter = "/"
	output, err := sample.obsClient.ListVersions(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Root path:")
	for index, val := range output.Versions {
		fmt.Printf("Version[%d]-ETag:%s, Key:%s, Size:%d, VersionId:%s\n",
			index, val.ETag, val.Key, val.Size, val.VersionId)
	}
	fmt.Println()
	sample.listVersionsByPrefixes(output.CommonPrefixes)
}

func (sample ListVersionsSample) BatchDeleteVersions() {
	input := &obs.ListVersionsInput{}
	input.Bucket = sample.bucketName
	output, err := sample.obsClient.ListVersions(input)
	if err != nil {
		panic(err)
	}
	objects := make([]obs.ObjectToDelete, 0, len(output.Versions))
	for _, val := range output.Versions {
		objects = append(objects, obs.ObjectToDelete{Key: val.Key, VersionId: val.VersionId})
	}
	deleteObjectsInput := &obs.DeleteObjectsInput{}
	deleteObjectsInput.Bucket = sample.bucketName
	deleteObjectsInput.Objects = objects[:]
	_, err = sample.obsClient.DeleteObjects(deleteObjectsInput)
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete versions successfully!")
	fmt.Println()
}

func RunListVersionsSample() {

	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		location   = "yourbucketlocation"
	)

	sample := newListVersionsSample(ak, sk, endpoint, bucketName, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	// First prepare folders and objects
	sample.PrepareFoldersAndObjects()

	// List versions in folders
	sample.ListVersionsInFolders()

	// List versions in way of pagination
	sample.ListVersionsByPage()

	// List versions group by folder
	sample.ListVersionsGroupByFolder()

	sample.BatchDeleteVersions()
}
