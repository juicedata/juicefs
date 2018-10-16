# AWS S3 SDK For Go使用指南 
---

[TOC]

## 1 概述
此SDK是基于aws-sdk-go改造，适用于golang开发环境。

## 2 环境准备
配置Go 开发环境  

## 3 初始化
### 3.1 下载安装SDK
go get  github.com/ks3sdklib/aws-sdk-go
### 3.2 获取秘钥
1、开通KS3服务，[http://www.ksyun.com/user/register](http://www.ksyun.com/user/register) 注册账号  
2、进入控制台, [http://ks3.ksyun.com/console.html#/setting](http://ks3.ksyun.com/console.html#/setting) 获取AccessKeyID 、AccessKeySecret
### 3.3 初始化
1、引用相关包

	import(
		"github.com/ks3sdklib/aws-sdk-go/aws"
		"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
		"github.com/ks3sdklib/aws-sdk-go/service/s3"
	)

2、初始化客户端

	credentials := credentials.NewStaticCredentials("<AccessKeyID>","<AccessKeySecret>","")
	client := s3.New(&aws.Config{
		Region: "HANGZHOU",
		Credentials: credentials,
		Endpoint:"kssws.ks-cdn.com",//s3地址
		DisableSSL:true,//是否禁用https
		LogLevel:1,//是否开启日志,0为关闭日志，1为开启日志
		S3ForcePathStyle:false,//是否强制使用path style方式访问
		LogHTTPBody:true,//是否把HTTP请求body打入日志
		Logger:os.Stdout,//打日志的位置
		})
## 4 使用示例
输入参数params和返回结果resp详细结构请参考github.com/ks3sdklib/aws-sdk-go/service/s3/api.go  
### 4.1 上传文件

	params := &s3.PutObjectInput{
		Bucket:             aws.String("BucketName"), // bucket名称
		Key:                aws.String("ObjectKey"),  // object key
		ACL:                aws.String("ObjectCannedACL"),//权限，支持private(私有)，public-read(公开读)
		Body:               bytes.NewReader([]byte("PAYLOAD")),//要上传的内容
		ContentType:        aws.String("application/ocet-stream"),//设置content-type
		Metadata: map[string]*string{
			//"Key": aws.String("MetadataValue"), // 设置用户元数据
			// More values...
		},
	}
	resp, err := client.PutObject(params)
	if err!= nil{
		panic(err)
	}
	fmt.Println(resp)
	//获取新的文件名
	fmt.Println(*resp.NewFileName)

### 4.2 下载文件

	params := &s3.GetObjectInput{
		Bucket:             aws.String("BucketName"), // bucket名称
		Key:                aws.String("ObjectKey"),  // object key
	}
	resp, err := client.GetObject(params)
	if err != nil{
		panic(err)
	}
	//读取返回结果中body的前20个字节
	b := make([]byte, 20)
	n, err := resp.Body.Read(b)
	fmt.Printf("%-20s %-2v %v\n", b[:n], n, err)

###  4.3 生成文件下载外链

	params := &s3.GetObjectInput{
		Bucket:             aws.String("BucketName"), // bucket名称
		Key:                aws.String("ObjectKey"),  // object key
		ResponseCacheControl:       aws.String("ResponseCacheControl"),//控制返回的Cache-Control header
		ResponseContentDisposition: aws.String("ResponseContentDisposition"),//控制返回的Content-Disposition header
		ResponseContentEncoding:    aws.String("ResponseContentEncoding"),//控制返回的Content-Encoding header
		ResponseContentLanguage:    aws.String("ResponseContentLanguage"),//控制返回的Content-Language header
		ResponseContentType:        aws.String("ResponseContentType"),//控制返回的Content-Type header
	}
	resp, err := client.GetObjectPresignedUrl(params,1444370289000000000)//第二个参数为外链过期时间，第二个参数为time.Duration类型
	if err!=nil {
		panic(err)
	}
	fmt.Println(resp)//resp即生成的外链地址,类型为url.URL
### 4.4 生成文件上传外链

	params := &s3.PutObjectInput{
		Bucket:             aws.String(bucket), // bucket名称
		Key:                aws.String(key),  // object key
		ACL:				aws.String("public-read"),//设置ACL
		ContentType:        aws.String("application/ocet-stream"),//设置文件的content-type
		ContentMaxLength:	aws.Long(20),//设置允许的最大长度，对应的header：x-amz-content-maxlength
	}
	resp, err := client.PutObjectPresignedUrl(params,1444370289000000000)//第二个参数为外链过期时间，第二个参数为time.Duration类型
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

### 4.5 生成设置文件ACL的外链

	params := &s3.PutObjectACLInput{
		Bucket:             aws.String(bucket), // bucket名称
		Key:                aws.String(key),  // object key
		ACL:				aws.String("private"),//设置ACL
		ContentType:		aws.String("text/plain"),
	}
	resp, err := client.PutObjectACLPresignedUrl(params,1444370289000000000)//第二个参数为外链过期时间，第二个参数为time.Duration类型
	if err!=nil {
		panic(err)
	}
	fmt.Println(resp)//resp即生成的外链地址,类型为url.URL

	httpReq, _ := http.NewRequest("PUT", "", nil)
	httpReq.URL = resp
	httpReq.Header["x-amz-acl"] = []string{"private"}
	httpReq.Header.Add("Content-Type","text/plain")
	fmt.Println(httpReq)
 	httpRep,err := http.DefaultClient.Do(httpReq)
	if err != nil{
		panic(err)
	}
	fmt.Println(httpRep)
### 4.7 初始化分块上传

	params := &s3.CreateMultipartUploadInput{
		Bucket:             aws.String("BucketName"), // bucket名称
		Key:                aws.String("ObjectKey"),  // object key
		ACL:                aws.String("ObjectCannedACL"),//权限，支持private(私有)，public-read(公开读)
		ContentType:        aws.String("application/ocet-stream"),//设置content-type
		Metadata: map[string]*string{
			//"Key": aws.String("MetadataValue"), // 设置用户元数据
			// More values...
		},
	}
	resp, err := client.CreateMultipartUpload(params)
	if err != nil{
		panic(err)
	}
	//获取这次初始化的uploadid
	fmt.Println(*resp.UploadID)

### 4.8 分块上传-上传块

	params := &s3.UploadPartInput{
		Bucket:aws.String(bucket),//bucket名称
		Key:aws.String(key),//文件名
		PartNumber:aws.Long(1),//当前块的序号
		UploadID:aws.String(uploadId),//由初始化获取到得uploadid
		Body:strings.NewReader(content),//当前块的内容
		ContentLength:aws.Long(int64(len(content))),//内容长度
	}
	resp,err := client.UploadPart(params)
	if err != nil{
		panic(err)
	}
	fmt.Println(resp)

### 4.9 完成分块上传并合并块

	params := &s3.CompleteMultipartUploadInput{
		Bucket:aws.String(bucket),//bucket名称
		Key:aws.String(key),//文件名
		UploadID:aws.String(uploadId),//由初始化获取到得uploadid
		MultipartUpload:&s3.CompletedMultipartUpload{
			Parts:<已经完成的块列表>,//类型为*s3.CompletedPart数组
		},
	}
	resp,err := client.CompleteMultipartUpload(params)
	if err != nil{
		panic(err)
	}
	fmt.Println(resp)

### 4.10 取消分块上传

	params := &s3.AbortMultipartUploadInput{
		Bucket:aws.String(bucket),//bucket名称
		Key:aws.String(key),//文件名
		UploadID:aws.String(uploadId),//由初始化获取到得uploadid
	}
	resp,err := client.AbortMultipartUpload(params)
	if err != nil{
		panic(err)
	}
	fmt.Println(resp)

### 4.11 罗列分块上传已经上传的块

	params := &s3.ListPartsInput{
		Bucket:aws.String(bucket),//bucket名称
		Key:aws.String(key),//文件名
		UploadID:aws.String(uploadId),//由初始化获取到得uploadid
	}
	resp,err := client.ListParts(params)
	if err != nil{
		panic(err)
	}
	fmt.Println(resp)

### 4.12 计算token（移动端相关）

	package main
	import(
		"crypto/hmac"
		"crypto/sha1"
		"encoding/base64"
		"fmt"
	)
	func main(){
		AccessKeyId := "AccessKeyId"
		AccessKeySecret:= "AccessKeySecret"
		stringToSign := "stringToSign"

		signature := string(base64Encode(makeHmac([]byte(AccessKeySecret), []byte(stringToSign))))
	token := "KSS "+AccessKeyId+":"+signature
	fmt.Println(token)
	}
	func makeHmac(key []byte, data []byte) []byte {
		hash := hmac.New(sha1.New, key)
		hash.Write(data)
		return hash.Sum(nil)
	}	
	func base64Encode(src []byte) []byte {  
	    return []byte(base64.StdEncoding.EncodeToString(src))  
	} 