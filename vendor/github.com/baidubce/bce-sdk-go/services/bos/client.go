/*
 * Copyright 2017 Baidu, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
 * except in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software distributed under the
 * License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions
 * and limitations under the License.
 */

// client.go - define the client for BOS service

// Package bos defines the BOS services of BCE. The supported APIs are all defined in sub-package
// model with three types: 16 bucket APIs, 9 object APIs and 7 multipart APIs.
package bos

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/baidubce/bce-sdk-go/auth"
	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
	"github.com/baidubce/bce-sdk-go/util/log"
)

const (
	DEFAULT_SERVICE_DOMAIN = bce.DEFAULT_REGION + ".bcebos.com"
	DEFAULT_MAX_PARALLEL   = 10
	MULTIPART_ALIGN        = 1 << 20        // 1MB
	MIN_MULTIPART_SIZE     = 5 * (1 << 20)  // 5MB
	DEFAULT_MULTIPART_SIZE = 10 * (1 << 20) // 10MB

	MAX_PART_NUMBER        = 10000
	MAX_SINGLE_PART_SIZE   = 5 * (1 << 30) // 5GB
	MAX_SINGLE_OBJECT_SIZE = 5 * (1 << 40) // 5TB
)

// Client of BOS service is a kind of BceClient, so derived from BceClient
type Client struct {
	*bce.BceClient

	// Fileds that used in parallel operation for BOS service
	MaxParallel   int64
	MultipartSize int64
}

// NewClient make the BOS service client with default configuration.
// Use `cli.Config.xxx` to access the config or change it to non-default value.
func NewClient(ak, sk, endpoint string) (*Client, error) {
	var credentials *auth.BceCredentials
	var err error
	if len(ak) == 0 && len(sk) == 0 { // to support public-read-write request
		credentials, err = nil, nil
	} else {
		credentials, err = auth.NewBceCredentials(ak, sk)
		if err != nil {
			return nil, err
		}
	}
	if len(endpoint) == 0 {
		endpoint = DEFAULT_SERVICE_DOMAIN
	}
	defaultSignOptions := &auth.SignOptions{
		HeadersToSign: auth.DEFAULT_HEADERS_TO_SIGN,
		ExpireSeconds: auth.DEFAULT_EXPIRE_SECONDS}
	defaultConf := &bce.BceClientConfiguration{
		Endpoint:    endpoint,
		Region:      bce.DEFAULT_REGION,
		UserAgent:   bce.DEFAULT_USER_AGENT,
		Credentials: credentials,
		SignOption:  defaultSignOptions,
		Retry:       bce.DEFAULT_RETRY_POLICY,
		ConnectionTimeoutInMillis: bce.DEFAULT_CONNECTION_TIMEOUT_IN_MILLIS}
	v1Signer := &auth.BceV1Signer{}

	client := &Client{bce.NewBceClient(defaultConf, v1Signer),
		DEFAULT_MAX_PARALLEL, DEFAULT_MULTIPART_SIZE}
	return client, nil
}

// ListBuckets - list all buckets
//
// RETURNS:
//     - *api.ListBucketsResult: the all buckets
//     - error: the return error if any occurs
func (c *Client) ListBuckets() (*api.ListBucketsResult, error) {
	return api.ListBuckets(c)
}

// ListObjects - list all objects of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
//     - args: the optional arguments to list objects
// RETURNS:
//     - *api.ListObjectsResult: the all objects of the bucket
//     - error: the return error if any occurs
func (c *Client) ListObjects(bucket string,
	args *api.ListObjectsArgs) (*api.ListObjectsResult, error) {
	return api.ListObjects(c, bucket, args)
}

// SimpleListObjects - list all objects of the given bucket with simple arguments
//
// PARAMS:
//     - bucket: the bucket name
//     - prefix: the prefix for listing
//     - maxKeys: the max number of result objects
//     - marker: the marker to mark the beginning for the listing
//     - delimiter: the delimiter for list objects
// RETURNS:
//     - *api.ListObjectsResult: the all objects of the bucket
//     - error: the return error if any occurs
func (c *Client) SimpleListObjects(bucket, prefix string, maxKeys int, marker,
	delimiter string) (*api.ListObjectsResult, error) {
	args := &api.ListObjectsArgs{delimiter, marker, maxKeys, prefix}
	return api.ListObjects(c, bucket, args)
}

// HeadBucket - test the given bucket existed and access authority
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - error: nil if exists and have authority otherwise the specific error
func (c *Client) HeadBucket(bucket string) error {
	return api.HeadBucket(c, bucket)
}

// DoesBucketExist - test the given bucket existed or not
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - bool: true if exists and false if not exists or occrus error
//     - error: nil if exists or not exist, otherwise the specific error
func (c *Client) DoesBucketExist(bucket string) (bool, error) {
	err := api.HeadBucket(c, bucket)
	if err == nil {
		return true, nil
	}
	if realErr, ok := err.(*bce.BceServiceError); ok {
		if realErr.StatusCode == http.StatusForbidden {
			return true, nil
		}
		if realErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
	}
	return false, err
}

// PutBucket - create a new bucket
//
// PARAMS:
//     - bucket: the new bucket name
// RETURNS:
//     - string: the location of the new bucket if create success
//     - error: nil if create success otherwise the specific error
func (c *Client) PutBucket(bucket string) (string, error) {
	return api.PutBucket(c, bucket)
}

// DeleteBucket - delete a empty bucket
//
// PARAMS:
//     - bucket: the bucket name to be deleted
// RETURNS:
//     - error: nil if delete success otherwise the specific error
func (c *Client) DeleteBucket(bucket string) error {
	return api.DeleteBucket(c, bucket)
}

// GetBucketLocation - get the location fo the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - string: the location of the bucket
//     - error: nil if success otherwise the specific error
func (c *Client) GetBucketLocation(bucket string) (string, error) {
	return api.GetBucketLocation(c, bucket)
}

// PutBucketAcl - set the acl of the given bucket with acl json file stream
//
// PARAMS:
//     - bucket: the bucket name
//     - aclBody: the acl json file body
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketAcl(bucket string, aclBody *bce.Body) error {
	return api.PutBucketAcl(c, bucket, "", aclBody)
}

// PutBucketAclFromCanned - set the canned acl of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
//     - cannedAcl: the cannedAcl string
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketAclFromCanned(bucket, cannedAcl string) error {
	return api.PutBucketAcl(c, bucket, cannedAcl, nil)
}

// PutBucketAclFromFile - set the acl of the given bucket with acl json file name
//
// PARAMS:
//     - bucket: the bucket name
//     - aclFile: the acl file name
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketAclFromFile(bucket, aclFile string) error {
	body, err := bce.NewBodyFromFile(aclFile)
	if err != nil {
		return err
	}
	return api.PutBucketAcl(c, bucket, "", body)
}

// PutBucketAclFromString - set the acl of the given bucket with acl json string
//
// PARAMS:
//     - bucket: the bucket name
//     - aclString: the acl string with json format
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketAclFromString(bucket, aclString string) error {
	body, err := bce.NewBodyFromString(aclString)
	if err != nil {
		return err
	}
	return api.PutBucketAcl(c, bucket, "", body)
}

// PutBucketAclFromStruct - set the acl of the given bucket with acl data structure
//
// PARAMS:
//     - bucket: the bucket name
//     - aclObj: the acl struct object
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketAclFromStruct(bucket string, aclObj *api.PutBucketAclArgs) error {
	jsonBytes, jsonErr := json.Marshal(aclObj)
	if jsonErr != nil {
		return jsonErr
	}
	body, err := bce.NewBodyFromBytes(jsonBytes)
	if err != nil {
		return err
	}
	return api.PutBucketAcl(c, bucket, "", body)
}

// GetBucketAcl - get the acl of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - *api.GetBucketAclResult: the result of the bucket acl
//     - error: nil if success otherwise the specific error
func (c *Client) GetBucketAcl(bucket string) (*api.GetBucketAclResult, error) {
	return api.GetBucketAcl(c, bucket)
}

// PutBucketLogging - set the loging setting of the given bucket with json stream
//
// PARAMS:
//     - bucket: the bucket name
//     - body: the json body
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketLogging(bucket string, body *bce.Body) error {
	return api.PutBucketLogging(c, bucket, body)
}

// PutBucketLoggingFromString - set the loging setting of the given bucket with json string
//
// PARAMS:
//     - bucket: the bucket name
//     - logging: the json format string
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketLoggingFromString(bucket, logging string) error {
	body, err := bce.NewBodyFromString(logging)
	if err != nil {
		return err
	}
	return api.PutBucketLogging(c, bucket, body)
}

// PutBucketLoggingFromStruct - set the loging setting of the given bucket with args object
//
// PARAMS:
//     - bucket: the bucket name
//     - obj: the logging setting object
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketLoggingFromStruct(bucket string, obj *api.PutBucketLoggingArgs) error {
	jsonBytes, jsonErr := json.Marshal(obj)
	if jsonErr != nil {
		return jsonErr
	}
	body, err := bce.NewBodyFromBytes(jsonBytes)
	if err != nil {
		return err
	}
	return api.PutBucketLogging(c, bucket, body)
}

// GetBucketLogging - get the logging setting of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - *api.GetBucketLoggingResult: the logging setting of the bucket
//     - error: nil if success otherwise the specific error
func (c *Client) GetBucketLogging(bucket string) (*api.GetBucketLoggingResult, error) {
	return api.GetBucketLogging(c, bucket)
}

// DeleteBucketLogging - delete the logging setting of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) DeleteBucketLogging(bucket string) error {
	return api.DeleteBucketLogging(c, bucket)
}

// PutBucketLifecycle - set the lifecycle rule of the given bucket with raw stream
//
// PARAMS:
//     - bucket: the bucket name
//     - lifecycle: the lifecycle rule json body
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketLifecycle(bucket string, lifecycle *bce.Body) error {
	return api.PutBucketLifecycle(c, bucket, lifecycle)
}

// PutBucketLifecycleFromString - set the lifecycle rule of the given bucket with string
//
// PARAMS:
//     - bucket: the bucket name
//     - lifecycle: the lifecycle rule json format string body
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketLifecycleFromString(bucket, lifecycle string) error {
	body, err := bce.NewBodyFromString(lifecycle)
	if err != nil {
		return err
	}
	return api.PutBucketLifecycle(c, bucket, body)
}

// GetBucketLifecycle - get the lifecycle rule of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - *api.GetBucketLifecycleResult: the lifecycle rule of the bucket
//     - error: nil if success otherwise the specific error
func (c *Client) GetBucketLifecycle(bucket string) (*api.GetBucketLifecycleResult, error) {
	return api.GetBucketLifecycle(c, bucket)
}

// DeleteBucketLifecycle - delete the lifecycle rule of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) DeleteBucketLifecycle(bucket string) error {
	return api.DeleteBucketLifecycle(c, bucket)
}

// PutBucketStorageclass - set the storage class of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
//     - storageClass: the storage class string value
// RETURNS:
//     - error: nil if success otherwise the specific error
func (c *Client) PutBucketStorageclass(bucket, storageClass string) error {
	return api.PutBucketStorageclass(c, bucket, storageClass)
}

// GetBucketStorageclass - get the storage class of the given bucket
//
// PARAMS:
//     - bucket: the bucket name
// RETURNS:
//     - string: the storage class string value
//     - error: nil if success otherwise the specific error
func (c *Client) GetBucketStorageclass(bucket string) (string, error) {
	return api.GetBucketStorageclass(c, bucket)
}

// PutObject - upload a new object or rewrite the existed object with raw stream
//
// PARAMS:
//     - bucket: the name of the bucket to store the object
//     - object: the name of the object
//     - body: the object content body
//     - args: the optional arguments
// RETURNS:
//     - string: etag of the uploaded object
//     - error: the uploaded error if any occurs
func (c *Client) PutObject(bucket, object string, body *bce.Body,
	args *api.PutObjectArgs) (string, error) {
	return api.PutObject(c, bucket, object, body, args)
}

// BasicPutObject - the basic interface of uploading an object
//
// PARAMS:
//     - bucket: the name of the bucket to store the object
//     - object: the name of the object
//     - body: the object content body
// RETURNS:
//     - string: etag of the uploaded object
//     - error: the uploaded error if any occurs
func (c *Client) BasicPutObject(bucket, object string, body *bce.Body) (string, error) {
	return api.PutObject(c, bucket, object, body, nil)
}

// PutObjectFromBytes - upload a new object or rewrite the existed object from a byte array
//
// PARAMS:
//     - bucket: the name of the bucket to store the object
//     - object: the name of the object
//     - bytesArr: the content byte array
//     - args: the optional arguments
// RETURNS:
//     - string: etag of the uploaded object
//     - error: the uploaded error if any occurs
func (c *Client) PutObjectFromBytes(bucket, object string, bytesArr []byte,
	args *api.PutObjectArgs) (string, error) {
	body, err := bce.NewBodyFromBytes(bytesArr)
	if err != nil {
		return "", err
	}
	return api.PutObject(c, bucket, object, body, args)
}

// PutObjectFromString - upload a new object or rewrite the existed object from a string
//
// PARAMS:
//     - bucket: the name of the bucket to store the object
//     - object: the name of the object
//     - content: the content string
//     - args: the optional arguments
// RETURNS:
//     - string: etag of the uploaded object
//     - error: the uploaded error if any occurs
func (c *Client) PutObjectFromString(bucket, object, content string,
	args *api.PutObjectArgs) (string, error) {
	body, err := bce.NewBodyFromString(content)
	if err != nil {
		return "", err
	}
	return api.PutObject(c, bucket, object, body, args)
}

// PutObjectFromFile - upload a new object or rewrite the existed object from a local file
//
// PARAMS:
//     - bucket: the name of the bucket to store the object
//     - object: the name of the object
//     - fileName: the local file full path name
//     - args: the optional arguments
// RETURNS:
//     - string: etag of the uploaded object
//     - error: the uploaded error if any occurs
func (c *Client) PutObjectFromFile(bucket, object, fileName string,
	args *api.PutObjectArgs) (string, error) {
	body, err := bce.NewBodyFromFile(fileName)
	if err != nil {
		return "", err
	}
	return api.PutObject(c, bucket, object, body, args)
}

// CopyObject - copy a remote object to another one
//
// PARAMS:
//     - bucket: the name of the destination bucket
//     - object: the name of the destination object
//     - srcBucket: the name of the source bucket
//     - srcObject: the name of the source object
//     - args: the optional arguments for copying object which are MetadataDirective, StorageClass,
//       IfMatch, IfNoneMatch, ifModifiedSince, IfUnmodifiedSince
// RETURNS:
//     - *api.CopyObjectResult: result struct which contains "ETag" and "LastModified" fields
//     - error: any error if it occurs
func (c *Client) CopyObject(bucket, object, srcBucket, srcObject string,
	args *api.CopyObjectArgs) (*api.CopyObjectResult, error) {
	source := fmt.Sprintf("/%s/%s", srcBucket, srcObject)
	return api.CopyObject(c, bucket, object, source, args)
}

// BasicCopyObject - the basic interface of copying a object to another one
//
// PARAMS:
//     - bucket: the name of the destination bucket
//     - object: the name of the destination object
//     - srcBucket: the name of the source bucket
//     - srcObject: the name of the source object
// RETURNS:
//     - *api.CopyObjectResult: result struct which contains "ETag" and "LastModified" fields
//     - error: any error if it occurs
func (c *Client) BasicCopyObject(bucket, object, srcBucket,
	srcObject string) (*api.CopyObjectResult, error) {
	source := fmt.Sprintf("/%s/%s", srcBucket, srcObject)
	return api.CopyObject(c, bucket, object, source, nil)
}

// GetObject - get the given object with raw stream return
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
//     - responseHeaders: the optional response headers to get the given object
//     - ranges: the optional range start and end to get the given object
// RETURNS:
//     - *api.GetObjectResult: result struct which contains "Body" and header fields
//       for details reference https://cloud.baidu.com/doc/BOS/API.html#GetObject.E6.8E.A5.E5.8F.A3
//     - error: any error if it occurs
func (c *Client) GetObject(bucket, object string, responseHeaders map[string]string,
	ranges ...int64) (*api.GetObjectResult, error) {
	return api.GetObject(c, bucket, object, responseHeaders, ranges...)
}

// BasicGetObject - the basic interface of geting the given object
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
// RETURNS:
//     - *api.GetObjectResult: result struct which contains "Body" and header fields
//       for details reference https://cloud.baidu.com/doc/BOS/API.html#GetObject.E6.8E.A5.E5.8F.A3
//     - error: any error if it occurs
func (c *Client) BasicGetObject(bucket, object string) (*api.GetObjectResult, error) {
	return api.GetObject(c, bucket, object, nil)
}

// BasicGetObjectToFile - use basic interface to get the given object to the given file path
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
//     - filePath: the file path to store the object content
// RETURNS:
//     - error: any error if it occurs
func (c *Client) BasicGetObjectToFile(bucket, object, filePath string) error {
	res, err := api.GetObject(c, bucket, object, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	file, fileErr := os.OpenFile(filePath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if fileErr != nil {
		return fileErr
	}
	defer file.Close()

	written, writeErr := io.CopyN(file, res.Body, res.ContentLength)
	if writeErr != nil {
		return writeErr
	}
	if written != res.ContentLength {
		return fmt.Errorf("written content size does not match the response content")
	}
	return nil
}

// GetObjectMeta - get the given object metadata
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
// RETURNS:
//     - *api.GetObjectMetaResult: metadata result, for details reference
//       https://cloud.baidu.com/doc/BOS/API.html#GetObjectMeta.E6.8E.A5.E5.8F.A3
//     - error: any error if it occurs
func (c *Client) GetObjectMeta(bucket, object string) (*api.GetObjectMetaResult, error) {
	return api.GetObjectMeta(c, bucket, object)
}

// FetchObject - fetch the object content from the given source and store
//
// PARAMS:
//     - bucket: the name of the bucket to store
//     - object: the name of the object to store
//     - source: fetch source url
//     - args: the optional arguments to fetch the object
// RETURNS:
//     - *api.FetchObjectResult: result struct with Code, Message, RequestId and JobId fields
//     - error: any error if it occurs
func (c *Client) FetchObject(bucket, object, source string,
	args *api.FetchObjectArgs) (*api.FetchObjectResult, error) {
	return api.FetchObject(c, bucket, object, source, args)
}

// BasicFetchObject - the basic interface of the fetch object api
//
// PARAMS:
//     - bucket: the name of the bucket to store
//     - object: the name of the object to store
//     - source: fetch source url
// RETURNS:
//     - *api.FetchObjectResult: result struct with Code, Message, RequestId and JobId fields
//     - error: any error if it occurs
func (c *Client) BasicFetchObject(bucket, object, source string) (*api.FetchObjectResult, error) {
	return api.FetchObject(c, bucket, object, source, nil)
}

// SimpleFetchObject - fetch object with simple arguments interface
//
// PARAMS:
//     - bucket: the name of the bucket to store
//     - object: the name of the object to store
//     - source: fetch source url
//     - mode: fetch mode which supports sync and async
//     - storageClass: the storage class of the fetched object
// RETURNS:
//     - *api.FetchObjectResult: result struct with Code, Message, RequestId and JobId fields
//     - error: any error if it occurs
func (c *Client) SimpleFetchObject(bucket, object, source, mode,
	storageClass string) (*api.FetchObjectResult, error) {
	args := &api.FetchObjectArgs{mode, storageClass}
	return api.FetchObject(c, bucket, object, source, args)
}

// AppendObject - append the gievn content to a new or existed object which is appendable
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
//     - content: the append object stream
//     - args: the optional arguments to append object
// RETURNS:
//     - *api.AppendObjectResult: the result of the appended object
//     - error: any error if it occurs
func (c *Client) AppendObject(bucket, object string, content *bce.Body,
	args *api.AppendObjectArgs) (*api.AppendObjectResult, error) {
	return api.AppendObject(c, bucket, object, content, args)
}

// SimpleAppendObject - the interface to append object with simple offset argument
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
//     - content: the append object stream
//     - offset: the offset of where to append
// RETURNS:
//     - *api.AppendObjectResult: the result of the appended object
//     - error: any error if it occurs
func (c *Client) SimpleAppendObject(bucket, object string, content *bce.Body,
	offset int64) (*api.AppendObjectResult, error) {
	return api.AppendObject(c, bucket, object, content, &api.AppendObjectArgs{Offset: offset})
}

// SimpleAppendObjectFromString - the simple interface of appending an object from a string
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
//     - content: the object string to append
//     - offset: the offset of where to append
// RETURNS:
//     - *api.AppendObjectResult: the result of the appended object
//     - error: any error if it occurs
func (c *Client) SimpleAppendObjectFromString(bucket, object, content string,
	offset int64) (*api.AppendObjectResult, error) {
	body, err := bce.NewBodyFromString(content)
	if err != nil {
		return nil, err
	}
	return api.AppendObject(c, bucket, object, body, &api.AppendObjectArgs{Offset: offset})
}

// SimpleAppendObjectFromFile - the simple interface of appending an object from a file
//
// PARAMS:
//     - bucket: the name of the bucket
//     - object: the name of the object
//     - filePath: the full file path
//     - offset: the offset of where to append
// RETURNS:
//     - *api.AppendObjectResult: the result of the appended object
//     - error: any error if it occurs
func (c *Client) SimpleAppendObjectFromFile(bucket, object, filePath string,
	offset int64) (*api.AppendObjectResult, error) {
	body, err := bce.NewBodyFromFile(filePath)
	if err != nil {
		return nil, err
	}
	return api.AppendObject(c, bucket, object, body, &api.AppendObjectArgs{Offset: offset})
}

// DeleteObject - delete the given object
//
// PARAMS:
//     - bucket: the name of the bucket to delete
//     - object: the name of the object to delete
// RETURNS:
//     - error: any error if it occurs
func (c *Client) DeleteObject(bucket, object string) error {
	return api.DeleteObject(c, bucket, object)
}

// DeleteMultipleObjects - delete a list of objects
//
// PARAMS:
//     - bucket: the name of the bucket to delete
//     - objectListStream: the object list stream to be deleted
// RETURNS:
//     - *api.DeleteMultipleObjectsResult: the delete information
//     - error: any error if it occurs
func (c *Client) DeleteMultipleObjects(bucket string,
	objectListStream *bce.Body) (*api.DeleteMultipleObjectsResult, error) {
	return api.DeleteMultipleObjects(c, bucket, objectListStream)
}

// DeleteMultipleObjectsFromString - delete a list of objects with json format string
//
// PARAMS:
//     - bucket: the name of the bucket to delete
//     - objectListString: the object list string to be deleted
// RETURNS:
//     - *api.DeleteMultipleObjectsResult: the delete information
//     - error: any error if it occurs
func (c *Client) DeleteMultipleObjectsFromString(bucket,
	objectListString string) (*api.DeleteMultipleObjectsResult, error) {
	body, err := bce.NewBodyFromString(objectListString)
	if err != nil {
		return nil, err
	}
	return api.DeleteMultipleObjects(c, bucket, body)
}

// DeleteMultipleObjectsFromStruct - delete a list of objects with object list struct
//
// PARAMS:
//     - bucket: the name of the bucket to delete
//     - objectListStruct: the object list struct to be deleted
// RETURNS:
//     - *api.DeleteMultipleObjectsResult: the delete information
//     - error: any error if it occurs
func (c *Client) DeleteMultipleObjectsFromStruct(bucket string,
	objectListStruct *api.DeleteMultipleObjectsArgs) (*api.DeleteMultipleObjectsResult, error) {
	jsonBytes, jsonErr := json.Marshal(objectListStruct)
	if jsonErr != nil {
		return nil, jsonErr
	}
	body, err := bce.NewBodyFromBytes(jsonBytes)
	if err != nil {
		return nil, err
	}
	return api.DeleteMultipleObjects(c, bucket, body)
}

// DeleteMultipleObjectsFromKeyList - delete a list of objects with given key string array
//
// PARAMS:
//     - bucket: the name of the bucket to delete
//     - keyList: the key stirng list to be deleted
// RETURNS:
//     - *api.DeleteMultipleObjectsResult: the delete information
//     - error: any error if it occurs
func (c *Client) DeleteMultipleObjectsFromKeyList(bucket string,
	keyList []string) (*api.DeleteMultipleObjectsResult, error) {
	if len(keyList) == 0 {
		return nil, fmt.Errorf("the key list to be deleted is empty")
	}
	args := make([]api.DeleteObjectArgs, len(keyList))
	for i, k := range keyList {
		args[i].Key = k
	}
	argsContainer := &api.DeleteMultipleObjectsArgs{args}

	jsonBytes, jsonErr := json.Marshal(argsContainer)
	if jsonErr != nil {
		return nil, jsonErr
	}
	body, err := bce.NewBodyFromBytes(jsonBytes)
	if err != nil {
		return nil, err
	}
	return api.DeleteMultipleObjects(c, bucket, body)
}

// InitiateMultipartUpload - initiate a multipart upload to get a upload ID
//
// PARAMS:
//     - bucket: the bucket name
//     - object: the object name
//     - contentType: the content type of the object to be uploaded which should be specified,
//       otherwise use the default(application/octet-stream)
//     - args: the optional arguments
// RETURNS:
//     - *InitiateMultipartUploadResult: the result data structure
//     - error: nil if ok otherwise the specific error
func (c *Client) InitiateMultipartUpload(bucket, object, contentType string,
	args *api.InitiateMultipartUploadArgs) (*api.InitiateMultipartUploadResult, error) {
	return api.InitiateMultipartUpload(c, bucket, object, contentType, args)
}

// BasicInitiateMultipartUpload - basic interface to initiate a multipart upload
//
// PARAMS:
//     - bucket: the bucket name
//     - object: the object name
// RETURNS:
//     - *InitiateMultipartUploadResult: the result data structure
//     - error: nil if ok otherwise the specific error
func (c *Client) BasicInitiateMultipartUpload(bucket,
	object string) (*api.InitiateMultipartUploadResult, error) {
	return api.InitiateMultipartUpload(c, bucket, object, "", nil)
}

// UploadPart - upload the single part in the multipart upload process
//
// PARAMS:
//     - bucket: the bucket name
//     - object: the object name
//     - uploadId: the multipart upload id
//     - partNumber: the current part number
//     - content: the uploaded part content
//     - args: the optional arguments
// RETURNS:
//     - string: the etag of the uploaded part
//     - error: nil if ok otherwise the specific error
func (c *Client) UploadPart(bucket, object, uploadId string, partNumber int,
	content *bce.Body, args *api.UploadPartArgs) (string, error) {
	return api.UploadPart(c, bucket, object, uploadId, partNumber, content, args)
}

// BasicUploadPart - basic interface to upload the single part in the multipart upload process
//
// PARAMS:
//     - bucket: the bucket name
//     - object: the object name
//     - uploadId: the multipart upload id
//     - partNumber: the current part number
//     - content: the uploaded part content
// RETURNS:
//     - string: the etag of the uploaded part
//     - error: nil if ok otherwise the specific error
func (c *Client) BasicUploadPart(bucket, object, uploadId string, partNumber int,
	content *bce.Body) (string, error) {
	return api.UploadPart(c, bucket, object, uploadId, partNumber, content, nil)
}

// UploadPartCopy - copy the multipart object
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - srcBucket: the source bucket
//     - srcObject: the source object
//     - uploadId: the multipart upload id
//     - partNumber: the current part number
//     - args: the optional arguments
// RETURNS:
//     - *CopyObjectResult: the lastModified and eTag of the part
//     - error: nil if ok otherwise the specific error
func (c *Client) UploadPartCopy(bucket, object, srcBucket, srcObject, uploadId string,
	partNumber int, args *api.UploadPartCopyArgs) (*api.CopyObjectResult, error) {
	source := fmt.Sprintf("/%s/%s", srcBucket, srcObject)
	return api.UploadPartCopy(c, bucket, object, source, uploadId, partNumber, args)
}

// BasicUploadPartCopy - basic interface to copy the multipart object
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - srcBucket: the source bucket
//     - srcObject: the source object
//     - uploadId: the multipart upload id
//     - partNumber: the current part number
// RETURNS:
//     - *CopyObjectResult: the lastModified and eTag of the part
//     - error: nil if ok otherwise the specific error
func (c *Client) BasicUploadPartCopy(bucket, object, srcBucket, srcObject, uploadId string,
	partNumber int) (*api.CopyObjectResult, error) {
	source := fmt.Sprintf("/%s/%s", srcBucket, srcObject)
	return api.UploadPartCopy(c, bucket, object, source, uploadId, partNumber, nil)
}

// CompleteMultipartUpload - finish a multipart upload operation with parts stream
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - uploadId: the multipart upload id
//     - parts: all parts info stream
//     - meta: user defined meta data
// RETURNS:
//     - *CompleteMultipartUploadResult: the result data
//     - error: nil if ok otherwise the specific error
func (c *Client) CompleteMultipartUpload(bucket, object, uploadId string,
	parts *bce.Body, meta map[string]string) (*api.CompleteMultipartUploadResult, error) {
	return api.CompleteMultipartUpload(c, bucket, object, uploadId, parts, meta)
}

// CompleteMultipartUploadFromStruct - finish a multipart upload operation with parts struct
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - uploadId: the multipart upload id
//     - parts: all parts info struct object
//     - meta: user defined meta data
// RETURNS:
//     - *CompleteMultipartUploadResult: the result data
//     - error: nil if ok otherwise the specific error
func (c *Client) CompleteMultipartUploadFromStruct(bucket, object, uploadId string,
	parts *api.CompleteMultipartUploadArgs,
	meta map[string]string) (*api.CompleteMultipartUploadResult, error) {
	jsonBytes, jsonErr := json.Marshal(parts)
	if jsonErr != nil {
		return nil, jsonErr
	}
	body, err := bce.NewBodyFromBytes(jsonBytes)
	if err != nil {
		return nil, err
	}
	return api.CompleteMultipartUpload(c, bucket, object, uploadId, body, meta)
}

// AbortMultipartUpload - abort a multipart upload operation
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - uploadId: the multipart upload id
// RETURNS:
//     - error: nil if ok otherwise the specific error
func (c *Client) AbortMultipartUpload(bucket, object, uploadId string) error {
	return api.AbortMultipartUpload(c, bucket, object, uploadId)
}

// ListParts - list the successfully uploaded parts info by upload id
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - uploadId: the multipart upload id
//     - args: the optional arguments
// RETURNS:
//     - *ListPartsResult: the uploaded parts info result
//     - error: nil if ok otherwise the specific error
func (c *Client) ListParts(bucket, object, uploadId string,
	args *api.ListPartsArgs) (*api.ListPartsResult, error) {
	return api.ListParts(c, bucket, object, uploadId, args)
}

// BasicListParts - basic interface to list the successfully uploaded parts info by upload id
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - uploadId: the multipart upload id
// RETURNS:
//     - *ListPartsResult: the uploaded parts info result
//     - error: nil if ok otherwise the specific error
func (c *Client) BasicListParts(bucket, object, uploadId string) (*api.ListPartsResult, error) {
	return api.ListParts(c, bucket, object, uploadId, nil)
}

// ListMultipartUploads - list the unfinished uploaded parts of the given bucket
//
// PARAMS:
//     - bucket: the destination bucket name
//     - args: the optional arguments
// RETURNS:
//     - *ListMultipartUploadsResult: the unfinished uploaded parts info result
//     - error: nil if ok otherwise the specific error
func (c *Client) ListMultipartUploads(bucket string,
	args *api.ListMultipartUploadsArgs) (*api.ListMultipartUploadsResult, error) {
	return api.ListMultipartUploads(c, bucket, args)
}

// BasicListMultipartUploads - basic interface to list the unfinished uploaded parts
//
// PARAMS:
//     - bucket: the destination bucket name
// RETURNS:
//     - *ListMultipartUploadsResult: the unfinished uploaded parts info result
//     - error: nil if ok otherwise the specific error
func (c *Client) BasicListMultipartUploads(bucket string) (
	*api.ListMultipartUploadsResult, error) {
	return api.ListMultipartUploads(c, bucket, nil)
}

// UploadSuperFile - parallel upload the super file by using the multipart upload interface
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - fileName: the local full path filename of the super file
//     - storageClass: the storage class to be set to the uploaded file
// RETURNS:
//     - error: nil if ok otherwise the specific error
func (c *Client) UploadSuperFile(bucket, object, fileName, storageClass string) error {
	// Get the file size and check the size for multipart upload
	file, fileErr := os.Open(fileName)
	if fileErr != nil {
		return fileErr
	}
	oldTimeout := c.Config.ConnectionTimeoutInMillis
	c.Config.ConnectionTimeoutInMillis = 0
	defer func() {
		c.Config.ConnectionTimeoutInMillis = oldTimeout
		file.Close()
	}()
	fileInfo, infoErr := file.Stat()
	if infoErr != nil {
		return infoErr
	}
	size := fileInfo.Size()
	if size < MIN_MULTIPART_SIZE || c.MultipartSize < MIN_MULTIPART_SIZE {
		return bce.NewBceClientError("multipart size should not be less than 5MB")
	}

	// Caculate part size and total part number
	partSize := (c.MultipartSize + MULTIPART_ALIGN - 1) / MULTIPART_ALIGN * MULTIPART_ALIGN
	partNum := (size + partSize - 1) / partSize
	if partNum > MAX_PART_NUMBER {
		partSize = (size + MAX_PART_NUMBER - 1) / MAX_PART_NUMBER
		partSize = (partSize + MULTIPART_ALIGN - 1) / MULTIPART_ALIGN * MULTIPART_ALIGN
		partNum = (size + partSize - 1) / partSize
	}
	log.Debugf("starting upload super file, total parts: %d, part size: %d", partNum, partSize)

	// Inner wrapper function of parallel uploading each part to get the ETag of the part
	uploadPart := func(bucket, object, uploadId string, partNumber int, body *bce.Body,
		result chan *api.UploadInfoType, ret chan error, id int64, pool chan int64) {
		etag, err := c.BasicUploadPart(bucket, object, uploadId, partNumber, body)
		if err != nil {
			result <- nil
			ret <- err
		} else {
			result <- &api.UploadInfoType{partNumber, etag}
		}
		pool <- id
	}

	// Do the parallel multipart upload
	resp, err := c.InitiateMultipartUpload(bucket, object, "",
		&api.InitiateMultipartUploadArgs{StorageClass: storageClass})
	if err != nil {
		return err
	}
	uploadId := resp.UploadId
	uploadedResult := make(chan *api.UploadInfoType, partNum)
	retChan := make(chan error, partNum)
	workerPool := make(chan int64, c.MaxParallel)
	for i := int64(0); i < c.MaxParallel; i++ {
		workerPool <- i
	}
	for partId := int64(1); partId <= partNum; partId++ {
		uploadSize := partSize
		offset := (partId - 1) * partSize
		left := size - offset
		if uploadSize > left {
			uploadSize = left
		}
		partBody, _ := bce.NewBodyFromSectionFile(file, offset, uploadSize)
		select { // wait until get a worker to upload
		case workerId := <-workerPool:
			go uploadPart(bucket, object, uploadId, int(partId), partBody,
				uploadedResult, retChan, workerId, workerPool)
		case uploadPartErr := <-retChan:
			c.AbortMultipartUpload(bucket, object, uploadId)
			return uploadPartErr
		}
	}

	// Check the return of each part uploading, and decide to complete or abort it
	completeArgs := &api.CompleteMultipartUploadArgs{make([]api.UploadInfoType, partNum)}
	for i := partNum; i > 0; i-- {
		uploaded := <-uploadedResult
		if uploaded == nil { // error occurs and not be caught in `select' statement
			c.AbortMultipartUpload(bucket, object, uploadId)
			return <-retChan
		}
		completeArgs.Parts[uploaded.PartNumber-1] = *uploaded
		log.Debugf("upload part %d success, etag: %s", uploaded.PartNumber, uploaded.ETag)
	}
	if _, err := c.CompleteMultipartUploadFromStruct(bucket, object,
		uploadId, completeArgs, nil); err != nil {
		c.AbortMultipartUpload(bucket, object, uploadId)
		return err
	}
	return nil
}

// DownloadSuperFile - parallel download the super file using the get object with range
//
// PARAMS:
//     - bucket: the destination bucket name
//     - object: the destination object name
//     - fileName: the local full path filename to store the object
// RETURNS:
//     - error: nil if ok otherwise the specific error
func (c *Client) DownloadSuperFile(bucket, object, fileName string) (err error) {
	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		return
	}
	oldTimeout := c.Config.ConnectionTimeoutInMillis
	c.Config.ConnectionTimeoutInMillis = 0
	defer func() {
		c.Config.ConnectionTimeoutInMillis = oldTimeout
		file.Close()
		if err != nil {
			os.Remove(fileName)
		}
	}()

	meta, err := c.GetObjectMeta(bucket, object)
	if err != nil {
		return
	}
	size := meta.ContentLength
	partSize := (c.MultipartSize + MULTIPART_ALIGN - 1) / MULTIPART_ALIGN * MULTIPART_ALIGN
	partNum := (size + partSize - 1) / partSize
	log.Debugf("starting download super file, total parts: %d, part size: %d", partNum, partSize)

	doneChan := make(chan struct{}, partNum)
	abortChan := make(chan struct{})

	// Set up multiple goroutine workers to download the object
	workerPool := make(chan int64, c.MaxParallel)
	for i := int64(0); i < c.MaxParallel; i++ {
		workerPool <- i
	}
	for i := int64(0); i < partNum; i++ {
		rangeStart := i * partSize
		rangeEnd := (i+1)*partSize - 1
		if rangeEnd > size-1 {
			rangeEnd = size - 1
		}
		select {
		case workerId := <-workerPool:
			go func(rangeStart, rangeEnd, workerId int64) {
				res, rangeGetErr := c.GetObject(bucket, object, nil, rangeStart, rangeEnd)
				if rangeGetErr != nil {
					log.Errorf("download object part(offset:%d, size:%d) failed: %v",
						rangeStart, res.ContentLength, rangeGetErr)
					abortChan <- struct{}{}
					err = rangeGetErr
					return
				}
				defer res.Body.Close()
				log.Debugf("writing part %d with offset=%d, size=%d", rangeStart/partSize,
					rangeStart, res.ContentLength)
				buf := make([]byte, 4096)
				offset := rangeStart
				for {
					n, e := res.Body.Read(buf)
					if e != nil && e != io.EOF {
						abortChan <- struct{}{}
						err = e
						return
					}
					if n == 0 {
						break
					}
					if _, writeErr := file.WriteAt(buf[:n], offset); writeErr != nil {
						abortChan <- struct{}{}
						err = writeErr
						return
					}
					offset += int64(n)
				}
				log.Debugf("writing part %d done", rangeStart/partSize)
				workerPool <- workerId
				doneChan <- struct{}{}
			}(rangeStart, rangeEnd, workerId)
		case <-abortChan: // abort range get if error occurs during downloading any part
			return
		}
	}

	// Wait for writing to local file done
	for i := partNum; i > 0; i-- {
		<-doneChan
	}
	return nil
}

// GeneratePresignedUrl - generate an authorization url with expire time and optional arguments
//
// PARAMS:
//     - bucket: the target bucket name
//     - object: the target object name
//     - expireInSeconds: the expire time in seconds of the signed url
//     - method: optional sign method, default is GET
//     - headers: optional sign headers, default just set the Host
//     - params: optional sign params, default is empty
// RETURNS:
//     - string: the presigned url with authorization string
func (c *Client) GeneratePresignedUrl(bucket, object string, expireInSeconds int, method string,
	headers, params map[string]string) string {
	return api.GeneratePresignedUrl(c.Config, c.Signer, bucket, object,
		expireInSeconds, method, headers, params)
}

// BasicGeneratePresignedUrl - basic interface to generate an authorization url with expire time
//
// PARAMS:
//     - bucket: the target bucket name
//     - object: the target object name
//     - expireInSeconds: the expire time in seconds of the signed url
// RETURNS:
//     - string: the presigned url with authorization string
func (c *Client) BasicGeneratePresignedUrl(bucket, object string, expireInSeconds int) string {
	return api.GeneratePresignedUrl(c.Config, c.Signer, bucket, object,
		expireInSeconds, "", nil, nil)
}
