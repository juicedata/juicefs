package examples

/**
 * This sample demonstrates how to multipart upload an object concurrently by copy mode
 * to OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"math/rand"
	"obs"
	"os"
	"path/filepath"
	"time"
)

type ConcurrentCopyPartSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newConcurrentCopyPartSample(ak, sk, endpoint, bucketName, objectKey, location string) *ConcurrentCopyPartSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &ConcurrentCopyPartSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample ConcurrentCopyPartSample) CreateBucket() {
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

func (ConcurrentCopyPartSample) createSampleFile(sampleFilePath string, byteCount int64) {
	if err := os.MkdirAll(filepath.Dir(sampleFilePath), os.ModePerm); err != nil {
		panic(err)
	}

	fd, err := os.OpenFile(sampleFilePath, os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		panic(err)
	}

	const chunkSize = 1024
	b := [chunkSize]byte{}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < chunkSize; i++ {
		b[i] = byte(uint8(r.Intn(255)))
	}

	var writedCount int64 = 0
	for {
		remainCount := byteCount - writedCount
		if remainCount <= 0 {
			break
		}
		if remainCount > chunkSize {
			fd.Write(b[:])
			writedCount += chunkSize
		} else {
			fd.Write(b[:remainCount])
			writedCount += remainCount
		}
	}

	defer fd.Close()
	fd.Sync()
}

func (sample ConcurrentCopyPartSample) PutFile(sampleFilePath string) {
	input := &obs.PutFileInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.SourceFile = sampleFilePath
	_, err := sample.obsClient.PutFile(input)
	if err != nil {
		panic(err)
	}
}

func (sample ConcurrentCopyPartSample) DoConcurrentCopyPart() {
	destBucketName := sample.bucketName
	destObjectKey := sample.objectKey + "-back"
	sourceBucketName := sample.bucketName
	sourceObjectKey := sample.objectKey
	// Claim a upload id firstly
	input := &obs.InitiateMultipartUploadInput{}
	input.Bucket = destBucketName
	input.Key = destObjectKey
	output, err := sample.obsClient.InitiateMultipartUpload(input)
	if err != nil {
		panic(err)
	}
	uploadId := output.UploadId

	fmt.Printf("Claiming a new upload id %s\n", uploadId)
	fmt.Println()

	// Get size of the object
	getObjectMetadataInput := &obs.GetObjectMetadataInput{}
	getObjectMetadataInput.Bucket = sourceBucketName
	getObjectMetadataInput.Key = sourceObjectKey
	getObjectMetadataOutput, _ := sample.obsClient.GetObjectMetadata(getObjectMetadataInput)

	objectSize := getObjectMetadataOutput.ContentLength

	// Calculate how many blocks to be divided
	// 5MB
	var partSize int64 = 5 * 1024 * 1024
	partCount := int(objectSize / partSize)

	if objectSize%partSize != 0 {
		partCount++
	}

	fmt.Printf("Total parts count %d\n", partCount)
	fmt.Println()

	//  Upload multiparts by copy mode
	fmt.Println("Begin to upload multiparts to OBS by copy mode")

	partChan := make(chan obs.Part, 5)

	for i := 0; i < partCount; i++ {
		partNumber := i + 1
		rangeStart := int64(i) * partSize
		rangeEnd := rangeStart + partSize - 1
		if i+1 == partCount {
			rangeEnd = objectSize - 1
		}
		go func() {
			copyPartInput := &obs.CopyPartInput{}
			copyPartInput.Bucket = destBucketName
			copyPartInput.Key = destObjectKey
			copyPartInput.UploadId = uploadId
			copyPartInput.PartNumber = int(partNumber)
			copyPartInput.CopySourceBucket = sourceBucketName
			copyPartInput.CopySourceKey = sourceObjectKey
			copyPartInput.CopySourceRangeStart = rangeStart
			copyPartInput.CopySourceRangeEnd = rangeEnd
			copyPartOutput, err := sample.obsClient.CopyPart(copyPartInput)
			if err == nil {
				fmt.Printf("%d finished\n", partNumber)
				partChan <- obs.Part{ETag: copyPartOutput.ETag, PartNumber: copyPartOutput.PartNumber}
			} else {
				panic(err)
			}
		}()
	}

	parts := make([]obs.Part, 0, partCount)

	for {
		part, ok := <-partChan
		if !ok {
			break
		}
		parts = append(parts, part)
		if len(parts) == partCount {
			close(partChan)
		}
	}

	fmt.Println()
	fmt.Println("Completing to upload multiparts")
	completeMultipartUploadInput := &obs.CompleteMultipartUploadInput{}
	completeMultipartUploadInput.Bucket = destBucketName
	completeMultipartUploadInput.Key = destObjectKey
	completeMultipartUploadInput.UploadId = uploadId
	completeMultipartUploadInput.Parts = parts
	_, err = sample.obsClient.CompleteMultipartUpload(completeMultipartUploadInput)
	if err != nil {
		panic(err)
	}
	fmt.Println("Complete multiparts finished")
}

func RunConcurrentCopyPartSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)

	sample := newConcurrentCopyPartSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	sampleFilePath := "/temp/text.txt"
	//60MB file
	sample.createSampleFile(sampleFilePath, 1024*1024*60)
	//Upload an object to your source bucket
	sample.PutFile(sampleFilePath)

	sample.DoConcurrentCopyPart()
}
