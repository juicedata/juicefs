package ks3test

import (
	"testing"
	"strings"
	"bufio"
//	"io"
	"fmt"
	"net/http"
	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/internal/apierr"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	"github.com/ks3sdklib/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/assert"
)
var bucket =string("aa-go-sdk")
var key = string("中文/test.go")
var key_encode = string("%E4%B8%AD%E6%96%87/test.go")
var key_copy = string("中文/test.go.copy")
var content = string("content")
var cre = credentials.NewStaticCredentials("lMQTr0hNlMpB0iOk/i+x","D4CsYLs75JcWEjbiI22zR3P7kJ/+5B1qdEje7A7I","")
var svc = s3.New(&aws.Config{
		Region: "HANGZHOU",
		Credentials: cre,
		Endpoint:"kssws.ks-cdn.com",
		DisableSSL:true,
		LogLevel:1,
		S3ForcePathStyle:false,
		LogHTTPBody:true,
		})

func TestCreateBucket(t *testing.T){
	_,err := svc.CreateBucket(&s3.CreateBucketInput{
		ACL:aws.String("public-read"),
		Bucket:aws.String(bucket),
		})
	assert.Error(t,err)
	assert.Equal(t,"BucketAlreadyExists",err.(*apierr.RequestError).Code())	
}
func TestBucketAcl(t *testing.T){
	_,err := svc.PutBucketACL(&s3.PutBucketACLInput{
		Bucket:aws.String(bucket),
		ACL:aws.String("public-read"),
		})
	assert.NoError(t,err)

	acp,err := svc.GetBucketACL(&s3.GetBucketACLInput{
		Bucket:aws.String(bucket),
		})
	assert.NoError(t,err)
	grants := acp.Grants
	assert.Equal(t,2,len(grants),"size of grants")

	foundFull := false
	foundRead := false
	for i:=0;i <len(grants);i++{
		grant := grants[i]
		if *grant.Permission == "FULL_CONTROL"{
			foundFull = true
			assert.NotNil(t,*grant.Grantee.ID,"grantee userid should not null")
			assert.NotNil(t,*grant.Grantee.DisplayName,"grantee displayname should not null")
		}else if *grant.Permission == "READ"{
			foundRead = true
			assert.NotNil(t,*grant.Grantee.URI,"grantee uri should not null")
		}
	}
	assert.True(t,foundRead,"acp should contains READ")
	assert.True(t,foundFull,"acp should contains FULL_CONTROL")

	_,putaclErr := svc.PutBucketACL(&s3.PutBucketACLInput{
		Bucket:aws.String(bucket),
		ACL:aws.String("private"),
		})
	assert.NoError(t,putaclErr)

	acp,getaclErr := svc.GetBucketACL(&s3.GetBucketACLInput{
		Bucket:aws.String(bucket),
		})
	assert.NoError(t,getaclErr)
	privategrants := acp.Grants
	assert.Equal(t,1,len(privategrants),"size of grants")
}
func TestListBuckets(t *testing.T) {
	out,err := svc.ListBuckets(nil)
	assert.NoError(t,err)
	buckets := out.Buckets
	found := false
	for i:=0;i<len(buckets);i++{
		if *buckets[i].Name == bucket{
			found = true
		}
	}
	assert.True(t,found,"list buckets expected contains "+bucket)
}
func TestHeadBucket(t *testing.T) {
	_,err := svc.HeadBucket(&s3.HeadBucketInput{
		Bucket:aws.String(bucket),
	})
	assert.NoError(t,err)
}
func TestDeleteBucket(t *testing.T) {
	putObjectSimple()
	_,err := svc.DeleteBucket(&s3.DeleteBucketInput{
		Bucket:aws.String(bucket),
	})
	assert.Error(t,err)
	assert.Equal(t,"BucketNotEmpty",err.(*apierr.RequestError).Code())	
}
func TestListObjects(t *testing.T) {
	putObjectSimple()
	objects,err := svc.ListObjects(&s3.ListObjectsInput{
		Bucket:aws.String(bucket),
		Delimiter:aws.String("/"),
		MaxKeys:aws.Long(999),
		Prefix:aws.String(""),
	})
	assert.NoError(t,err)
	assert.Equal(t,"/",*objects.Delimiter)
	assert.Equal(t,*aws.Long(999),*objects.MaxKeys)
	assert.Equal(t,"",*objects.Prefix)
	assert.False(t,*objects.IsTruncated)

	objects1,err := svc.ListObjects(&s3.ListObjectsInput{
		Bucket:aws.String(bucket),
		})
	assert.NoError(t,err)
	objectList := objects1.Contents
	found := false
	for i:=0;i <len(objectList);i++{
		object := objectList[i]
		assert.Equal(t,"STANDARD",*object.StorageClass)
		if *object.Key == key{
			found = true
		}
	}
	assert.True(t,found,"expected found "+key+"in object listing")
}
func TestDelObject(t *testing.T) {
	putObjectSimple();
	assert.True(t,objectExists(bucket,key))
	_,err := svc.DeleteObject(&s3.DeleteObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
	})
	assert.NoError(t,err)
	assert.False(t,objectExists(bucket,key))
}
func TestDelMulti(t *testing.T) {
	putObjectSimple();
	assert.True(t,objectExists(bucket,key))

	var objects [] *s3.ObjectIdentifier
	objects = append(objects,&s3.ObjectIdentifier{Key:&key,})
	_,err := svc.DeleteObjects(&s3.DeleteObjectsInput{
		Bucket:aws.String(bucket),
		Delete:&s3.Delete{
			Objects:objects,
		},
		ContentType:aws.String("application/xml"),
	})
	assert.NoError(t,err)
	assert.False(t,objectExists(bucket,key))
}
func TestGetObject(t *testing.T){
	putObjectSimple();
	out,err := svc.GetObject(&s3.GetObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		ResponseContentType:aws.String("application/pdf"),
		Range:aws.String("bytes=0-1"),
	})
	assert.NoError(t,err)
	assert.True(t,strings.HasPrefix(*out.ContentRange,"bytes 0-1/"))
	assert.Equal(t,*aws.Long(2),*out.ContentLength)
	assert.Equal(t,"application/pdf",*out.ContentType)
	br := bufio.NewReader(out.Body)
	w, _ := br.ReadString('\n')
	assert.Equal(t,content[:2],w)
}
func TestObjectAcl(t *testing.T){
	putObjectSimple();
	_,err := svc.PutObjectACL(&s3.PutObjectACLInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		ACL:aws.String("public-read"),
		})
	assert.NoError(t,err)

	acp,err := svc.GetObjectACL(&s3.GetObjectACLInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		})
	assert.NoError(t,err)
	grants := acp.Grants
	assert.Equal(t,2,len(grants),"size of grants")

	foundFull := false
	foundRead := false
	for i:=0;i <len(grants);i++{
		grant := grants[i]
		if *grant.Permission == "FULL_CONTROL"{
			foundFull = true
			assert.NotNil(t,*grant.Grantee.ID,"grantee userid should not null")
			assert.NotNil(t,*grant.Grantee.DisplayName,"grantee displayname should not null")
		}else if *grant.Permission == "READ"{
			foundRead = true
			assert.NotNil(t,*grant.Grantee.URI,"grantee uri should not null")
		}
	}
	assert.True(t,foundRead,"acp should contains READ")
	assert.True(t,foundFull,"acp should contains FULL_CONTROL")

	_,putaclErr := svc.PutObjectACL(&s3.PutObjectACLInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		ACL:aws.String("private"),
		})
	assert.NoError(t,putaclErr)

	acp,getaclErr := svc.GetObjectACL(&s3.GetObjectACLInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		})
	assert.NoError(t,getaclErr)
	privategrants := acp.Grants
	assert.Equal(t,1,len(privategrants),"size of grants")
}
func TestCopyObject(t *testing.T) {
	putObjectSimple();
	svc.DeleteObject(&s3.DeleteObjectInput{
			Bucket:aws.String(bucket),
			Key:aws.String(key_copy),
		})
	_,err := svc.CopyObject(&s3.CopyObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key_copy),
		CopySource:aws.String(bucket+"/"+key_encode),
	})
	assert.NoError(t,err)
	assert.True(t,objectExists(bucket,key))

	meta,err := svc.HeadObject(
		&s3.HeadObjectInput{
			Bucket:&bucket,
			Key:&key_copy,
		},
	)
	assert.NoError(t,err)
	assert.Equal(t,int64(len(content)),*meta.ContentLength)
}
func TestMultipartUpload(t *testing.T) {
	initRet,initErr := svc.CreateMultipartUpload(&s3.CreateMultipartUploadInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		ACL:aws.String("public-read"),
		ContentType:aws.String("image/jpeg"),
	})
	assert.NoError(t,initErr)
	assert.Equal(t,bucket,*initRet.Bucket)
	assert.Equal(t,key,*initRet.Key)
	assert.NotNil(t,*initRet.UploadID)

	uploadId := *initRet.UploadID


	upRet,upErr := svc.UploadPart(&s3.UploadPartInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		PartNumber:aws.Long(1),
		UploadID:aws.String(uploadId),
		Body:strings.NewReader(content),
		ContentLength:aws.Long(int64(len(content))),
	})
	assert.NoError(t,upErr)
	assert.NotNil(t,upRet.ETag)


	listRet,listErr := svc.ListParts(&s3.ListPartsInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		UploadID:aws.String(uploadId),
	})
	assert.NoError(t,listErr)
	assert.Equal(t,bucket,*listRet.Bucket)
	assert.Equal(t,key,*listRet.Key)
	assert.False(t,*listRet.IsTruncated)
	assert.Equal(t,uploadId,*listRet.UploadID)
	parts := listRet.Parts
	assert.Equal(t,1,len(parts))
	part := parts[0]

	compParts := []*s3.CompletedPart{&s3.CompletedPart{PartNumber:part.PartNumber,ETag:part.ETag,}}
	compRet,compErr := svc.CompleteMultipartUpload(&s3.CompleteMultipartUploadInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		UploadID:aws.String(uploadId),
		MultipartUpload:&s3.CompletedMultipartUpload{
			Parts:compParts,
		},
	})
	assert.NoError(t,compErr)
	assert.Equal(t,bucket,*compRet.Bucket)
	assert.Equal(t,key,*compRet.Key)
}
func TestPutObjectWithUserMeta(t *testing.T) {
	meta := make(map[string]*string)
	meta["user"] = aws.String("lijunwei")
	meta["edit-date"] = aws.String("20150623")
	_,putErr := svc.PutObject(&s3.PutObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		Metadata:meta,
	})
	assert.NoError(t,putErr)

	headRet,headErr := svc.HeadObject(&s3.HeadObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
	})
	assert.NoError(t,headErr)

	outMeta := headRet.Metadata
	user := outMeta["User"]
	date := outMeta["Edit-Date"]
	assert.NotNil(t,user)
	assert.NotNil(t,date)
	if user != nil{
		assert.Equal(t,"lijunwei",*user)
	}
	if date != nil{
		assert.Equal(t,"20150623",*date)
	}
}
func TestPutObjectWithSSEC(t *testing.T) {
	putRet,putErr := svc.PutObject(&s3.PutObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		SSECustomerAlgorithm:aws.String("AES256"),
		SSECustomerKey:aws.String("12345678901234567890123456789012"),
	})
	assert.NoError(t,putErr)
	assert.NotNil(t,putRet.SSECustomerAlgorithm)
	if putRet.SSECustomerAlgorithm!=nil{
		assert.Equal(t,"AES256",*putRet.SSECustomerAlgorithm)
	}
	assert.NotNil(t,putRet.SSECustomerKeyMD5)
	if putRet.SSECustomerKeyMD5 != nil{
		assert.NotNil(t,*putRet.SSECustomerKeyMD5)
	}

	headRet,headErr := svc.HeadObject(&s3.HeadObjectInput{
		Bucket:aws.String(bucket),
		Key:aws.String(key),
		SSECustomerAlgorithm:aws.String("AES256"),
		SSECustomerKey:aws.String("12345678901234567890123456789012"),
	})
	assert.NoError(t,headErr)
	assert.NotNil(t,headRet.SSECustomerAlgorithm)
	if headRet.SSECustomerAlgorithm != nil{
		assert.Equal(t,"AES256",*headRet.SSECustomerAlgorithm)
	}
	assert.NotNil(t,headRet.SSECustomerKeyMD5)
	if headRet.SSECustomerKeyMD5!=nil{
		assert.NotNil(t,*headRet.SSECustomerKeyMD5)
	}
}
func TestPutObjectAclPresignedUrl(t *testing.T){
	params := &s3.PutObjectACLInput{
		Bucket:             aws.String(bucket), // bucket名称
		Key:                aws.String(key),  // object key
		ACL:				aws.String("private"),//设置ACL
		ContentType:		aws.String("text/plain"),
	}
	resp, err := svc.PutObjectACLPresignedUrl(params,1444370289000000000)//第二个参数为外链过期时间，第二个参数为time.Duration类型
	if err!=nil {
		panic(err)
	}

	httpReq, _ := http.NewRequest("PUT", "", nil)
	httpReq.URL = resp
	httpReq.Header["x-amz-acl"] = []string{"private"}
	httpReq.Header.Add("Content-Type","text/plain")
 	httpRep,err := http.DefaultClient.Do(httpReq)
	if err != nil{
		panic(err)
	}
	assert.Equal(t,"200 OK",httpRep.Status)
}
func TestPutObjectPresignedUrl(t *testing.T){
	params := &s3.PutObjectInput{
		Bucket:             aws.String(bucket), // bucket名称
		Key:                aws.String(key),  // object key
		ACL:				aws.String("public-read"),//设置ACL
		ContentType:        aws.String("application/ocet-stream"),//设置文件的content-type
		ContentMaxLength:	aws.Long(20),//设置允许的最大长度，对应的header：x-amz-content-maxlength
	}
	resp, err := svc.PutObjectPresignedUrl(params,1444370289000000000)//第二个参数为外链过期时间，第二个参数为time.Duration类型
	if err!=nil {
		panic(err)
	}
	
	httpReq, _ := http.NewRequest("PUT", "", strings.NewReader("123"))
	httpReq.URL = resp
	httpReq.Header["x-amz-acl"] = []string{"public-read"}
	httpReq.Header["x-amz-content-maxlength"] = []string{"20"}
	httpReq.Header.Add("Content-Type","application/ocet-stream")
	fmt.Println(httpReq)
 	httpRep,err := http.DefaultClient.Do(httpReq)
 	fmt.Println(httpRep)
	if err != nil{
		panic(err)
	}
	assert.Equal(t,"200 OK",httpRep.Status)
}
func putObjectSimple() {
/*	svc.DeleteObject(
		&s3.DeleteObjectInput{
			Bucket:aws.String(bucket),
			Key:aws.String(key),
		},
	)*/
	svc.PutObject(
		&s3.PutObjectInput{
			Bucket:aws.String(bucket),
			Key:aws.String(key),
			Body:strings.NewReader(content),
		},
	)
}
func objectExists(bucket,key string) (bool){
	_,err := svc.HeadObject(
		&s3.HeadObjectInput{
			Bucket:&bucket,
			Key:&key,
		},
	)
	if err!=nil{
		if err.(*apierr.RequestError).StatusCode() == 404{
			return false
		}else{
			panic(err)
		}
	}
	return true
}