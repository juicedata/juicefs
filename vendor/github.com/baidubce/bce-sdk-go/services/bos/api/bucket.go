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

// bucket.go - the bucket APIs definition supported by the BOS service

// Package api defines all APIs supported by the BOS service of BCE.
package api

import (
	"encoding/json"
	"strconv"

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/http"
)

// ListBuckets - list all buckets of the account
//
// PARAMS:
//     - cli: the client agent which can perform sending request
// RETURNS:
//     - *ListBucketsResult: the result bucket list structure
//     - error: nil if ok otherwise the specific error
func ListBuckets(cli bce.Client) (*ListBucketsResult, error) {
	req := &bce.BceRequest{}
	req.SetMethod(http.GET)
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	result := &ListBucketsResult{}
	if err := resp.ParseJsonBody(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ListObjects - list all objects of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
//     - args: the optional arguments to list objects
// RETURNS:
//     - *ListObjectsResult: the result object list structure
//     - error: nil if ok otherwise the specific error
func ListObjects(cli bce.Client, bucket string,
	args *ListObjectsArgs) (*ListObjectsResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.GET)

	// Optional arguments settings
	if args != nil {
		if len(args.Delimiter) != 0 {
			req.SetParam("delimiter", args.Delimiter)
		}
		if len(args.Marker) != 0 {
			req.SetParam("marker", args.Marker)
		}
		req.SetParam("maxKeys", strconv.Itoa(args.MaxKeys))
		if len(args.Prefix) != 0 {
			req.SetParam("prefix", args.Prefix)
		}
	}

	// Send the request and get result
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	result := &ListObjectsResult{}
	if err := resp.ParseJsonBody(result); err != nil {
		return nil, err
	}
	return result, nil
}

// HeadBucket - test the given bucket existed and access authority
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - error: nil if exists and have authority otherwise the specific error
func HeadBucket(cli bce.Client, bucket string) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.HEAD)
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// PutBucket - create a new bucket with the given name
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the new bucket name
// RETURNS:
//     - string: the location of the new bucket if create success
//     - error: nil if create success otherwise the specific error
func PutBucket(cli bce.Client, bucket string) (string, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.PUT)
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return "", err
	}
	if resp.IsFail() {
		return "", resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return resp.Header(http.LOCATION), nil
}

// DeleteBucket - delete an empty bucket by given name
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name to be deleted
// RETURNS:
//     - error: nil if delete success otherwise the specific error
func DeleteBucket(cli bce.Client, bucket string) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.DELETE)
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// GetBucketLocation - get the location of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - string: the location of the bucket
//     - error: nil if delete success otherwise the specific error
func GetBucketLocation(cli bce.Client, bucket string) (string, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.GET)
	req.SetParam("location", "")
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return "", err
	}
	if resp.IsFail() {
		return "", resp.ServiceError()
	}
	result := &LocationType{}
	if err := resp.ParseJsonBody(result); err != nil {
		return "", err
	}
	return result.LocationConstraint, nil
}

// PutBucketAcl - set the acl of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
//     - cannedAcl: support private, public-read, public-read-write
//     - aclBody: the acl file body
// RETURNS:
//     - error: nil if delete success otherwise the specific error
func PutBucketAcl(cli bce.Client, bucket, cannedAcl string, aclBody *bce.Body) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.PUT)
	req.SetParam("acl", "")

	// The acl setting
	if len(cannedAcl) != 0 && aclBody != nil {
		return bce.NewBceClientError("BOS does not support cannedAcl and acl file at the same time")
	}
	if validCannedAcl(cannedAcl) {
		req.SetHeader(http.BCE_ACL, cannedAcl)
	}
	if aclBody != nil {
		req.SetHeader(http.CONTENT_TYPE, bce.DEFAULT_CONTENT_TYPE)
		req.SetBody(aclBody)
	}

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// GetBucketAcl - get the acl of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - *GetBucketAclResult: the result of the bucket acl
//     - error: nil if success otherwise the specific error
func GetBucketAcl(cli bce.Client, bucket string) (*GetBucketAclResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.GET)
	req.SetParam("acl", "")

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	result := &GetBucketAclResult{}
	if err := resp.ParseJsonBody(result); err != nil {
		return nil, err
	}
	return result, nil
}

// PutBucketLogging - set the logging prefix of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
//     - logging: the logging prefix json string body
// RETURNS:
//     - error: nil if success otherwise the specific error
func PutBucketLogging(cli bce.Client, bucket string, logging *bce.Body) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.PUT)
	req.SetParam("logging", "")
	req.SetBody(logging)
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// GetBucketLogging - get the logging config of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - *GetBucketLoggingResult: the logging setting of the bucket
//     - error: nil if success otherwise the specific error
func GetBucketLogging(cli bce.Client, bucket string) (*GetBucketLoggingResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.GET)
	req.SetParam("logging", "")

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	result := &GetBucketLoggingResult{}
	if err := resp.ParseJsonBody(result); err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteBucketLogging - delete the logging setting of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - error: nil if success otherwise the specific error
func DeleteBucketLogging(cli bce.Client, bucket string) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.DELETE)
	req.SetParam("logging", "")
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// PutBucketLifecycle - set the lifecycle rule of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
//     - lifecycle: the lifecycle rule json string body
// RETURNS:
//     - error: nil if success otherwise the specific error
func PutBucketLifecycle(cli bce.Client, bucket string, lifecycle *bce.Body) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.PUT)
	req.SetParam("lifecycle", "")
	req.SetBody(lifecycle)
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// GetBucketLifecycle - get the lifecycle rule of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - *GetBucketLifecycleResult: the lifecycle rule of the bucket
//     - error: nil if success otherwise the specific error
func GetBucketLifecycle(cli bce.Client, bucket string) (*GetBucketLifecycleResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.GET)
	req.SetParam("lifecycle", "")

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	result := &GetBucketLifecycleResult{}
	if err := resp.ParseJsonBody(result); err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteBucketLifecycle - delete the lifecycle rule of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - error: nil if success otherwise the specific error
func DeleteBucketLifecycle(cli bce.Client, bucket string) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.DELETE)
	req.SetParam("lifecycle", "")
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// PutBucketStorageclass - set the storage class of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
//     - storageClass: the storage class string
// RETURNS:
//     - error: nil if success otherwise the specific error
func PutBucketStorageclass(cli bce.Client, bucket, storageClass string) error {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.PUT)
	req.SetParam("storageClass", "")

	obj := &StorageClassType{storageClass}
	jsonBytes, jsonErr := json.Marshal(obj)
	if jsonErr != nil {
		return jsonErr
	}
	body, err := bce.NewBodyFromBytes(jsonBytes)
	if err != nil {
		return err
	}
	req.SetBody(body)

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return err
	}
	if resp.IsFail() {
		return resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return nil
}

// GetBucketStorageclass - get the storage class of the given bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name
// RETURNS:
//     - string: the storage class of the bucket
//     - error: nil if success otherwise the specific error
func GetBucketStorageclass(cli bce.Client, bucket string) (string, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.GET)
	req.SetParam("storageClass", "")

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return "", err
	}
	if resp.IsFail() {
		return "", resp.ServiceError()
	}
	result := &StorageClassType{}
	if err := resp.ParseJsonBody(result); err != nil {
		return "", err
	}
	return result.StorageClass, nil
}
