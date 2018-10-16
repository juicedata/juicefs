package main

import (
	"fmt"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/config"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/logger"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/model"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/nosclient"
)

func sample_init(endpoint, accessKey, secretKey string) *nosclient.NosClient {
	conf := &config.Config{
		Endpoint:  endpoint,
		AccessKey: accessKey,
		SecretKey: secretKey,

		NosServiceConnectTimeout:    3,
		NosServiceReadWriteTimeout:  60,
		NosServiceMaxIdleConnection: 100,

		LogLevel: logger.LogLevel(logger.DEBUG),
		Logger:   logger.NewDefaultLogger(),
	}

	nosClient, _ := nosclient.New(conf)
	return nosClient
}

func main() {
	path := "<File Path>"
	endpoint := "<endpoint>"
	accessKey := "<AccessKeyId>"
	secretKey := "<SecretKey>"

	nosClient := sample_init(endpoint, accessKey, secretKey)

	putObjectRequest := &model.PutObjectRequest{
		Bucket:   "<my-bucket>",
		Object:   "<my-object>",
		FilePath: path,
	}
	_, err := nosClient.PutObjectByFile(putObjectRequest)
	if err != nil {
		fmt.Println(err.Error())
	}

	getObjectRequest := &model.GetObjectRequest{
		Bucket: "<my-bucket>",
		Object: "<my-object>",
	}
	objectResult, err := nosClient.GetObject(getObjectRequest)
	if err != nil {
		fmt.Println(err.Error())
	} else {
		objectResult.Body.Close()
	}

	objectRequest := &model.ObjectRequest{
		Bucket: "<my-bucket>",
		Object: "<my-object>",
	}
	err = nosClient.DeleteObject(objectRequest)
	if err != nil {
		fmt.Println(err.Error())
	}

	fmt.Println("Simple samples completed")
}
