package nosclient

import (
	"github.com/NetEase-Object-Storage/nos-golang-sdk/config"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/logger"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/model"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/nosconst"
	. "gopkg.in/check.v1"
	"os"
	"strings"
	"testing"
	"time"
)

func Test(t *testing.T) { TestingT(t) }

type NosClientTestSuite struct {
	nosClient *NosClient
}

func (s *NosClientTestSuite) SetUpSuite(c *C) {

	conf := &config.Config{
		Endpoint:  "nos-eastchina1.126.net",
		AccessKey: "your accesskey",
		SecretKey: "your secretkey",

		NosServiceConnectTimeout:    3,
		NosServiceReadWriteTimeout:  60,
		NosServiceMaxIdleConnection: 100,

		LogLevel: logger.LogLevel(logger.DEBUG),
		Logger:   logger.NewDefaultLogger(),
	}

	s.nosClient, _ = New(conf)
}

var _ = Suite(&NosClientTestSuite{})

const (
	TEST_BUCKET    = "gosdktest"
	PUTOBJECTFILE  = "fortest"
	BIGFILEOBJECT  = "bigfile.mp4"
	OBJECTMD5      = "2b04df3ecc1d94afddff082d139c6f15"
	ERROBJECTMD5   = "029ed5acc233ba3403e1a87ca54d1839"
	SRCMD5         = "ba92b109c212245a8b8d4f1f5e46f0b6"
	SPECIALOBJECT  = "特殊   字符`-=[]\\;',./ ~!@#$%^&*()_+{}|:\"<>?"
	SPECIALBUCKET  = "ab._a"
	BUCKETNOTEXIST = "1a2b3c4dexist"
)

func (s *NosClientTestSuite) TestCreateBucket(c *C) {
	err := s.nosClient.CreateBucket("sjltestbucket-public", nosconst.HZ, nosconst.PUBLICREAD)
	c.Assert(err, IsNil)
}

func (s *NosClientTestSuite) TestPutObjectByStream(c *C) {

	//test put object ok
	file, err := os.Open("../test/" + PUTOBJECTFILE)
	c.Assert(err, IsNil)

	fi, err := file.Stat()
	c.Assert(err, IsNil)

	contentLength := fi.Size()
	contentType := "text/plain"

	c.Assert(err, IsNil)

	metadata := &model.ObjectMetadata{
		ContentLength: contentLength,
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE:     contentType,
			nosconst.ORIG_CONTENT_MD5: OBJECTMD5,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		Body:     file,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByStream(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	file.Close()

	//test bucket not exit
	file, err = os.Open("../test/" + PUTOBJECTFILE)
	c.Assert(err, IsNil)

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET + "notexistedtestbucket7890",
		Object:   PUTOBJECTFILE,
		Body:     file,
		Metadata: metadata,
	}

	result, err = s.nosClient.PutObjectByStream(putObjectRequest)
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)

	file.Close()

	//test Invalid bucketname
	file, err = os.Open("../test/" + PUTOBJECTFILE)
	c.Assert(err, IsNil)

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   "",
		Object:   PUTOBJECTFILE,
		Body:     file,
		Metadata: metadata,
	}

	result, err = s.nosClient.PutObjectByStream(putObjectRequest)
	c.Assert(err.Error(), Equals, "StatusCode = 430, Resource = , Message = InvalidBucketName")
	c.Assert(result, IsNil)

	file.Close()

	//test md5 different
	file, err = os.Open("../test/" + PUTOBJECTFILE)
	c.Assert(err, IsNil)
	metadata.Metadata[nosconst.ORIG_CONTENT_MD5] = ERROBJECTMD5
	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		Body:     file,
		Metadata: metadata,
	}

	result, err = s.nosClient.PutObjectByStream(putObjectRequest)
	c.Assert(result, IsNil)
	c.Assert(strings.Contains(err.Error(), "BadDigest"), Equals, true)

	result, err = s.nosClient.PutObjectByStream(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestPutObjectByFile(c *C) {

	//test without contentlength
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE:     contentType,
			nosconst.ORIG_CONTENT_MD5: OBJECTMD5,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	//test big file
	metadata = &model.ObjectMetadata{
		ContentLength: 1024000000,
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}
	_, err = s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, NotNil)

	//test special object
	metadata = &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   SPECIALOBJECT,
		FilePath: path,
		Metadata: metadata,
	}
	result, err = s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	//test special bucket
	metadata = &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   SPECIALBUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}
	result, err = s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err.Error(), Equals, "StatusCode = 430, Resource = /ab._a, Message = InvalidBucketName")

	result, err = s.nosClient.PutObjectByFile(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestDoesObjectExist(c *C) {

	//does object exist
	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: PUTOBJECTFILE + "not_exist",
	}
	isExist, err := s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	isExist, err = s.nosClient.DoesObjectExist(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")

	//upload file
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE:     contentType,
			nosconst.ORIG_CONTENT_MD5: OBJECTMD5,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	objectRequest = &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: PUTOBJECTFILE,
	}
	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, true)

	objectRequest.Bucket = BUCKETNOTEXIST
	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	objectRequest.Bucket = SPECIALBUCKET
	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(err, NotNil)
}

func (s *NosClientTestSuite) TestCopyObject(c *C) {

	//upload src file
	path := "../test/" + "testserver"
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE:     contentType,
			nosconst.ORIG_CONTENT_MD5: SRCMD5,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   "testserver",
		FilePath: path,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, SRCMD5)

	//copy file
	copyRequest := &model.CopyObjectRequest{
		SrcBucket:  TEST_BUCKET,
		SrcObject:  "testserver",
		DestBucket: TEST_BUCKET,
		DestObject: "CopiedTest",
	}
	err = s.nosClient.CopyObject(copyRequest)
	c.Assert(err, IsNil)

	//test special destobject
	copyRequest = &model.CopyObjectRequest{
		SrcBucket:  TEST_BUCKET,
		SrcObject:  "testserver",
		DestBucket: TEST_BUCKET,
		DestObject: SPECIALOBJECT,
	}
	err = s.nosClient.CopyObject(copyRequest)
	c.Assert(err, IsNil)

	copyRequest.SrcBucket = SPECIALBUCKET
	err = s.nosClient.CopyObject(copyRequest)
	c.Assert(err, NotNil)
	copyRequest.SrcBucket = BUCKETNOTEXIST
	err = s.nosClient.CopyObject(copyRequest)
	c.Assert(err, NotNil)

	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: SPECIALOBJECT,
	}
	isExist, err := s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, true)

	err = s.nosClient.CopyObject(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestMoveObject(c *C) {

	//upload src file
	path := "../test/" + "testserver"
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE:     contentType,
			nosconst.ORIG_CONTENT_MD5: SRCMD5,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   "testserver",
		FilePath: path,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, SRCMD5)

	//move file
	moveRequest := &model.MoveObjectRequest{
		SrcBucket:  TEST_BUCKET,
		SrcObject:  "testserver",
		DestBucket: TEST_BUCKET,
		DestObject: "MovedTest",
	}
	err = s.nosClient.MoveObject(moveRequest)
	c.Assert(err, IsNil)

	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: "MovedTest",
	}
	isExist, err := s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, true)

	objectRequest = &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: "testserver",
	}
	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	//test special destobject
	moveRequest = &model.MoveObjectRequest{
		SrcBucket:  TEST_BUCKET,
		SrcObject:  "MovedTest",
		DestBucket: TEST_BUCKET,
		DestObject: SPECIALOBJECT,
	}
	err = s.nosClient.MoveObject(moveRequest)
	c.Assert(err, IsNil)

	objectRequest = &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: SPECIALOBJECT,
	}
	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, true)

	moveRequest.SrcBucket = SPECIALBUCKET
	err = s.nosClient.MoveObject(moveRequest)
	c.Assert(err, NotNil)

	moveRequest.SrcBucket = BUCKETNOTEXIST
	err = s.nosClient.MoveObject(moveRequest)
	c.Assert(err, NotNil)

	err = s.nosClient.MoveObject(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestDeleteObject(c *C) {

	//upload file
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	//delete file
	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: PUTOBJECTFILE,
	}

	err = s.nosClient.DeleteObject(objectRequest)
	c.Assert(err, IsNil)

	//check file
	isExist, err := s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	//upload special file
	metadata = &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   SPECIALOBJECT,
		FilePath: path,
		Metadata: metadata,
	}

	result, err = s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	//delete file
	objectRequest = &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: SPECIALOBJECT,
	}

	err = s.nosClient.DeleteObject(objectRequest)
	c.Assert(err, IsNil)

	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	objectRequest.Bucket = SPECIALBUCKET
	err = s.nosClient.DeleteObject(objectRequest)
	c.Assert(err, NotNil)

	objectRequest.Bucket = BUCKETNOTEXIST
	err = s.nosClient.DeleteObject(objectRequest)
	c.Assert(err, NotNil)

	err = s.nosClient.DeleteObject(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestDeleteMultilObjects(c *C) {

	//upload file
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	_, err := s.nosClient.PutObjectByFile(putObjectRequest)

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   SPECIALOBJECT,
		FilePath: path,
		Metadata: metadata,
	}
	_, err = s.nosClient.PutObjectByFile(putObjectRequest)

	//delete  files
	deleteObject1 := model.DeleteObject{
		Key: PUTOBJECTFILE,
	}
	deleteObject2 := model.DeleteObject{
		Key: SPECIALOBJECT,
	}
	deleteMultiObjects := &model.DeleteMultiObjects{
		Quiet: false,
	}
	deleteMultiObjects.Append(deleteObject1)
	deleteMultiObjects.Append(deleteObject2)
	deleteMultiObjectsRequest := &model.DeleteMultiObjectsRequest{
		Bucket:        TEST_BUCKET,
		DelectObjects: deleteMultiObjects,
	}

	deleteResult, err := s.nosClient.DeleteMultiObjects(deleteMultiObjectsRequest)
	c.Assert(err, IsNil)
	c.Assert(len(deleteResult.Deleted), Equals, 2)

	//check file
	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: PUTOBJECTFILE,
	}
	isExist, err := s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	objectRequest = &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: SPECIALOBJECT,
	}
	isExist, err = s.nosClient.DoesObjectExist(objectRequest)
	c.Assert(isExist, Equals, false)

	deleteMultiObjectsRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.DeleteMultiObjects(deleteMultiObjectsRequest)
	c.Assert(err, NotNil)

	deleteMultiObjectsRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.DeleteMultiObjects(deleteMultiObjectsRequest)
	c.Assert(err, NotNil)

	_, err = s.nosClient.DeleteMultiObjects(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestGetObjects(c *C) {

	//upload file
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	_, err := s.nosClient.PutObjectByFile(putObjectRequest)

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   SPECIALOBJECT,
		FilePath: path,
		Metadata: metadata,
	}
	_, err = s.nosClient.PutObjectByFile(putObjectRequest)

	//get  files
	objectRequest := &model.GetObjectRequest{
		Bucket: TEST_BUCKET,
		Object: PUTOBJECTFILE,
	}
	objectResult, err := s.nosClient.GetObject(objectRequest)
	c.Assert(err, IsNil)
	c.Assert(objectResult.ObjectMetadata.ContentLength, Equals, int64(780831))
	objectResult.Body.Close()

	//get special object
	objectRequest = &model.GetObjectRequest{
		Bucket: TEST_BUCKET,
		Object: SPECIALOBJECT,
	}
	objectResult, err = s.nosClient.GetObject(objectRequest)
	c.Assert(err, IsNil)
	c.Assert(objectResult.ObjectMetadata.ContentLength, Equals, int64(780831))
	objectResult.Body.Close()

	//get  files with range
	objectRequest = &model.GetObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		ObjRange: "bytes=100-599",
	}
	objectResult, err = s.nosClient.GetObject(objectRequest)
	c.Assert(err, IsNil)
	c.Assert(objectResult.ObjectMetadata.ContentLength, Equals, int64(500))
	objectResult.Body.Close()

	//get  files with modify
	modify := time.Now().Unix()
	tm := time.Unix(modify, 0)
	objectRequest = &model.GetObjectRequest{
		Bucket:          TEST_BUCKET,
		Object:          PUTOBJECTFILE,
		IfModifiedSince: tm.Format(nosconst.RFC1123_GMT),
	}
	objectResult, err = s.nosClient.GetObject(objectRequest)
	c.Assert(err, IsNil)
	c.Assert(objectResult, IsNil)

	objectRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.GetObject(objectRequest)
	c.Assert(err, NotNil)

	objectRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.GetObject(objectRequest)
	c.Assert(err, NotNil)

	_, err = s.nosClient.GetObject(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestGetObjectMeta(c *C) {

	//upload file
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE:     contentType,
			nosconst.ORIG_CONTENT_MD5: OBJECTMD5,
			"x-nos-meta-huabin":       "jianren",
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	result, err := s.nosClient.PutObjectByFile(putObjectRequest)
	c.Assert(err, IsNil)
	c.Assert(result.Etag, Equals, OBJECTMD5)

	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: PUTOBJECTFILE,
	}

	metaData, err := s.nosClient.GetObjectMetaData(objectRequest)
	c.Assert(err, IsNil)
	c.Assert(metaData.Metadata["X-Nos-Meta-Huabin"], Equals, "jianren")

	objectRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.GetObjectMetaData(objectRequest)
	c.Assert(err, NotNil)

	objectRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.GetObjectMetaData(objectRequest)
	c.Assert(err, NotNil)

	_, err = s.nosClient.GetObjectMetaData(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestListObjects(c *C) {

	//upload file
	path := "../test/" + PUTOBJECTFILE
	contentType := "text/plain"

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.CONTENT_TYPE: contentType,
		},
	}

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   PUTOBJECTFILE,
		FilePath: path,
		Metadata: metadata,
	}

	_, err := s.nosClient.PutObjectByFile(putObjectRequest)

	putObjectRequest = &model.PutObjectRequest{
		Bucket:   TEST_BUCKET,
		Object:   SPECIALOBJECT,
		FilePath: path,
		Metadata: metadata,
	}
	_, err = s.nosClient.PutObjectByFile(putObjectRequest)

	listRequest := &model.ListObjectsRequest{
		Bucket: TEST_BUCKET,
	}
	listResult, err := s.nosClient.ListObjects(listRequest)
	c.Assert(err, IsNil)
	i := len(listResult.Contents)
	if i < 2 {
		c.Assert(true, IsNil)
	}

	listRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.ListObjects(listRequest)
	c.Assert(err, NotNil)

	listRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.ListObjects(listRequest)
	c.Assert(err, NotNil)

	_, err = s.nosClient.ListObjects(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestBigFileUpload(c *C) {

	var uploadId string
	path := "../test/" + "testserver"
	initRequest := &model.InitMultiUploadRequest{
		Bucket: TEST_BUCKET,
		Object: BIGFILEOBJECT,
	}

	//create uploadId
	initResult, err := s.nosClient.InitMultiUpload(initRequest)
	c.Assert(err, IsNil)
	c.Assert(initResult.Object, Equals, BIGFILEOBJECT)

	uploadId = initResult.UploadId

	initRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.InitMultiUpload(initRequest)
	c.Assert(err, NotNil)

	initRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.InitMultiUpload(initRequest)
	c.Assert(err, NotNil)

	//upload part
	file, err := os.Open(path)
	c.Assert(err, IsNil)
	stat, err := file.Stat()
	count := stat.Size()/1024000 + 1
	uploadPartRequest := &model.UploadPartRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
	}
	var partNum int
	etags := make([]model.UploadPart, count)
	for {
		partNum++
		buffer := make([]byte, 1024000)
		n, err := file.Read(buffer)
		if err != nil || n == 0 {
			break
		}

		uploadPartRequest.PartSize = int64(n)
		uploadPartRequest.PartNumber = partNum
		uploadPartRequest.Content = buffer
		uploadPart, err := s.nosClient.UploadPart(uploadPartRequest)
		c.Assert(err, IsNil)
		etagPart := model.UploadPart{
			PartNumber: partNum,
			Etag:       uploadPart.Etag,
		}
		etags[partNum-1] = etagPart
	}
	partNum--

	uploadPartRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.UploadPart(uploadPartRequest)
	c.Assert(err, NotNil)

	uploadPartRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.UploadPart(uploadPartRequest)
	c.Assert(err, NotNil)

	completeMultiUploadRequest := &model.CompleteMultiUploadRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
		Parts:    etags,
	}
	_, err = s.nosClient.CompleteMultiUpload(completeMultiUploadRequest)
	c.Assert(err, IsNil)

	completeMultiUploadRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.CompleteMultiUpload(completeMultiUploadRequest)
	c.Assert(err, NotNil)

	completeMultiUploadRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.CompleteMultiUpload(completeMultiUploadRequest)
	c.Assert(err, NotNil)

	objectRequest := &model.ObjectRequest{
		Bucket: TEST_BUCKET,
		Object: BIGFILEOBJECT,
	}
	data, err := s.nosClient.GetObjectMetaData(objectRequest)
	c.Assert(data.ContentLength, Equals, int64(6390720))

	_, err = s.nosClient.InitMultiUpload(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
	_, err = s.nosClient.UploadPart(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
	_, err = s.nosClient.CompleteMultiUpload(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestListPartsAndAbort(c *C) {

	var uploadId string
	path := "../test/" + "testserver"
	initRequest := &model.InitMultiUploadRequest{
		Bucket: TEST_BUCKET,
		Object: BIGFILEOBJECT,
	}

	//create uploadId
	initResult, err := s.nosClient.InitMultiUpload(initRequest)
	c.Assert(err, IsNil)
	c.Assert(initResult.Object, Equals, BIGFILEOBJECT)

	uploadId = initResult.UploadId

	//upload part
	file, err := os.Open(path)
	c.Assert(err, IsNil)
	stat, err := file.Stat()
	count := stat.Size()/1024000 + 1
	uploadPartRequest := &model.UploadPartRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
	}
	var partNum int
	etags := make([]model.UploadPart, count)
	for {
		partNum++
		buffer := make([]byte, 1024000)
		n, err := file.Read(buffer)
		if err != nil || n == 0 {
			break
		}

		uploadPartRequest.PartSize = int64(n)
		uploadPartRequest.PartNumber = partNum
		uploadPartRequest.Content = buffer
		uploadPart, err := s.nosClient.UploadPart(uploadPartRequest)
		c.Assert(err, IsNil)
		etagPart := model.UploadPart{
			PartNumber: partNum,
			Etag:       uploadPart.Etag,
		}
		etags[partNum-1] = etagPart
	}

	listUploadPartsRequest := &model.ListUploadPartsRequest{
		Bucket:           TEST_BUCKET,
		Object:           BIGFILEOBJECT,
		UploadId:         uploadId,
		MaxParts:         7,
		PartNumberMarker: 1,
	}
	listResult, err := s.nosClient.ListUploadParts(listUploadPartsRequest)
	c.Assert(err, IsNil)
	c.Assert(len(listResult.Parts), Equals, int(6))

	listUploadPartsRequest = &model.ListUploadPartsRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
		MaxParts: 7,
	}
	listResult1, err := s.nosClient.ListUploadParts(listUploadPartsRequest)
	c.Assert(err, IsNil)
	c.Assert(len(listResult1.Parts), Equals, int(7))

	listUploadPartsRequest = &model.ListUploadPartsRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
	}
	listResult2, err := s.nosClient.ListUploadParts(listUploadPartsRequest)
	c.Assert(err, IsNil)
	c.Assert(len(listResult2.Parts), Equals, int(7))

	listUploadPartsRequest = &model.ListUploadPartsRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId + "ab",
	}
	_, err = s.nosClient.ListUploadParts(listUploadPartsRequest)
	c.Assert(err, NotNil)

	abortMultiUploadRequest := &model.AbortMultiUploadRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
	}

	listUploadPartsRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.ListUploadParts(listUploadPartsRequest)
	c.Assert(err, NotNil)

	listUploadPartsRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.ListUploadParts(listUploadPartsRequest)
	c.Assert(err, NotNil)

	err = s.nosClient.AbortMultiUpload(abortMultiUploadRequest)
	c.Assert(err, IsNil)

	abortMultiUploadRequest = &model.AbortMultiUploadRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId + "ab",
	}
	err = s.nosClient.AbortMultiUpload(abortMultiUploadRequest)
	c.Assert(err, NotNil)

	abortMultiUploadRequest.Bucket = SPECIALBUCKET
	err = s.nosClient.AbortMultiUpload(abortMultiUploadRequest)
	c.Assert(err, NotNil)

	abortMultiUploadRequest.Bucket = BUCKETNOTEXIST
	err = s.nosClient.AbortMultiUpload(abortMultiUploadRequest)
	c.Assert(err, NotNil)

	err = s.nosClient.AbortMultiUpload(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
	_, err = s.nosClient.ListUploadParts(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}

func (s *NosClientTestSuite) TestListMultiUploads(c *C) {

	var uploadId string
	var uploadId2 string
	initRequest := &model.InitMultiUploadRequest{
		Bucket: TEST_BUCKET,
		Object: BIGFILEOBJECT,
	}

	//create uploadId
	initResult, err := s.nosClient.InitMultiUpload(initRequest)
	c.Assert(err, IsNil)
	c.Assert(initResult.Object, Equals, BIGFILEOBJECT)
	uploadId = initResult.UploadId

	initRequest.Object = SPECIALOBJECT
	initResult, err = s.nosClient.InitMultiUpload(initRequest)
	c.Assert(err, IsNil)
	c.Assert(initResult.Object, Equals, SPECIALOBJECT)
	uploadId2 = initResult.UploadId

	listMultiUploadsRequest := &model.ListMultiUploadsRequest{
		Bucket: TEST_BUCKET,
	}
	multiResult, err := s.nosClient.ListMultiUploads(listMultiUploadsRequest)
	c.Assert(err, IsNil)

	if len(multiResult.Uploads) < 2 {
		c.Assert(true, Equals, false)
	}

	listMultiUploadsRequest = &model.ListMultiUploadsRequest{
		Bucket:     TEST_BUCKET,
		MaxUploads: 2,
	}
	multiResult, err = s.nosClient.ListMultiUploads(listMultiUploadsRequest)
	c.Assert(err, IsNil)
	if len(multiResult.Uploads) != 2 {
		c.Assert(true, Equals, false)
	}

	abortMultiUploadRequest := &model.AbortMultiUploadRequest{
		Bucket:   TEST_BUCKET,
		Object:   BIGFILEOBJECT,
		UploadId: uploadId,
	}
	err = s.nosClient.AbortMultiUpload(abortMultiUploadRequest)
	c.Assert(err, IsNil)

	abortMultiUploadRequest = &model.AbortMultiUploadRequest{
		Bucket:   TEST_BUCKET,
		Object:   SPECIALOBJECT,
		UploadId: uploadId2,
	}
	err = s.nosClient.AbortMultiUpload(abortMultiUploadRequest)
	c.Assert(err, IsNil)

	listMultiUploadsRequest.Bucket = SPECIALBUCKET
	_, err = s.nosClient.ListMultiUploads(listMultiUploadsRequest)
	c.Assert(err, NotNil)

	listMultiUploadsRequest.Bucket = BUCKETNOTEXIST
	_, err = s.nosClient.ListMultiUploads(listMultiUploadsRequest)
	c.Assert(err, NotNil)

	_, err = s.nosClient.ListMultiUploads(nil)
	c.Assert(err.Error(), Equals, "StatusCode = 434, Resource = , Message = Request is nil")
}
