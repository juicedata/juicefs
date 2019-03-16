package examples

/**
 * This sample demonstrates how to download an object concurrently
 * from OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"math/rand"
	"obs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ConcurrentDownloadObjectSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newConcurrentDownloadObjectSample(ak, sk, endpoint, bucketName, objectKey, location string) *ConcurrentDownloadObjectSample {
	obsClient, err := obs.New(ak, sk, endpoint, obs.WithPathStyle(true))
	if err != nil {
		panic(err)
	}
	return &ConcurrentDownloadObjectSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample ConcurrentDownloadObjectSample) CreateBucket() {
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

func (ConcurrentDownloadObjectSample) createSampleFile(sampleFilePath string, byteCount int64) {
	if err := os.MkdirAll(filepath.Dir(sampleFilePath), os.ModePerm); err != nil {
		panic(err)
	}

	fd, err := os.OpenFile(sampleFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
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

func (sample ConcurrentDownloadObjectSample) PutFile(sampleFilePath string) {
	input := &obs.PutFileInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.SourceFile = sampleFilePath
	_, err := sample.obsClient.PutFile(input)
	if err != nil {
		panic(err)
	}
}

func (sample ConcurrentDownloadObjectSample) DoConcurrentDownload(sampleFilePath string) {

	// Get size of the object
	getObjectMetadataInput := &obs.GetObjectMetadataInput{}
	getObjectMetadataInput.Bucket = sample.bucketName
	getObjectMetadataInput.Key = sample.objectKey
	getObjectMetadataOutput, _ := sample.obsClient.GetObjectMetadata(getObjectMetadataInput)

	objectSize := getObjectMetadataOutput.ContentLength

	// Calculate how many blocks to be divided
	// 5MB
	var partSize int64 = 1024 * 1024 * 5
	partCount := int(objectSize / partSize)

	if objectSize%partSize != 0 {
		partCount++
	}

	fmt.Printf("Total parts count %d\n", partCount)
	fmt.Println()

	downloadFilePath := filepath.Dir(sampleFilePath) + "/" + sample.objectKey

	var wg sync.WaitGroup
	wg.Add(partCount)

	fd, err := os.OpenFile(downloadFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0777)
	if err != nil {
		panic(err)
	}

	fd.Close()

	//Download the object concurrently
	fmt.Printf("Start to download %s \n", sample.objectKey)

	for i := 0; i < partCount; i++ {
		index := i + 1
		rangeStart := int64(i) * partSize
		rangeEnd := rangeStart + partSize - 1
		if index == partCount {
			rangeEnd = objectSize - 1
		}
		go func() {
			defer wg.Done()
			getObjectInput := &obs.GetObjectInput{}
			getObjectInput.Bucket = sample.bucketName
			getObjectInput.Key = sample.objectKey
			getObjectInput.RangeStart = rangeStart
			getObjectInput.RangeEnd = rangeEnd
			getObjectOutput, err := sample.obsClient.GetObject(getObjectInput)
			if err == nil {
				defer getObjectOutput.Body.Close()
				wfd, err := os.OpenFile(downloadFilePath, os.O_WRONLY, 0777)
				if err != nil {
					panic(err)
				}
				b := make([]byte, 1024)
				for {
					n, err := getObjectOutput.Body.Read(b)
					if n > 0 {
						wcnt, err := wfd.WriteAt(b[0:n], rangeStart)
						if err != nil {
							panic(err)
						}
						if n != wcnt {
							panic(fmt.Sprintf("wcnt %d, n %d", wcnt, n))
						}
						rangeStart += int64(n)
					}

					if err != nil {
						break
					}
				}
				wfd.Sync()
				wfd.Close()
				fmt.Printf("%d finished\n", index)
			} else {
				panic(err)
			}
		}()
	}
	wg.Wait()

	fmt.Printf("Download object finished, downloadPath:%s\n", downloadFilePath)
}

func RunConcurrentDownloadObjectSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)

	sample := newConcurrentDownloadObjectSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	//60MB file
	sampleFilePath := "/temp/text.txt"
	sample.createSampleFile(sampleFilePath, 1024*1024*60)
	//Upload an object to your source bucket
	sample.PutFile(sampleFilePath)

	sample.DoConcurrentDownload(sampleFilePath)

}
