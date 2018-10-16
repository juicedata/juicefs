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

// object.go - the object APIs definition supported by the BOS service

package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/baidubce/bce-sdk-go/auth"
	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/http"
	"github.com/baidubce/bce-sdk-go/util"
)

// PutObject - put the object from the string or the stream
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name of the object
//     - object: the name of the object
//     - body: the input content of the object
//     - args: the optional arguments of this api
// RETURNS:
//     - string: the etag of the object
//     - error: nil if ok otherwise the specific error
func PutObject(cli bce.Client, bucket, object string, body *bce.Body,
	args *PutObjectArgs) (string, error) {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
	req.SetMethod(http.PUT)
	req.SetBody(body)

	// Optional arguments settings
	if args != nil {
		setOptionalNullHeaders(req, map[string]string{
			http.CACHE_CONTROL:       args.CacheControl,
			http.CONTENT_DISPOSITION: args.ContentDisposition,
			http.CONTENT_TYPE:        args.ContentType,
			http.EXPIRES:             args.Expires,
			http.BCE_CONTENT_SHA256:  args.ContentSha256,
		})
		if args.ContentLength != 0 {
			// User specified Content-Length can be smaller than the body size, so the body should
			// be reset. The `net/http.Client' does not support the Content-Length bigger than the
			// body size.
			if args.ContentLength > body.Size() {
				return "", bce.NewBceClientError("content-length can't be bigger than body size")
			}
			if args.ContentLength < 0 {
				return "", bce.NewBceClientError("content-length can't be a negative number")
			}
			body, err := bce.NewBodyFromSizedReader(body.Stream(), args.ContentLength)
			if err != nil {
				return "", bce.NewBceClientError(err.Error())
			}
			req.SetBody(body)
			req.SetHeader(http.CONTENT_LENGTH, fmt.Sprintf("%d", args.ContentLength))
		}

		// Reset the contentMD5 if set by user
		if len(args.ContentMD5) != 0 {
			req.SetHeader(http.CONTENT_MD5, args.ContentMD5)
		}

		if validStorageClass(args.StorageClass) {
			req.SetHeader(http.BCE_STORAGE_CLASS, args.StorageClass)
		} else {
			if len(args.StorageClass) != 0 {
				return "", bce.NewBceClientError("invalid storage class value: " +
					args.StorageClass)
			}
		}
		if err := setUserMetadata(req, args.UserMeta); err != nil {
			return "", err
		}
	}

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return "", err
	}
	if resp.IsFail() {
		return "", resp.ServiceError()
	}
	defer func() { resp.Body().Close() }()
	return strings.Trim(resp.Header(http.ETAG), "\""), nil
}

// CopyObject - copy one object to a new object with new bucket and/or name. It can alse set the
// metadata of the object with the same source and target.
//
// PARAMS:
//     - cli: the client object which can perform sending request
//     - bucket: the bucket name of the target object
//     - object: the name of the target object
//     - source: the source object uri
//     - *CopyObjectArgs: the optional input args for copying object
// RETURNS:
//     - *CopyObjectResult: the result object which contains etag and lastmodified
//     - error: nil if ok otherwise the specific error
func CopyObject(cli bce.Client, bucket, object, source string,
	args *CopyObjectArgs) (*CopyObjectResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
	req.SetMethod(http.PUT)
	if len(source) == 0 {
		return nil, bce.NewBceClientError("copy source should not be null")
	}
	req.SetHeader(http.BCE_COPY_SOURCE, util.UriEncode(source, false))

	// Optional arguments settings
	if args != nil {
		setOptionalNullHeaders(req, map[string]string{
			http.CACHE_CONTROL:                       args.CacheControl,
			http.CONTENT_DISPOSITION:                 args.ContentDisposition,
			http.CONTENT_ENCODING:                    args.ContentEncoding,
			http.CONTENT_RANGE:                       args.ContentRange,
			http.CONTENT_TYPE:                        args.ContentType,
			http.EXPIRES:                             args.Expires,
			http.LAST_MODIFIED:                       args.LastModified,
			http.ETAG:                                args.ETag,
			http.CONTENT_MD5:                         args.ContentMD5,
			http.BCE_CONTENT_SHA256:                  args.ContentSha256,
			http.BCE_OBJECT_TYPE:                     args.ObjectType,
			http.BCE_NEXT_APPEND_OFFSET:              args.NextAppendOffset,
			http.BCE_COPY_SOURCE_IF_MATCH:            args.IfMatch,
			http.BCE_COPY_SOURCE_IF_NONE_MATCH:       args.IfNoneMatch,
			http.BCE_COPY_SOURCE_IF_MODIFIED_SINCE:   args.IfModifiedSince,
			http.BCE_COPY_SOURCE_IF_UNMODIFIED_SINCE: args.IfUnmodifiedSince,
		})
		if args.ContentLength != 0 {
			req.SetHeader(http.CONTENT_LENGTH, fmt.Sprintf("%d", args.ContentLength))
		}
		if validMetadataDirective(args.MetadataDirective) {
			req.SetHeader(http.BCE_COPY_METADATA_DIRECTIVE, args.MetadataDirective)
		} else {
			if len(args.MetadataDirective) != 0 {
				return nil, bce.NewBceClientError(
					"invalid metadata directive value: " + args.MetadataDirective)
			}
		}
		if validStorageClass(args.StorageClass) {
			req.SetHeader(http.BCE_STORAGE_CLASS, args.StorageClass)
		} else {
			if len(args.StorageClass) != 0 {
				return nil, bce.NewBceClientError("invalid storage class value: " +
					args.StorageClass)
			}
		}
		if err := setUserMetadata(req, args.UserMeta); err != nil {
			return nil, err
		}
	}

	// Send request and get the result
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	jsonBody := &CopyObjectResult{}
	if err := resp.ParseJsonBody(jsonBody); err != nil {
		return nil, err
	}
	return jsonBody, nil
}

// GetObject - get the object content with range and response-headers-specified support
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name of the object
//     - object: the name of the object
//     - responseHeaders: the optional response headers to get the given object
//     - ranges: the optional range start and end to get the given object
// RETURNS:
//     - *GetObjectResult: the output content result of the object
//     - error: nil if ok otherwise the specific error
func GetObject(cli bce.Client, bucket, object string, responseHeaders map[string]string,
	ranges ...int64) (*GetObjectResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
	req.SetMethod(http.GET)

	// Optional arguments settings
	if responseHeaders != nil {
		for k, v := range responseHeaders {
			if _, ok := GET_OBJECT_ALLOWED_RESPONSE_HEADERS[k]; ok {
				req.SetParam("response"+k, v)
			}
		}
	}
	if len(ranges) != 0 {
		rangeStr := "bytes="
		if len(ranges) == 1 {
			rangeStr += fmt.Sprintf("%d", ranges[0]) + "-"
		} else {
			rangeStr += fmt.Sprintf("%d", ranges[0]) + "-" + fmt.Sprintf("%d", ranges[1])
		}
		req.SetHeader("Range", rangeStr)
	}

	// Send request and get the result
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	headers := resp.Headers()
	result := &GetObjectResult{}
	if val, ok := headers[http.CACHE_CONTROL]; ok {
		result.CacheControl = val
	}
	if val, ok := headers[http.CONTENT_DISPOSITION]; ok {
		result.ContentDisposition = val
	}
	if val, ok := headers[http.CONTENT_LENGTH]; ok {
		if length, err := strconv.ParseInt(val, 10, 64); err == nil {
			result.ContentLength = length
		}
	}
	if val, ok := headers[http.CONTENT_RANGE]; ok {
		result.ContentRange = val
	}
	if val, ok := headers[http.CONTENT_TYPE]; ok {
		result.ContentType = val
	}
	if val, ok := headers[http.CONTENT_MD5]; ok {
		result.ContentMD5 = val
	}
	if val, ok := headers[http.EXPIRES]; ok {
		result.Expires = val
	}
	if val, ok := headers[http.LAST_MODIFIED]; ok {
		result.LastModified = val
	}
	if val, ok := headers[http.ETAG]; ok {
		result.ETag = strings.Trim(val, "\"")
	}
	if val, ok := headers[http.CONTENT_LANGUAGE]; ok {
		result.ContentLanguage = val
	}
	if val, ok := headers[http.CONTENT_ENCODING]; ok {
		result.ContentEncoding = val
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_CONTENT_SHA256)]; ok {
		result.ContentSha256 = val
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_STORAGE_CLASS)]; ok {
		result.StorageClass = val
	}
	bcePrefix := toHttpHeaderKey(http.BCE_USER_METADATA_PREFIX)
	for k, v := range headers {
		if strings.Index(k, bcePrefix) == 0 {
			if result.UserMeta == nil {
				result.UserMeta = make(map[string]string)
			}
			result.UserMeta[k[len(bcePrefix):]] = v
		}
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_OBJECT_TYPE)]; ok {
		result.ObjectType = val
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_NEXT_APPEND_OFFSET)]; ok {
		result.NextAppendOffset = val
	}
	result.Body = resp.Body()
	return result, nil
}

// GetObjectMeta - get the meta data of the given object
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name of the object
//     - object: the name of the object
// RETURNS:
//     - *GetObjectMetaResult: the result of this api
//     - error: nil if ok otherwise the specific error
func GetObjectMeta(cli bce.Client, bucket, object string) (*GetObjectMetaResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
	req.SetMethod(http.HEAD)

	// Send request and get the result
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	headers := resp.Headers()
	result := &GetObjectMetaResult{}
	if val, ok := headers[http.CACHE_CONTROL]; ok {
		result.CacheControl = val
	}
	if val, ok := headers[http.CONTENT_DISPOSITION]; ok {
		result.ContentDisposition = val
	}
	if val, ok := headers[http.CONTENT_LENGTH]; ok {
		if length, err := strconv.ParseInt(val, 10, 64); err == nil {
			result.ContentLength = length
		}
	}
	if val, ok := headers[http.CONTENT_RANGE]; ok {
		result.ContentRange = val
	}
	if val, ok := headers[http.CONTENT_TYPE]; ok {
		result.ContentType = val
	}
	if val, ok := headers[http.CONTENT_MD5]; ok {
		result.ContentMD5 = val
	}
	if val, ok := headers[http.EXPIRES]; ok {
		result.Expires = val
	}
	if val, ok := headers[http.LAST_MODIFIED]; ok {
		result.LastModified = val
	}
	if val, ok := headers[http.ETAG]; ok {
		result.ETag = strings.Trim(val, "\"")
	}
	if val, ok := headers[http.CONTENT_ENCODING]; ok {
		result.ContentEncoding = val
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_CONTENT_SHA256)]; ok {
		result.ContentSha256 = val
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_STORAGE_CLASS)]; ok {
		result.StorageClass = val
	}
	bcePrefix := toHttpHeaderKey(http.BCE_USER_METADATA_PREFIX)
	for k, v := range headers {
		if strings.Index(k, bcePrefix) == 0 {
			if result.UserMeta == nil {
				result.UserMeta = make(map[string]string)
			}
			result.UserMeta[k[len(bcePrefix):]] = v
		}
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_OBJECT_TYPE)]; ok {
		result.ObjectType = val
	}
	if val, ok := headers[toHttpHeaderKey(http.BCE_NEXT_APPEND_OFFSET)]; ok {
		result.NextAppendOffset = val
	}
	defer func() { resp.Body().Close() }()
	return result, nil
}

// FetchObject - fetch the object by the given url and store it to a bucket
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name to store the object
//     - object: the name of the object to be stored
//     - source: the source url to fetch
//     - args: the optional arguments to perform the fetch operation
// RETURNS:
//     - *FetchObjectArgs: the result of this api
//     - error: nil if ok otherwise the specific error
func FetchObject(cli bce.Client, bucket, object, source string,
	args *FetchObjectArgs) (*FetchObjectResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
	req.SetMethod(http.POST)
	req.SetParam("fetch", "")
	if len(source) == 0 {
		return nil, bce.NewBceClientError("invalid fetch source value: " + source)
	}
	req.SetHeader(http.BCE_PREFIX+"fetch-source", source)

	// Optional arguments settings
	if args != nil {
		if validFetchMode(args.FetchMode) {
			req.SetHeader(http.BCE_PREFIX+"fetch-mode", args.FetchMode)
		} else {
			if len(args.FetchMode) != 0 {
				return nil, bce.NewBceClientError("invalid fetch mode value: " + args.FetchMode)
			}
		}
		if validStorageClass(args.StorageClass) {
			req.SetHeader(http.BCE_STORAGE_CLASS, args.StorageClass)
		} else {
			if len(args.StorageClass) != 0 {
				return nil, bce.NewBceClientError("invalid storage class value: " +
					args.StorageClass)
			}
		}
	}

	// Send request and get the result
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	jsonBody := &FetchObjectResult{}
	if err := resp.ParseJsonBody(jsonBody); err != nil {
		return nil, err
	}
	return jsonBody, nil
}

// AppendObject - append the gievn content to a new or existed object which is appendable
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name of the object
//     - object: the name of the object
//     - content: the content to be appended
//     - args: the optional arguments to perform the append operation
// RETURNS:
//     - *AppendObjectResult: the result status for this api
//     - error: nil if ok otherwise the specific error
func AppendObject(cli bce.Client, bucket, object string, content *bce.Body,
	args *AppendObjectArgs) (*AppendObjectResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
	req.SetMethod(http.POST)
	req.SetParam("append", "")
	req.SetBody(content)

	// Optional arguments settings
	if args != nil {
		if args.Offset < 0 {
			return nil, bce.NewBceClientError(
				fmt.Sprintf("invalid append offset value: %d", args.Offset))
		}
		if args.Offset > 0 {
			req.SetParam("offset", fmt.Sprintf("%d", args.Offset))
		}
		setOptionalNullHeaders(req, map[string]string{
			http.CACHE_CONTROL:       args.CacheControl,
			http.CONTENT_DISPOSITION: args.ContentDisposition,
			http.CONTENT_MD5:         args.ContentMD5,
			http.CONTENT_TYPE:        args.ContentType,
			http.EXPIRES:             args.Expires,
			http.BCE_CONTENT_SHA256:  args.ContentSha256,
		})

		if validStorageClass(args.StorageClass) {
			req.SetHeader(http.BCE_STORAGE_CLASS, args.StorageClass)
		} else {
			if len(args.StorageClass) != 0 {
				return nil, bce.NewBceClientError("invalid storage class value: " +
					args.StorageClass)
			}
		}
		if err := setUserMetadata(req, args.UserMeta); err != nil {
			return nil, err
		}
	}

	// Send request and get the result
	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	nextOffset, offsetErr := strconv.ParseInt(
		resp.Header(http.BCE_PREFIX+"next-append-offset"), 10, 64)
	if offsetErr != nil {
		nextOffset = content.Size()
	}
	result := &AppendObjectResult{
		resp.Header(http.CONTENT_MD5),
		nextOffset,
		strings.Trim(resp.Header(http.ETAG), "\"")}
	defer func() { resp.Body().Close() }()
	return result, nil
}

// DeleteObject - delete the given object
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name of the object to be deleted
//     - object: the name of the object
// RETURNS:
//     - error: nil if ok otherwise the specific error
func DeleteObject(cli bce.Client, bucket, object string) error {
	req := &bce.BceRequest{}
	req.SetUri(getObjectUri(bucket, object))
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

// DeleteMultipleObjects - delete the given objects within a single http request
//
// PARAMS:
//     - cli: the client agent which can perform sending request
//     - bucket: the bucket name of the objects to be deleted
//     - objectListStream: the objects list to be delete with json format
// RETURNS:
//     - *DeleteMultipleObjectsResult: the objects failed to delete
//     - error: nil if ok otherwise the specific error
func DeleteMultipleObjects(cli bce.Client, bucket string,
	objectListStream *bce.Body) (*DeleteMultipleObjectsResult, error) {
	req := &bce.BceRequest{}
	req.SetUri(getBucketUri(bucket))
	req.SetMethod(http.POST)
	req.SetParam("delete", "")
	req.SetHeader(http.CONTENT_TYPE, "application/json; charset=utf-8")
	req.SetBody(objectListStream)

	resp := &bce.BceResponse{}
	if err := cli.SendRequest(req, resp); err != nil {
		return nil, err
	}
	if resp.IsFail() {
		return nil, resp.ServiceError()
	}
	jsonBody := &DeleteMultipleObjectsResult{}
	if err := resp.ParseJsonBody(jsonBody); err != nil {
		return nil, err
	}
	return jsonBody, nil
}

// GeneratePresignedUrl - generate an authorization url with expire time and optional arguments
//
// PARAMS:
//     - conf: the client configuration
//     - signer: the client signer object to generate the authorization string
//     - bucket: the target bucket name
//     - object: the target object name
//     - expire: expire time in seconds
//     - method: optional sign method, default is GET
//     - headers: optional sign headers, default just set the Host
//     - params: optional sign params, default is empty
// RETURNS:
//     - string: the presigned url with authorization string
func GeneratePresignedUrl(conf *bce.BceClientConfiguration, signer auth.Signer, bucket,
	object string, expire int, method string, headers, params map[string]string) string {
	req := &bce.BceRequest{}

	// Set basic arguments
	req.SetUri(bce.URI_PREFIX + object)
	if len(method) == 0 {
		method = http.GET
	}
	req.SetMethod(method)
	req.SetEndpoint(conf.Endpoint)
	if req.Protocol() == "" {
		req.SetProtocol(bce.DEFAULT_PROTOCOL)
	}
	if len(bucket) != 0 { // only for ListBuckets API
		req.SetHost(bucket + "." + req.Host())
	}

	// Set headers and params if given.
	req.SetHeader(http.HOST, req.Host())
	if headers != nil {
		for k, v := range headers {
			req.SetHeader(k, v)
		}
	}
	if params != nil {
		for k, v := range params {
			req.SetParam(k, v)
		}
	}

	// Copy one SignOptions object to rewrite it.
	option := *conf.SignOption
	if expire != 0 {
		option.ExpireSeconds = expire
	}

	// Generate the authorization string and return the signed url.
	signer.Sign(&req.Request, conf.Credentials, &option)
	req.SetParam("authorization", req.Header(http.AUTHORIZATION))
	return fmt.Sprintf("%s://%s%s?%s", req.Protocol(), req.Host(), req.Uri(), req.QueryString())
}
