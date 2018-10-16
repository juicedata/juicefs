package nosclient

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/auth"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/config"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/httpclient"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/logger"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/model"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/nosconst"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/noserror"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/utils"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

type NosClient struct {
	endPoint  string
	accessKey string
	secretKey string

	httpClient *http.Client
	Log        logger.NosLog
}

func NewHttpClient(connectTimeout, requestTimeout, readWriteTimeout,
	maxIdleConnection int) *http.Client {

	tr := &httpclient.Transport{
		ConnectTimeout:      time.Duration(connectTimeout) * time.Second,
		RequestTimeout:      time.Duration(requestTimeout) * time.Second,
		ReadWriteTimeout:    time.Duration(readWriteTimeout) * time.Second,
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: maxIdleConnection,
	}

	return &http.Client{Transport: tr}
}

// New constructs a new Driver with the given NOS credentials, bucket, chunksize flag
func New(conf *config.Config) (*NosClient, error) {
	noserror.Init()

	err := conf.Check()
	if err != nil {
		return nil, err
	}

	client := &NosClient{
		endPoint:  conf.Endpoint,
		accessKey: conf.AccessKey,
		secretKey: conf.SecretKey,

		httpClient: NewHttpClient(
			conf.NosServiceConnectTimeout,
			conf.NosServiceReadWriteTimeout,
			conf.NosServiceReadWriteTimeout,
			conf.NosServiceMaxIdleConnection),

		Log: logger.NosLog{
			LogLevel: conf.LogLevel,
			Logger:   conf.Logger,
		},
	}

	return client, nil
}

func (client *NosClient) getNosRequest(method, bucket, object string, metadata *model.ObjectMetadata,
	body io.Reader, params map[string]string, bodyStyle string) (*http.Request, error) {

	var opaque string
	urlStr := "http://" + bucket + "." + client.endPoint + "/"

	encodedObject := utils.NosUrlEncode(object)
	urlStr += encodedObject
	opaque = urlStr

	v := url.Values{}
	for key, val := range params {
		v.Add(key, val)
	}

	if len(v) > 0 {
		urlStr += "?" + v.Encode()
	}

	request, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return nil, err
	}
	request.URL.Opaque = opaque
	//add http header
	//request.Header.Set(nosconst.DATE, (time.Now().Format(nosconst.RFC1123_GMT)))
	request.Header.Set(nosconst.DATE, (time.Now().UTC().Format(nosconst.RFC1123_GMT)))
	request.Header.Set(nosconst.NOS_ENTITY_TYPE, bodyStyle)
	request.Header.Set(nosconst.USER_AGENT, utils.InitUserAgent())

	if metadata != nil {
		if metadata.Metadata != nil {
			for key, value := range metadata.Metadata {
				if value != "" {
					request.Header.Set(key, value)
				}
			}
		}
	}

	if client.accessKey != "" && client.secretKey != "" {
		request.Header.Set(nosconst.AUTHORIZATION,
			auth.SignRequest(request, client.accessKey, client.secretKey, bucket, encodedObject))
	}

	return request, nil
}

func (client *NosClient) CreateBucket(bucketName string, location nosconst.Location,
	acl nosconst.Acl) error {
	var locationConstraint string
	switch location {
	case nosconst.HZ:
		locationConstraint = "HZ"
	default:
		return errors.New("unsupported Location")
	}

	var aclString string

	switch acl {
	case nosconst.PUBLICREAD:
		aclString = "public-read"
	case nosconst.PRIVATE:
		aclString = "private"
	}

	request := &model.CreateBucketRequest{
		Location: locationConstraint,
	}
	body, err := xml.Marshal(request)
	if err != nil {
		return err
	}

	//Metadata
	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.X_NOS_ACL: aclString,
		},
	}

	req, err := client.getNosRequest("PUT", bucketName, "",
		metadata, bytes.NewReader(body), nil, nosconst.XML_TYPE)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return nil
	} else {
		err := utils.ProcessServerError(resp, bucketName, "")
		return err
	}
}

func (client *NosClient) PutObjectByStream(putObjectRequest *model.PutObjectRequest) (*model.ObjectResult, error) {
	if putObjectRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	var contentLength int64
	if putObjectRequest.Metadata != nil {
		contentLength = putObjectRequest.Metadata.ContentLength
	}

	err := utils.VerifyParamsWithLength(putObjectRequest.Bucket, putObjectRequest.Object, contentLength)
	if err != nil {
		return nil, err
	}

	request, err := client.getNosRequest("PUT", putObjectRequest.Bucket, putObjectRequest.Object,
		putObjectRequest.Metadata, putObjectRequest.Body, nil, nosconst.JSON_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode = ", resp.StatusCode)
	if resp.StatusCode == http.StatusOK {
		requestid, etag := utils.PopulateResponseHeader(resp)
		objectResult := &model.ObjectResult{
			Etag:      etag,
			RequestId: requestid,
		}

		return objectResult, nil
	} else {
		err := utils.ProcessServerError(resp, putObjectRequest.Bucket, putObjectRequest.Object)
		return nil, err
	}
}

func (client *NosClient) PutObjectByFile(putObjectRequest *model.PutObjectRequest) (*model.ObjectResult, error) {
	if putObjectRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	file, err := os.Open(putObjectRequest.FilePath)
	if err != nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_FILE_INVALID, "", "", err.Error())
	}
	defer file.Close()

	if putObjectRequest.Metadata == nil {
		putObjectRequest.Metadata = &model.ObjectMetadata{}
	}

	if putObjectRequest.Metadata.ContentLength == 0 {
		fi, err := file.Stat()
		if err == nil {
			putObjectRequest.Metadata.ContentLength = fi.Size()
		} else {
			return nil, utils.ProcessClientError(noserror.ERROR_CODE_FILE_INVALID, "", "", err.Error())
		}
	}

	putObjectRequest.Body = file

	return client.PutObjectByStream(putObjectRequest)
}

func (client *NosClient) CopyObject(copyObjectRequest *model.CopyObjectRequest) error {

	if copyObjectRequest == nil {
		return utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	srcBucket := copyObjectRequest.SrcBucket
	srcObject := copyObjectRequest.SrcObject
	destBucket := copyObjectRequest.DestBucket
	destObject := copyObjectRequest.DestObject

	err := utils.VerifyParamsWithObject(destBucket, destObject)
	if err != nil {
		return err
	}

	err = utils.VerifyParamsWithObject(srcBucket, srcObject)
	if err != nil {
		return utils.ProcessClientError(noserror.ERROR_CODE_SRCBUCKETANDOBJECT_ERROR, destBucket, destObject, "")
	}

	copySource := "/" + utils.NosUrlEncode(srcBucket) + "/" + utils.NosUrlEncode(srcObject)
	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.X_NOS_COPY_SOURCE: copySource,
		},
	}

	request, err := client.getNosRequest("PUT", destBucket, destObject, metadata, nil, nil, nosconst.JSON_TYPE)
	if err != nil {
		return err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return nil
	} else {
		err := utils.ProcessServerError(resp, destBucket, destObject)
		return err
	}
}

func (client *NosClient) MoveObject(moveObjectRequest *model.MoveObjectRequest) error {

	if moveObjectRequest == nil {
		return utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	srcBucket := moveObjectRequest.SrcBucket
	srcObject := moveObjectRequest.SrcObject
	destBucket := moveObjectRequest.DestBucket
	destObject := moveObjectRequest.DestObject

	err := utils.VerifyParamsWithObject(destBucket, destObject)
	if err != nil {
		return err
	}

	err = utils.VerifyParamsWithObject(srcBucket, srcObject)
	if err != nil {
		return utils.ProcessClientError(noserror.ERROR_CODE_SRCBUCKETANDOBJECT_ERROR, destBucket, destObject, "")
	}

	moveSource := "/" + srcBucket + "/" + srcObject
	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.X_NOS_MOVE_SOURCE: utils.NosUrlEncode(moveSource),
		},
	}

	request, err := client.getNosRequest("PUT", destBucket, destObject, metadata, nil, nil, nosconst.JSON_TYPE)
	if err != nil {
		return err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return nil
	} else {
		err := utils.ProcessServerError(resp, destBucket, destObject)
		return err
	}
}

func (client *NosClient) DeleteObject(deleteObjectRequest *model.ObjectRequest) error {

	if deleteObjectRequest == nil {
		return utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	err := utils.VerifyParamsWithObject(deleteObjectRequest.Bucket, deleteObjectRequest.Object)
	if err != nil {
		return err
	}

	request, err := client.getNosRequest("DELETE", deleteObjectRequest.Bucket, deleteObjectRequest.Object,
		nil, nil, nil, nosconst.JSON_TYPE)
	if err != nil {
		return err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return nil
	} else {
		err := utils.ProcessServerError(resp, deleteObjectRequest.Bucket, deleteObjectRequest.Object)
		return err
	}
}

func (client *NosClient) DeleteMultiObjects(deleteRequest *model.DeleteMultiObjectsRequest) (*model.DeleteObjectsResult,
	error) {

	if deleteRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	err := utils.VerifyParams(deleteRequest.Bucket)
	if err != nil {
		return nil, err
	}
	delectObjects := deleteRequest.DelectObjects
	if delectObjects == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_DELETEMULTIOBJECTS_ERROR, "", "", "")
	}
	if len(delectObjects.Objects) > nosconst.MAX_FILENUMBER {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_OBJECTSBIGGER_ERROR, "", "", "")
	}

	body, err := xml.Marshal(delectObjects)
	if err != nil {
		return nil, err
	}

	contentLength := int64(len(body))
	if contentLength > nosconst.MAX_DELETEBODY {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_OBJECTSBIGGER_ERROR, "", "", "")
	}

	md5Ctx := md5.New()
	md5Ctx.Write(body)
	cipherStr := md5Ctx.Sum(nil)
	metadata := &model.ObjectMetadata{
		ContentLength: contentLength,
		Metadata: map[string]string{
			"Content-MD5": hex.EncodeToString(cipherStr),
		},
	}
	params := map[string]string{
		"delete": "",
	}
	request, err := client.getNosRequest("POST", deleteRequest.Bucket, "", metadata,
		bytes.NewReader(body), params, nosconst.XML_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		result := &model.DeleteObjectsResult{}

		err := utils.ParseXmlBody(resp.Body, result)
		if err != nil {
			return nil, err
		}
		return result, nil
	} else {
		err := utils.ProcessServerError(resp, deleteRequest.Bucket, "")
		return nil, err
	}
}

func (client *NosClient) GetObject(getObjectRequest *model.GetObjectRequest) (*model.NOSObject, error) {

	if getObjectRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	err := utils.VerifyParamsWithObject(getObjectRequest.Bucket, getObjectRequest.Object)
	if err != nil {
		return nil, err
	}

	metadata := &model.ObjectMetadata{
		Metadata: map[string]string{
			nosconst.IfMODIFYSINCE: getObjectRequest.IfModifiedSince,
			nosconst.RANGE:         getObjectRequest.ObjRange,
		},
	}

	request, err := client.getNosRequest("GET", getObjectRequest.Bucket, getObjectRequest.Object, metadata,
		nil, nil, nosconst.JSON_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}

	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
		nosObject := &model.NOSObject{
			Key:            getObjectRequest.Object,
			BucketName:     getObjectRequest.Bucket,
			ObjectMetadata: utils.PopulateAllHeader(resp),
			Body:           resp.Body,
		}
		return nosObject, nil
	} else if resp.StatusCode == http.StatusNotModified {
		return nil, nil
	} else {
		err := utils.ProcessServerError(resp, getObjectRequest.Bucket, getObjectRequest.Object)
		resp.Body.Close()
		return nil, err
	}
}

func (client *NosClient) DoesObjectExist(objectRequest *model.ObjectRequest) (bool, error) {

	if objectRequest == nil {
		return false, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	err := utils.VerifyParamsWithObject(objectRequest.Bucket, objectRequest.Object)
	if err != nil {
		return false, err
	}

	request, err := client.getNosRequest("HEAD", objectRequest.Bucket, objectRequest.Object, nil, nil,
		nil, nosconst.JSON_TYPE)
	if err != nil {
		return false, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return false, err
	}

	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return true, nil
	} else if resp.StatusCode == http.StatusNotFound {
		return false, nil
	} else {
		err := utils.ProcessServerError(resp, objectRequest.Bucket, objectRequest.Object)
		return false, err
	}
}

func (client *NosClient) GetObjectMetaData(objectRequest *model.ObjectRequest) (*model.ObjectMetadata, error) {

	if objectRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	err := utils.VerifyParamsWithObject(objectRequest.Bucket, objectRequest.Object)
	if err != nil {
		return nil, err
	}

	request, err := client.getNosRequest("HEAD", objectRequest.Bucket, objectRequest.Object, nil,
		nil, nil, nosconst.JSON_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}

	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return utils.PopulateAllHeader(resp), nil
	} else {
		err := utils.ProcessServerError(resp, objectRequest.Bucket, objectRequest.Object)
		return nil, err
	}
}

func (client *NosClient) ListObjects(listObjectsRequest *model.ListObjectsRequest) (*model.ListObjectsResult, error) {

	if listObjectsRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := listObjectsRequest.Bucket
	prefix := listObjectsRequest.Prefix
	delimiter := listObjectsRequest.Delimiter
	marker := listObjectsRequest.Marker
	maxKeys := listObjectsRequest.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 100
	}

	err := utils.VerifyParams(bucket)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		nosconst.LIST_PREFIX:    prefix,
		nosconst.LIST_DELIMITER: delimiter,
		nosconst.LIST_MARKER:    marker,
		nosconst.LIST_MAXKEYS:   strconv.Itoa(maxKeys),
	}

	request, err := client.getNosRequest("GET", bucket, "", nil, nil, params, nosconst.XML_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		result := &model.ListObjectsResult{}
		err = utils.ParseXmlBody(resp.Body, result)
		if err != nil {
			return nil, err
		}
		return result, nil
	} else {
		err := utils.ProcessServerError(resp, bucket, "")
		return nil, err
	}
}

// multipart upload api
func (client *NosClient) InitMultiUpload(initMultiUploadRequest *model.InitMultiUploadRequest) (*model.InitMultiUploadResult, error) {

	if initMultiUploadRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := initMultiUploadRequest.Bucket
	object := initMultiUploadRequest.Object
	metadata := initMultiUploadRequest.Metadata

	err := utils.VerifyParamsWithObject(bucket, object)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		"uploads": "",
	}

	request, err := client.getNosRequest("POST", bucket, object, metadata, nil, params, nosconst.XML_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		result := &model.InitMultiUploadResult{}
		err = utils.ParseXmlBody(resp.Body, result)
		if err != nil {
			return nil, err
		}
		return result, nil
	} else {
		err := utils.ProcessServerError(resp, bucket, object)
		return nil, err
	}
}

func (client *NosClient) UploadPart(uploadPartRequest *model.UploadPartRequest) (*model.ObjectResult, error) {

	if uploadPartRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := uploadPartRequest.Bucket
	object := uploadPartRequest.Object
	uploadId := uploadPartRequest.UploadId
	partNumber := uploadPartRequest.PartNumber
	content := uploadPartRequest.Content
	partSize := uploadPartRequest.PartSize
	contentMd5 := uploadPartRequest.ContentMd5

	err := utils.VerifyParamsWithObject(bucket, object)
	if err != nil {
		return nil, err
	}
	metadata := &model.ObjectMetadata{}
	metadata.Metadata = make(map[string]string)
	if contentMd5 != "" {
		metadata.Metadata[nosconst.CONTENT_MD5] = contentMd5
	}

	params := map[string]string{
		nosconst.UPLOADID:   uploadId,
		nosconst.PARTNUMBER: strconv.FormatInt(int64(partNumber), 10),
	}
	limitReader := &io.LimitedReader{
		R: bytes.NewReader(content),
		N: partSize,
	}
	request, err := client.getNosRequest("PUT", bucket, object, metadata, limitReader,
		params, nosconst.JSON_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		requestid, etag := utils.PopulateResponseHeader(resp)
		objectResult := &model.ObjectResult{
			Etag:      etag,
			RequestId: requestid,
		}
		return objectResult, nil
	} else {
		err := utils.ProcessServerError(resp, bucket, object)
		return nil, err
	}
}

func (client *NosClient) CompleteMultiUpload(completeMultiUploadRequest *model.CompleteMultiUploadRequest) (
	*model.CompleteMultiUploadResult, error) {

	if completeMultiUploadRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := completeMultiUploadRequest.Bucket
	object := completeMultiUploadRequest.Object
	uploadId := completeMultiUploadRequest.UploadId
	parts := completeMultiUploadRequest.Parts
	contentMd5 := completeMultiUploadRequest.ContentMd5
	objectMd5 := completeMultiUploadRequest.ObjectMd5

	err := utils.VerifyParamsWithObject(bucket, object)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		nosconst.UPLOADID: uploadId,
	}

	metadata := &model.ObjectMetadata{}
	metadata.Metadata = make(map[string]string)
	if contentMd5 != "" {
		metadata.Metadata[nosconst.CONTENT_MD5] = contentMd5
	}
	if objectMd5 != "" {
		metadata.Metadata[nosconst.X_NOS_OBJECT_MD5] = objectMd5
	}

	uploadParts := model.UploadParts{Parts: parts}
	body, err := xml.Marshal(uploadParts)
	if err != nil {
		return nil, err
	}

	request, err := client.getNosRequest("POST", bucket, object, metadata, bytes.NewReader(body),
		params, nosconst.XML_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		result := &model.CompleteMultiUploadResult{}
		err = utils.ParseXmlBody(resp.Body, result)
		if err != nil {
			return nil, err
		}
		result.Etag = utils.RemoveQuotes(result.Etag)
		return result, nil
	} else {
		err := utils.ProcessServerError(resp, bucket, object)
		return nil, err
	}
}

func (client *NosClient) AbortMultiUpload(abortMultiUploadRequest *model.AbortMultiUploadRequest) error {

	if abortMultiUploadRequest == nil {
		return utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := abortMultiUploadRequest.Bucket
	object := abortMultiUploadRequest.Object
	uploadId := abortMultiUploadRequest.UploadId

	err := utils.VerifyParamsWithObject(bucket, object)
	if err != nil {
		return err
	}

	params := map[string]string{
		nosconst.UPLOADID: uploadId,
	}

	request, err := client.getNosRequest("DELETE", bucket, object, nil, nil, params, nosconst.JSON_TYPE)
	if err != nil {
		return err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		return nil
	} else {
		err := utils.ProcessServerError(resp, bucket, object)
		return err
	}
}

func (client *NosClient) ListUploadParts(listUploadPartsRequest *model.ListUploadPartsRequest) (*model.ListPartsResult, error) {

	if listUploadPartsRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := listUploadPartsRequest.Bucket
	object := listUploadPartsRequest.Object
	uploadId := listUploadPartsRequest.UploadId
	maxParts := listUploadPartsRequest.MaxParts
	partNumberMarker := listUploadPartsRequest.PartNumberMarker

	err := utils.VerifyParamsWithObject(bucket, object)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		nosconst.UPLOADID:           uploadId,
		nosconst.MAX_PARTS:          strconv.Itoa(maxParts),
		nosconst.PART_NUMBER_MARKER: strconv.Itoa(partNumberMarker),
	}

	request, err := client.getNosRequest("GET", bucket, object, nil, nil, params, nosconst.XML_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		result := &model.ListPartsResult{}
		err = utils.ParseXmlBody(resp.Body, result)
		if err != nil {
			return nil, err
		}

		return result, nil
	} else {
		err := utils.ProcessServerError(resp, bucket, object)
		return nil, err
	}
}

// This operation lists in-progress multipart uploads.
func (client *NosClient) ListMultiUploads(listMultiUploadsRequest *model.ListMultiUploadsRequest) (
	*model.ListMultiUploadsResult, error) {

	if listMultiUploadsRequest == nil {
		return nil, utils.ProcessClientError(noserror.ERROR_CODE_REQUEST_ERROR, "", "", "")
	}

	bucket := listMultiUploadsRequest.Bucket
	err := utils.VerifyParams(bucket)
	if err != nil {
		return nil, err
	}

	if listMultiUploadsRequest.MaxUploads == 0 {
		listMultiUploadsRequest.MaxUploads = nosconst.DEFAULTVALUE
	}

	params := map[string]string{
		nosconst.UPLOADS:          "",
		nosconst.LIST_KEY_MARKER:  listMultiUploadsRequest.KeyMarker,
		nosconst.LIST_MAX_UPLOADS: strconv.Itoa(listMultiUploadsRequest.MaxUploads),
	}

	request, err := client.getNosRequest("GET", bucket, "", nil, nil, params, nosconst.XML_TYPE)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	client.Log.Debug("resp.StatusCode=", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		result := &model.ListMultiUploadsResult{}
		err = utils.ParseXmlBody(resp.Body, result)
		if err != nil {
			return nil, err
		}

		return result, nil
	} else {
		err := utils.ProcessServerError(resp, bucket, "")
		return nil, err
	}

}
