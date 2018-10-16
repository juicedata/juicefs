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

// model.go - definitions of the request arguments and results data structure model

package api

import (
	"io"
)

type OwnerType struct {
	Id          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type BucketSummaryType struct {
	Name         string `json:"name"`
	Location     string `json:"location"`
	CreationDate string `json:"creationDate"`
}

// ListBucketsResult defines the result structure of ListBuckets api.
type ListBucketsResult struct {
	Owner   OwnerType           `json:"owner"`
	Buckets []BucketSummaryType `json:"buckets"`
}

// ListObjectsArgs defines the optional arguments for ListObjects api.
type ListObjectsArgs struct {
	Delimiter string `json:"delimiter"`
	Marker    string `json:"marker"`
	MaxKeys   int    `json:"maxKeys"`
	Prefix    string `json:"prefix"`
}

type ObjectSummaryType struct {
	Key          string    `json:"key"`
	LastModified string    `json:"lastModified"`
	ETag         string    `json:"eTag"`
	Size         int       `json:"size"`
	StorageClass string    `json:"storageClass"`
	Owner        OwnerType `json:"owner"`
}

type PrefixType struct {
	Prefix string `json:"prefix"`
}

// ListObjectsResult defines the result structure of ListObjects api.
type ListObjectsResult struct {
	Name           string              `json:"name"`
	Prefix         string              `json:"prefix"`
	Delimiter      string              `json:"delimiter"`
	Marker         string              `json:"marker"`
	NextMarker     string              `json:"nextMarker,omitempty"`
	MaxKeys        int                 `json:"maxKeys"`
	IsTruncated    bool                `json:"isTruncated"`
	Contents       []ObjectSummaryType `json:"contents"`
	CommonPrefixes []PrefixType        `json:"commonPrefixes"`
}

type LocationType struct {
	LocationConstraint string `json:"locationConstraint"`
}

// AclOwnerType defines the owner struct in ACL setting
type AclOwnerType struct {
	Id string `json:"id"`
}

// GranteeType defines the grantee struct in ACL setting
type GranteeType struct {
	Id string `json:"id"`
}

type AclRefererType struct {
	StringLike   []string `json:"stringLike"`
	StringEquals []string `json:"stringEquals"`
}

type AclCondType struct {
	IpAddress []string       `json:"ipAddress"`
	Referer   AclRefererType `json:"referer"`
}

// GrantType defines the grant struct in ACL setting
type GrantType struct {
	Grantee     []GranteeType `json:"grantee"`
	Permission  []string      `json:"permission"`
	Resource    []string      `json:"resource,omitempty"`
	NotResource []string      `json:"notResource,omitempty"`
	Condition   AclCondType   `json:"condition,omitempty"`
}

// PutBucketAclArgs defines the input args structure for putting bucket acl.
type PutBucketAclArgs struct {
	AccessControlList []GrantType `json:"accessControlList"`
}

// GetBucketAclResult defines the result structure of getting bucket acl.
type GetBucketAclResult struct {
	AccessControlList []GrantType  `json:"accessControlList"`
	Owner             AclOwnerType `json:"owner"`
}

// PutBucketLoggingArgs defines the input args structure for putting bucket logging.
type PutBucketLoggingArgs struct {
	TargetBucket string `json:"targetBucket"`
	TargetPrefix string `json:"targetPrefix"`
}

// GetBucketLoggingResult defines the result structure for getting bucket logging.
type GetBucketLoggingResult struct {
	Status       string `json:"status"`
	TargetBucket string `json:"targetBucket,omitempty"`
	TargetPrefix string `json:"targetPrefix, omitempty"`
}

// LifecycleConditionTimeType defines the structure of time condition
type LifecycleConditionTimeType struct {
	DateGreaterThan string `json:"dateGreaterThan"`
}

// LifecycleConditionType defines the structure of condition
type LifecycleConditionType struct {
	Time LifecycleConditionTimeType `json:"time"`
}

// LifecycleActionType defines the structure of lifecycle action
type LifecycleActionType struct {
	Name         string `json:"name"`
	StorageClass string `json:"storageClass,omitempty"`
}

// LifecycleRuleType defines the structure of a single lifecycle rule
type LifecycleRuleType struct {
	Id        string                 `json:"id"`
	Status    string                 `json:"status"`
	Resource  []string               `json:"resource"`
	Condition LifecycleConditionType `json:"condition"`
	Action    LifecycleActionType    `json:"action"`
}

// GetBucketLifecycleResult defines the lifecycle argument structure for putting
type PutBucketLifecycleArgs struct {
	Rule []LifecycleRuleType `json:"rule"`
}

// GetBucketLifecycleResult defines the lifecycle result structure for getting
type GetBucketLifecycleResult struct {
	Rule []LifecycleRuleType `json:"rule"`
}

type StorageClassType struct {
	StorageClass string `json:"storageClass"`
}

// PutObjectArgs defines the optional args structure for the put object api.
type PutObjectArgs struct {
	CacheControl       string
	ContentDisposition string
	ContentMD5         string
	ContentType        string
	ContentLength      int64
	Expires            string
	UserMeta           map[string]string
	ContentSha256      string
	StorageClass       string
}

// CopyObjectArgs defines the optional args structure for the copy object api.
type CopyObjectArgs struct {
	ObjectMeta
	MetadataDirective string
	IfMatch           string
	IfNoneMatch       string
	IfModifiedSince   string
	IfUnmodifiedSince string
}

// CopyObjectResult defines the result json structure for the copy object api.
type CopyObjectResult struct {
	LastModified string `json:"lastModified"`
	ETag         string `json:"eTag"`
}

type ObjectMeta struct {
	CacheControl       string
	ContentDisposition string
	ContentEncoding    string
	ContentLength      int64
	ContentRange       string
	ContentType        string
	ContentMD5         string
	ContentSha256      string
	Expires            string
	LastModified       string
	ETag               string
	UserMeta           map[string]string
	StorageClass       string
	NextAppendOffset   string
	ObjectType         string
}

// GetObjectResult defines the result data of the get object api.
type GetObjectResult struct {
	ObjectMeta
	ContentLanguage string
	Body            io.ReadCloser
}

// GetObjectMetaResult defines the result data of the get object meta api.
type GetObjectMetaResult struct {
	ObjectMeta
}

// FetchObjectArgs defines the optional arguments structure for the fetch object api.
type FetchObjectArgs struct {
	FetchMode    string
	StorageClass string
}

// FetchObjectResult defines the result json structure for the fetch object api.
type FetchObjectResult struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestId string `json:"requestId"`
	JobId     string `json:"jobId"`
}

// AppendObjectArgs defines the optional arguments structure for appending object.
type AppendObjectArgs struct {
	Offset             int64
	CacheControl       string
	ContentDisposition string
	ContentMD5         string
	ContentType        string
	Expires            string
	UserMeta           map[string]string
	ContentSha256      string
	StorageClass       string
}

// AppendObjectResult defines the result data structure for appending object.
type AppendObjectResult struct {
	ContentMD5       string
	NextAppendOffset int64
	ETag             string
}

// DeleteObjectArgs defines the input args structure for a single object.
type DeleteObjectArgs struct {
	Key string `json:"key"`
}

// DeleteMultipleObjectsResult defines the input args structure for deleting multiple objects.
type DeleteMultipleObjectsArgs struct {
	Objects []DeleteObjectArgs `json:"objects"`
}

// DeleteObjectResult defines the result structure for deleting a single object.
type DeleteObjectResult struct {
	Key     string `json:"key"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DeleteMultipleObjectsResult defines the result structure for deleting multiple objects.
type DeleteMultipleObjectsResult struct {
	Errors []DeleteObjectResult `json:"errors"`
}

// InitiateMultipartUploadArgs defines the input arguments to initiate a multipart upload.
type InitiateMultipartUploadArgs struct {
	CacheControl       string
	ContentDisposition string
	Expires            string
	StorageClass       string
}

// InitiateMultipartUploadResult defines the result structure to initiate a multipart upload.
type InitiateMultipartUploadResult struct {
	Bucket   string `json:"bucket"`
	Key      string `json:"key"`
	UploadId string `json:"uploadId"`
}

// UploadPartArgs defines the optinoal argumets for uploading part.
type UploadPartArgs struct {
	ContentMD5    string
	ContentSha256 string
}

// UploadPartCopyArgs defines the optional arguments of UploadPartCopy.
type UploadPartCopyArgs struct {
	SourceRange       string
	IfMatch           string
	IfNoneMatch       string
	IfModifiedSince   string
	IfUnmodifiedSince string
}

// UploadInfoType defines an uploaded part info structure.
type UploadInfoType struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"eTag"`
}

// CompleteMultipartUploadArgs defines the input arguments structure of CompleteMultipartUpload.
type CompleteMultipartUploadArgs struct {
	Parts []UploadInfoType `json:"parts"`
}

// CompleteMultipartUploadResult defines the result structure of CompleteMultipartUpload.
type CompleteMultipartUploadResult struct {
	Location string `json:"location"`
	Bucket   string `json:"bucket"`
	Key      string `json:"key"`
	ETag     string `json:"eTag"`
}

// ListPartsArgs defines the input optional arguments of listing parts information.
type ListPartsArgs struct {
	MaxParts         int
	PartNumberMarker string
}

type ListPartType struct {
	PartNumber   int    `json:"partNumber"`
	LastModified string `json:"lastModified"`
	ETag         string `json:"eTag"`
	Size         int    `json:"size"`
}

// ListPartsResult defines the parts info result from ListParts.
type ListPartsResult struct {
	Bucket               string         `json:"bucket"`
	Key                  string         `json:"key"`
	UploadId             string         `json:"uploadId"`
	Initiated            string         `json:"initiated"`
	Owner                OwnerType      `json:"owner"`
	StorageClass         string         `json:"storageClass"`
	PartNumberMarker     int            `json:"partNumberMarker"`
	NextPartNumberMarker int            `json:"nextPartNumberMarker"`
	MaxParts             int            `json:"maxParts"`
	IsTruncated          bool           `json:"isTruncated"`
	Parts                []ListPartType `json:"parts"`
}

// ListMultipartUploadsArgs defines the optional arguments for ListMultipartUploads.
type ListMultipartUploadsArgs struct {
	Delimiter  string
	KeyMarker  string
	MaxUploads int
	Prefix     string
}

type ListMultipartUploadsType struct {
	Key          string    `json:"key"`
	UploadId     string    `json:"uploadId"`
	Owner        OwnerType `json:"owner"`
	Initiated    string    `json:"initiated"`
	StorageClass string    `json:"storageClass,omitempty"`
}

// ListMultipartUploadsResult defines the multipart uploads result structure.
type ListMultipartUploadsResult struct {
	Bucket         string                     `json:"bucket"`
	CommonPrefixes []PrefixType               `json:"commonPrefixes"`
	Delimiter      string                     `json:"delimiter"`
	Prefix         string                     `json:"prefix"`
	IsTruncated    bool                       `json:"isTruncated"`
	KeyMarker      string                     `json:"keyMarker"`
	MaxUploads     int                        `json:"maxUploads"`
	NextKeyMarker  string                     `json:"nextKeyMarker"`
	Uploads        []ListMultipartUploadsType `json:"uploads"`
}
