package model

import (
	"encoding/xml"
	"io"
)

// Create Bucket 

type CreateBucketRequest struct {
    XMLName xml.Name  `xml:"CreateBucketConfiguration"`
    Location  string  `xml:"LocationConstraint"`
}



// CompleteMultiUpload
type UploadPart struct {
	XMLName    xml.Name `xml:"Part"`
	PartNumber int      `xml:"PartNumber"`
	Etag       string   `xml:"ETag"`
}

type UploadParts struct {
	XMLName xml.Name     `xml:"CompleteMultipartUpload"`
	Parts   []UploadPart `xml:"Part"`
}

func (uploadParts *UploadParts) Append(part UploadPart) {
	uploadParts.Parts = append(uploadParts.Parts, part)
}

// DeleteMultiObjects
type DeleteObject struct {
	XMLName xml.Name `xml:"Object"`
	Key     string   `xml:"Key"`
}

type DeleteMultiObjects struct {
	XMLName xml.Name       `xml:"Delete"`
	Quiet   bool           `xml:"Quiet"`
	Objects []DeleteObject `xml:"Object"`
}

func (deleteMulti *DeleteMultiObjects) Append(object DeleteObject) {
	deleteMulti.Objects = append(deleteMulti.Objects, object)
}

type ObjectMetadata struct {
	ContentLength int64
	Metadata      map[string]string
}

type PutObjectRequest struct {
	Bucket   string
	Object   string
	Body     io.ReadSeeker
	FilePath string
	Metadata *ObjectMetadata
}

type CopyObjectRequest struct {
	SrcBucket  string
	SrcObject  string
	DestBucket string
	DestObject string
}

type MoveObjectRequest struct {
	SrcBucket  string
	SrcObject  string
	DestBucket string
	DestObject string
}

type DeleteMultiObjectsRequest struct {
	Bucket        string
	DelectObjects *DeleteMultiObjects
}

type GetObjectRequest struct {
	Bucket          string
	Object          string
	ObjRange        string
	IfModifiedSince string
}

type ObjectRequest struct {
	Bucket string
	Object string
}

type ListObjectsRequest struct {
	Bucket    string
	Prefix    string
	Delimiter string
	Marker    string
	MaxKeys   int
}

type InitMultiUploadRequest struct {
	Bucket   string
	Object   string
	Metadata *ObjectMetadata
}

type UploadPartRequest struct {
	Bucket     string
	Object     string
	UploadId   string
	PartNumber int
	Content    []byte
	PartSize   int64
	ContentMd5 string
}

type CompleteMultiUploadRequest struct {
	Bucket     string
	Object     string
	UploadId   string
	Parts      []UploadPart
	ContentMd5 string
	ObjectMd5  string
}

type AbortMultiUploadRequest struct {
	Bucket   string
	Object   string
	UploadId string
}

type ListUploadPartsRequest struct {
	Bucket           string
	Object           string
	UploadId         string
	MaxParts         int
	PartNumberMarker int
}

type ListMultiUploadsRequest struct {
	Bucket     string
	KeyMarker  string
	MaxUploads int
}
