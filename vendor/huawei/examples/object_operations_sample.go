package examples

/**
 * This sample demonstrates how to do object-related operations
 * (such as create/delete/get/copy object, do object ACL)
 * on OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"io/ioutil"
	"obs"
	"strings"
)

type ObjectOperationsSample struct {
	bucketName string
	objectKey  string
	location   string
	obsClient  *obs.ObsClient
}

func newObjectOperationsSample(ak, sk, endpoint, bucketName, objectKey, location string) *ObjectOperationsSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &ObjectOperationsSample{obsClient: obsClient, bucketName: bucketName, objectKey: objectKey, location: location}
}

func (sample ObjectOperationsSample) CreateBucket() {
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

func (sample ObjectOperationsSample) GetObjectMeta() {
	input := &obs.GetObjectMetadataInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	output, err := sample.obsClient.GetObjectMetadata(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Object content-type:%s\n", output.ContentType)
	fmt.Printf("Object content-length:%d\n", output.ContentLength)
	fmt.Println()
}

func (sample ObjectOperationsSample) CreateObject() {
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

func (sample ObjectOperationsSample) GetObject() {
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

func (sample ObjectOperationsSample) CopyObject() {
	input := &obs.CopyObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey + "-back"
	input.CopySourceBucket = sample.bucketName
	input.CopySourceKey = sample.objectKey

	_, err := sample.obsClient.CopyObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Copy object successfully!")
	fmt.Println()
}

func (sample ObjectOperationsSample) DoObjectAcl() {
	input := &obs.SetObjectAclInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey
	input.ACL = obs.AclPublicRead

	_, err := sample.obsClient.SetObjectAcl(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set object acl successfully!")
	fmt.Println()

	output, err := sample.obsClient.GetObjectAcl(&obs.GetObjectAclInput{Bucket: sample.bucketName, Key: sample.objectKey})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Object owner - ownerId:%s, ownerName:%s\n", output.Owner.ID, output.Owner.DisplayName)
	for index, grant := range output.Grants {
		fmt.Printf("Grant[%d]\n", index)
		fmt.Printf("GranteeUri:%s, GranteeId:%s, GranteeName:%s\n", grant.Grantee.URI, grant.Grantee.ID, grant.Grantee.DisplayName)
		fmt.Printf("Permission:%s\n", grant.Permission)
	}
}

func (sample ObjectOperationsSample) DeleteObject() {
	input := &obs.DeleteObjectInput{}
	input.Bucket = sample.bucketName
	input.Key = sample.objectKey

	_, err := sample.obsClient.DeleteObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Delete object:%s successfully!\n", input.Key)
	fmt.Println()

	input.Key = sample.objectKey + "-back"

	_, err = sample.obsClient.DeleteObject(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Delete object:%s successfully!\n", input.Key)
	fmt.Println()
}

func RunObjectOperationsSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		objectKey  = "object-test"
		location   = "yourbucketlocation"
	)

	sample := newObjectOperationsSample(ak, sk, endpoint, bucketName, objectKey, location)

	fmt.Println("Create a new bucket for demo")
	sample.CreateBucket()

	sample.CreateObject()

	sample.GetObjectMeta()

	sample.GetObject()

	sample.CopyObject()

	sample.DoObjectAcl()

	sample.DeleteObject()
}
