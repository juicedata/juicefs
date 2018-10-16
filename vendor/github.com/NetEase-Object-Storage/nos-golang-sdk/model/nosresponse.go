package model

import (
	"encoding/xml"
	"io"
)

type ObjectResult struct {
	Etag      string
	RequestId string
}

type NOSObject struct {
	Key            string
	BucketName     string
	ObjectMetadata *ObjectMetadata
	Body           io.ReadCloser `type:"blob"`
}

type DeleteError struct {
	XMLName xml.Name `xml:"Error"`
	Key     string   `xml:"Key"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

type DeleteKey struct {
	XMLName xml.Name `xml:"Deleted"`
	Key     string   `xml:"Key"`
}

type DeleteObjectsResult struct {
	XMLName xml.Name      `xml:"DeleteResult"`
	Deleted []DeleteKey   `xml:"Deleted"`
	Error   []DeleteError `xml:"Error"`
}

type ListObjectsResult struct {
	XMLName        xml.Name       `xml:"ListBucketResult"`
	Bucket         string         `xml:"Name"`
	Prefix         string         `xml:"Prefix"`
	CommonPrefixes []CommonPrefix `xml:"CommonPrefixes"`
	MaxKeys        string         `xml:"MaxKeys"`
	NextMarker     string         `xml:"NextMarker"`
	IsTruncated    bool           `xml:"IsTruncated"`
	Contents       []Contents     `xml:"Contents"`
}

type InitMultiUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Object   string   `xml:"Key"`
	UploadId string   `xml:"UploadId"`
}

type CompleteMultiUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	Etag     string   `xml:"ETag"`
}

type Owner struct {
	XMLName     xml.Name `xml:"Owner"`
	Id          string   `xml:"ID"`
	DisplayName string   `xml:"DisplayName"`
}

type UploadPartRet struct {
	XMLName      xml.Name `xml:"Part"`
	PartNumber   int      `xml:"PartNumber"`
	LastModified string   `xml:"LastModified"`
	Etag         string   `xml:"ETag"`
	Size         int      `xml:"Size"`
}

type ListPartsResult struct {
	XMLName              xml.Name        `xml:"ListPartsResult"`
	Bucket               string          `xml:"Bucket"`
	Key                  string          `xml:"Key"`
	UploadId             string          `xml:"UploadId"`
	Owner                Owner           `xml:"Owner"`
	StorageClass         string          `xml:"StorageClass"`
	PartNumberMarker     int             `xml:"PartNumberMarker"`
	NextPartNumberMarker int             `xml:"NextPartNumberMarker"`
	MaxPart              int             `xml:"MaxParts"`
	IsTruncated          bool            `xml:"IsTruncated"`
	Parts                []UploadPartRet `xml:"Part"`
}

type Contents struct {
	XMLName      xml.Name `xml:"Contents"`
	Key          string   `xml:"Key"`
	LastModified string   `xml:"LastModified"`
	Etag         string   `xml:"Etag"`
	Size         int64    `xml:"Size"`
}

type CommonPrefix struct {
	XMLName xml.Name `xml:"CommonPrefixes"`
	Prefix  string   `xml:"Prefix"`
}

type MultipartUpload struct {
	XMLName      xml.Name `xml:"Upload"`
	Key          string   `xml:"Key"`
	UploadId     string   `xml:"UploadId"`
	StorageClass string   `xml:"StorageClass"`
	Owner        Owner    `xml:"Owner"`
	Initiated    string   `xml:"Initiated"`
}

type ListMultiUploadsResult struct {
	XMLName       xml.Name          `xml:"ListMultipartUploadsResult"`
	Bucket        string            `xml:"Bucket"`
	NextKeyMarker string            `xml:"NextKeyMarker"`
	IsTruncated   bool              `xml:"IsTruncated"`
	Uploads       []MultipartUpload `xml:"Upload"`
}
