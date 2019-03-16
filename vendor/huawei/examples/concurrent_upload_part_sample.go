package examples

/**
 * This sample demonstrates how to multipart upload an object concurrently
 * from OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"math/rand"
	"obs"
	"os"
	"path/filepath"
	"time"
)

type ConcurrentUploadPartSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newConcurrentUploadPartSample(ak, sk, endpoint, bucketName, objectKey, location string) *ConcurrentUploadPartSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &ConcurrentUploadPartSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample ConcurrentUploadPartSample) CreateBucket() {
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

func (ConcurrentUploadPartSample) createSampleFile(sampleFilePath string, byteCount int64) {
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

func (sample ConcurrentUploadPartSample) PutFile(sampleFilePath string) {
	input := &obs.PutFileInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.SourceFile = sampleFilePath
	_, err := sample.obsClient.PutFile(input)
	if err != nil {
		panic(err)
	}
}

func (sample ConcurrentUploadPartSample) DoConcurrentUploadPart(sampleFilePath string) {
	// Claim a upload id firstly
	input := &obs.InitiateMultipartUploadInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	output, err := sample.obsClient.InitiateMultipartUpload(input)
	if err != nil {
		panic(err)
	}
	uploadId := output.UploadId

	fmt.Printf("Claiming a new upload id %s\n", uploadId)
	fmt.Println()

	// Calculate how many blocks to be divided
	// 5MB
	var partSize int64 = 5 * 1024 * 1024

	stat, err := os.Stat(sampleFilePath)
	if err != nil {
		panic(err)
	}
	fileSize := stat.Size()

	partCount := int(fileSize / partSize)

	if fileSize%partSize != 0 {
		partCount++
	}
	fmt.Printf("Total parts count %d\n", partCount)
	fmt.Println()

	//  Upload parts
	fmt.Println("Begin to upload parts to OBS")

	partChan := make(chan obs.Part, 5)

	for i := 0; i < partCount; i++ {
		partNumber := i + 1
		offset := int64(i) * partSize
		currPartSize := partSize
		if i+1 == partCount {
			currPartSize = fileSize - offset
		}
		go func() {
			uploadPartInput := &obs.UploadPartInput{}
			uploadPartInput.Bucket = sample.bucketName
			uploadPartInput.Key = sample.objectKey
			uploadPartInput.UploadId = uploadId
			uploadPartInput.SourceFile = sampleFilePath
			uploadPartInput.PartNumber = partNumber
			uploadPartInput.Offset = offset
			uploadPartInput.PartSize = currPartSize
			uploadPartInputOutput, err := sample.obsClient.UploadPart(uploadPartInput)
			if err == nil {
				fmt.Printf("%d finished\n", partNumber)
				partChan <- obs.Part{ETag: uploadPartInputOutput.ETag, PartNumber: uploadPartInputOutput.PartNumber}
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
	completeMultipartUploadInput.Bucket = sample.bucketName
	completeMultipartUploadInput.Key = sample.objectKey
	completeMultipartUploadInput.UploadId = uploadId
	completeMultipartUploadInput.Parts = parts
	_, err = sample.obsClient.CompleteMultipartUpload(completeMultipartUploadInput)
	if err != nil {
		panic(err)
	}
	fmt.Println("Complete multiparts finished")
}

func RunConcurrentUploadPartSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)

	sample := newConcurrentUploadPartSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	//60MB file
	sampleFilePath := "/temp/text.txt"
	sample.createSampleFile(sampleFilePath, 1024*1024*60)

	sample.DoConcurrentUploadPart(sampleFilePath)
}
