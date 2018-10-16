package main

import(

//"os"  
//"io"
//"bufio"
	"fmt"
	"strings"
	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/internal/apierr"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	"github.com/ks3sdklib/aws-sdk-go/service/s3"
	//"net/url"
)

var bucket =string("aa-go-sdk")


func main(){
	credentials := credentials.NewStaticCredentials("lMQTr0hNlMpB0iOk/i+x","D4CsYLs75JcWEjbiI22zR3P7kJ/+5B1qdEje7A7I","")
	svc := s3.New(&aws.Config{
		Region: "HANGZHOU",
		Credentials: credentials,
		Endpoint:"kss.ksyun.com",
		DisableSSL:true,
		LogLevel:1,
		S3ForcePathStyle:true,
		LogHTTPBody:true,
		})
	//listBuckets(svc)
 	//putBucket(svc)
	//headBucket(svc)
	//deleteBucket(svc)
	//getBucketAcl(svc)
	//listObjects(svc)
	//getBucketLogging(svc)
	//putBucketAcl(svc)
	//putBucketLogging(svc)
	//getBucketLocation(svc)
	//deleteObject(svc)
	
	//headObject(svc)
	//putObject(svc)
	//getObjectPresignedUrl(svc)
	//getObject(svc)
	//getObjectAcl(svc)
	//multipart(svc)
	deleteObjects(svc)
	
	//getObjectPresignedUrl(svc)
	//copyObject(svc)
}
func checkError(msg string,err error,code string){
	if err == nil{
		panic(msg+" expected error but error equals nil")
	}
	awserr:= err.(*apierr.RequestError)
	if awserr.Code() !=code{
		panic(msg+" expected "+code+",but "+awserr.Code())
	}
}
func listBuckets(c *s3.S3){
	out,err := c.ListBuckets(nil)
	if err !=nil{
		panic(err)
	}
	buckets := out.Buckets
	found := false
	for i:=0;i<len(buckets);i++{
		if *buckets[i].Name == bucket{
			found = true
		}
	}
	if !found {
		panic("list buckets expected found but not")
	}
}
func putBucket(c *s3.S3) {
	acl := "public-read"
	_,err := c.CreateBucket(&s3.CreateBucketInput{
		ACL:&acl,
		Bucket:&bucket,
		})
	checkError("put bucket",err,"BucketAlreadyExists")
}
func headBucket(c *s3.S3) {
	out,err := c.HeadBucket(
		&s3.HeadBucketInput{
			Bucket:&bucket,
		},
		)
	if err !=nil{
		panic(err)
	}
	fmt.Println(out)
}
func deleteBucket(c *s3.S3) {
	putObject(c)
	_,err := c.DeleteBucket(
		&s3.DeleteBucketInput{
			Bucket:&bucket,
		},
	)
	checkError("delete bucket",err,"BucketNotEmpty")
}
func getBucketAcl(c *s3.S3) {
	_,err := c.GetBucketACL(
		&s3.GetBucketACLInput{
			Bucket:&bucket,
		},
	)
	if err != nil{
		panic(err)
	}
}
func listObjects(c *s3.S3){
	delimiter := "/"
	out,err := c.ListObjects(
		&s3.ListObjectsInput{
			Bucket:&bucket,
			Delimiter:&delimiter,
		},
	)
	if err != nil{
		panic(err)
	}
	if *out.Delimiter != "/"{
		panic("list objects delimiter expected / but not")
	}
}
func  getBucketLogging(c *s3.S3) {
	out,err := c.GetBucketLogging(
		&s3.GetBucketLoggingInput{
			Bucket:&bucket,
		},
		)
	if err != nil{
		panic(err)
	}
	fmt.Println(*out.LoggingEnabled.TargetBucket)
	fmt.Println(*out.LoggingEnabled.TargetPrefix)
}
func putBucketAcl(c *s3.S3){
	acl := "public-read"
	out,err := c.PutBucketACL(&s3.PutBucketACLInput{
		ACL:&acl,
		Bucket:&bucket,
		})
	if err !=nil{
		panic(err)
	}
	fmt.Println(out)
}
func putBucketLogging(c *s3.S3){
	prefix := "x-kss-"
	out,err := c.PutBucketLogging(&s3.PutBucketLoggingInput{
		Bucket:&bucket,
		BucketLoggingStatus:&s3.BucketLoggingStatus{
			LoggingEnabled:&s3.LoggingEnabled{
				TargetBucket:&bucket,
				TargetPrefix:&prefix,
			},
		},
		})
	if err !=nil{
		panic(err)
	}
	fmt.Println(out)
}
func getBucketLocation(c *s3.S3){
	out,err := c.GetBucketLocation(
		&s3.GetBucketLocationInput{
			Bucket:&bucket,
		},
	)
	if err != nil{
		panic(err)
	}
	if *out.LocationConstraint!= "HANGZHOU"{
		panic("location expected HANGZHOU but not")
	}
}
func deleteObject(c *s3.S3) {
	putObject(c)
	key := "aws/config.go"
	_,err := c.DeleteObject(
		&s3.DeleteObjectInput{
			Bucket:&bucket,
			Key:&key,
		},
	)
	if err != nil{
		panic(err)
	}
}
func getObject(c *s3.S3){
	putObject(c)
	key := "aws/config.go"
	out,err := c.GetObject(
		&s3.GetObjectInput{
			Bucket:&bucket,
			Key:&key,
		},
	)
	if err!=nil{
		panic(err)
	}
	fmt.Println(out.Metadata)
	fmt.Println(*out.ContentLength)
	fmt.Println(*out.ContentType)

	b := make([]byte, 20)
	n, err := out.Body.Read(b)
	fmt.Printf("%-20s %-2v %v\n", b[:n], n, err)
}
func getObjectPresignedUrl(c *s3.S3){
	key := "aws/config.go"
	content := "text/html"
	url,_ := c.GetObjectPresignedUrl(
		&s3.GetObjectInput{
			Bucket:&bucket,
			Key:&key,
			ResponseContentType:&content,
		},
		1444370289000000000,
	)
	fmt.Println(url)
}
func headObject(c *s3.S3) {
	putObject(c)
	key := "aws/config.go"
	out,err := c.HeadObject(
		&s3.HeadObjectInput{
			Bucket:&bucket,
			Key:&key,
		},
	)
	if err!=nil{
		panic(err)
	}
	fmt.Println(out.Metadata)
	fmt.Println(*out.ContentLength)
	fmt.Println(*out.ContentType)
}
func putObject(c *s3.S3) {
	key := "aws/config.go"
	contenttype := "application/ocet-stream"
	out,err := c.PutObject(
		&s3.PutObjectInput{
			Bucket:&bucket,
			Key:&key,
			Body:strings.NewReader("content"),
			ContentType:&contenttype,
		},
	)
	if err != nil{
		panic(err)
	}
	fmt.Println(out)
}
func getObjectAcl(c *s3.S3) {
	putObject(c)
	key := "aws/config.go"
	out,err := c.GetObjectACL(
		&s3.GetObjectACLInput{
			Bucket:&bucket,
			Key:&key,
		},
	)
	if err != nil{
		panic(err)
	}
	grants :=  out.Grants
	for i:=0;i<len(grants);i++{
		grant := grants[i]
		grantee := grant.Grantee
		if grantee.DisplayName != nil{
			fmt.Println(*grantee.DisplayName)
		}
		if grantee.ID != nil{
			fmt.Println(*grantee.ID)
		}
		if grantee.Type != nil{
			fmt.Println(*grantee.Type)
		}
		if grantee.URI != nil{
			fmt.Println(*grantee.URI)
		}
		if grant.Permission != nil{
			fmt.Println(*grant.Permission)
		}
		fmt.Println("---------")
	}
}
func multipart(c *s3.S3){
	key := "中文"
	acl := "public-read"
	out,_ := c.CreateMultipartUpload(
		&s3.CreateMultipartUploadInput{
			Bucket:&bucket,
			Key:&key,
			ACL:&acl,
		},
		)
	uploadid := out.UploadID

	var partnum int64
	partnum = 1;
	s := strings.NewReader("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")
	uploadRest,_ := c.UploadPart(
		&s3.UploadPartInput{
			Bucket:&bucket,
			Key:&key,
			UploadID:uploadid,
			PartNumber:&partnum,
			Body:s,
		},
		)
	fmt.Println(uploadRest)
	listRet,_ := c.ListParts(
		&s3.ListPartsInput{
			Bucket:&bucket,
			Key:&key,
			UploadID:uploadid,
		},
		)
	fmt.Println("123")
	listP :=  listRet.Parts
	for i:=0;i<len(listP);i++{
		alistP := listP[i];
		fmt.Println(*alistP.PartNumber)
		fmt.Println(*alistP.ETag)
	}
	fmt.Println(*listRet.Key)
	fmt.Println("123")
	var parts [] *s3.CompletedPart;
	apart := &s3.CompletedPart{
		ETag:listRet.Parts[0].ETag,
		PartNumber:listRet.Parts[0].PartNumber,
	};
	parts = append(parts,apart)

	comRet,_ := c.CompleteMultipartUpload(
		&s3.CompleteMultipartUploadInput{
			Bucket:&bucket,
			Key:&key,
			UploadID:uploadid,
			MultipartUpload:&s3.CompletedMultipartUpload{
				Parts:parts,
			},
		},
		)
	fmt.Println(comRet)
}
func deleteObjects(c *s3.S3){
	key := "test"

	var objects [] *s3.ObjectIdentifier
	objects = append(objects,&s3.ObjectIdentifier{Key:&key,})


	out,_ := c.DeleteObjects(
		&s3.DeleteObjectsInput{
			Bucket:&bucket,
			Delete:&s3.Delete{
				Objects:objects,
			},
		},
		)
	fmt.Println(out)
}
func copyObject(c *s3.S3){
	bucket := "aa-go-sdk"
	source := "aa-go-sdk/aws/config.go"
	key := "test"

	out,_ := c.CopyObject(
		&s3.CopyObjectInput{
			Bucket:&bucket,
			Key:&key,
			CopySource:&source,
		},
		)
	fmt.Println(out)
}
