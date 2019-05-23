package obs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

type IReadCloser interface {
	setReadCloser(body io.ReadCloser)
}

func (output *GetObjectOutput) setReadCloser(body io.ReadCloser) {
	output.Body = body
}

type IBaseModel interface {
	setStatusCode(statusCode int)

	setRequestId(requestId string)

	setResponseHeaders(responseHeaders map[string][]string)
}

type ISerializable interface {
	trans() (map[string]string, map[string][]string, interface{})
}

type DefaultSerializable struct {
	params  map[string]string
	headers map[string][]string
	data    interface{}
}

func (input DefaultSerializable) trans() (map[string]string, map[string][]string, interface{}) {
	return input.params, input.headers, input.data
}

var defaultSerializable = &DefaultSerializable{}

func newSubResourceSerial(subResource SubResourceType) *DefaultSerializable {
	return &DefaultSerializable{map[string]string{string(subResource): ""}, nil, nil}
}

func trans(subResource SubResourceType, input interface{}) (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(subResource): ""}
	data, _ = ConvertRequestToIoReader(input)
	return
}

func (baseModel *BaseModel) setStatusCode(statusCode int) {
	baseModel.StatusCode = statusCode
}

func (baseModel *BaseModel) setRequestId(requestId string) {
	baseModel.RequestId = requestId
}

func (baseModel *BaseModel) setResponseHeaders(responseHeaders map[string][]string) {
	baseModel.ResponseHeaders = responseHeaders
}

func (input ListBucketsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	headers = make(map[string][]string)
	if input.QueryLocation {
		headers[HEADER_LOCATION_AMZ] = []string{"true"}
	}
	return
}

func (input CreateBucketInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	headers = make(map[string][]string)
	if acl := string(input.ACL); acl != "" {
		headers[HEADER_ACL_AMZ] = []string{acl}
	}

	if storageClass := string(input.StorageClass); storageClass != "" {
		headers[HEADER_STORAGE_CLASS] = []string{storageClass}
	}

	if input.FsFileInterface {
		headers[HEADER_FS_FILE_INTERFACE_OBS] = []string{"Enabled"}
	}

	if location := strings.TrimSpace(input.Location); location != "" {
		input.Location = location
		data, _ = ConvertRequestToIoReader(input)
	}

	return
}

func (input SetBucketStoragePolicyInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	return trans(SubResourceStoragePolicy, input)
}

func (input ListObjsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = make(map[string]string)
	if input.Prefix != "" {
		params["prefix"] = input.Prefix
	}
	if input.Delimiter != "" {
		params["delimiter"] = input.Delimiter
	}
	if input.MaxKeys > 0 {
		params["max-keys"] = IntToString(input.MaxKeys)
	}
	headers = make(map[string][]string)
	if origin := strings.TrimSpace(input.Origin); origin != "" {
		headers[HEADER_ORIGIN_CAMEL] = []string{origin}
	}
	if requestHeader := strings.TrimSpace(input.RequestHeader); requestHeader != "" {
		headers[HEADER_ACCESS_CONTROL_REQUEST_HEADER_CAMEL] = []string{requestHeader}
	}
	return
}

func (input ListObjectsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.ListObjsInput.trans()
	if input.Marker != "" {
		params["marker"] = input.Marker
	}
	return
}

func (input ListVersionsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.ListObjsInput.trans()
	params[string(SubResourceVersions)] = ""
	if input.KeyMarker != "" {
		params["key-marker"] = input.KeyMarker
	}
	if input.VersionIdMarker != "" {
		params["version-id-marker"] = input.VersionIdMarker
	}
	return
}

func (input ListMultipartUploadsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceUploads): ""}
	if input.Prefix != "" {
		params["prefix"] = input.Prefix
	}
	if input.Delimiter != "" {
		params["delimiter"] = input.Delimiter
	}
	if input.MaxUploads > 0 {
		params["max-uploads"] = IntToString(input.MaxUploads)
	}
	if input.KeyMarker != "" {
		params["key-marker"] = input.KeyMarker
	}
	if input.UploadIdMarker != "" {
		params["upload-id-marker"] = input.UploadIdMarker
	}
	return
}

func (input SetBucketQuotaInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	return trans(SubResourceQuota, input)
}

func (input SetBucketAclInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceAcl): ""}
	headers = make(map[string][]string)

	if acl := string(input.ACL); acl != "" {
		headers[HEADER_ACL_AMZ] = []string{acl}
	} else {
		data, _ = ConvertAclToXml(input.AccessControlPolicy, false)
	}
	return
}

func (input SetBucketPolicyInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourcePolicy): ""}
	data = strings.NewReader(input.Policy)
	return
}

func (input SetBucketCorsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceCors): ""}
	data, md5, _ := ConvertRequestToIoReaderV2(input)
	headers = map[string][]string{HEADER_MD5_CAMEL: []string{md5}}
	return
}

func (input SetBucketVersioningInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	return trans(SubResourceVersioning, input)
}

func (input SetBucketWebsiteConfigurationInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceWebsite): ""}
	data, _ = ConvertWebsiteConfigurationToXml(input.BucketWebsiteConfiguration, false)
	return
}

func (input GetBucketMetadataInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	headers = make(map[string][]string)
	if origin := strings.TrimSpace(input.Origin); origin != "" {
		headers[HEADER_ORIGIN_CAMEL] = []string{origin}
	}
	if requestHeader := strings.TrimSpace(input.RequestHeader); requestHeader != "" {
		headers[HEADER_ACCESS_CONTROL_REQUEST_HEADER_CAMEL] = []string{requestHeader}
	}
	return
}

func (input SetBucketLoggingConfigurationInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceLogging): ""}
	data, _ = ConvertLoggingStatusToXml(input.BucketLoggingStatus, false)
	return
}

func (input SetBucketLifecycleConfigurationInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceLifecycle): ""}
	data, md5 := ConvertLifecyleConfigurationToXml(input.BucketLifecyleConfiguration, true)
	headers = map[string][]string{HEADER_MD5_CAMEL: []string{md5}}
	return
}

func (input SetBucketTaggingInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceTagging): ""}
	data, md5, _ := ConvertRequestToIoReaderV2(input)
	headers = map[string][]string{HEADER_MD5_CAMEL: []string{md5}}
	return
}

func (input SetBucketNotificationInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceNotification): ""}
	data, _ = ConvertNotificationToXml(input.BucketNotification, false)
	return
}

func (input DeleteObjectInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = make(map[string]string)
	if input.VersionId != "" {
		params[PARAM_VERSION_ID] = input.VersionId
	}
	return
}

func (input DeleteObjectsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceDelete): ""}
	data, md5, _ := ConvertRequestToIoReaderV2(input)
	headers = map[string][]string{HEADER_MD5_CAMEL: []string{md5}}
	return
}

func (input SetObjectAclInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceAcl): ""}
	if input.VersionId != "" {
		params[PARAM_VERSION_ID] = input.VersionId
	}
	headers = make(map[string][]string)
	if acl := string(input.ACL); acl != "" {
		headers[HEADER_ACL_AMZ] = []string{acl}
	} else {
		data, _ = ConvertAclToXml(input.AccessControlPolicy, false)
	}
	return
}

func (input GetObjectAclInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceAcl): ""}
	if input.VersionId != "" {
		params[PARAM_VERSION_ID] = input.VersionId
	}
	return
}

func (input RestoreObjectInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{string(SubResourceRestore): ""}
	if input.VersionId != "" {
		params[PARAM_VERSION_ID] = input.VersionId
	}
	data, _ = ConvertRequestToIoReader(input)
	return
}

func (header SseKmsHeader) GetEncryption() string {
	if header.Encryption != "" {
		return header.Encryption
	}
	return DEFAULT_SSE_KMS_ENCRYPTION
}

func (header SseKmsHeader) GetKey() string {
	return header.Key
}

func (header SseCHeader) GetEncryption() string {
	if header.Encryption != "" {
		return header.Encryption
	}
	return DEFAULT_SSE_C_ENCRYPTION
}

func (header SseCHeader) GetKey() string {
	return header.Key
}

func (header SseCHeader) GetKeyMD5() string {
	if header.KeyMD5 != "" {
		return header.KeyMD5
	}

	if ret, err := Base64Decode(header.GetKey()); err == nil {
		return Base64Md5(ret)
	}
	return ""
}

func setSseHeader(headers map[string][]string, sseHeader ISseHeader, sseCOnly bool) {
	if sseHeader != nil {
		if sseCHeader, ok := sseHeader.(SseCHeader); ok {
			headers[HEADER_SSEC_ENCRYPTION_AMZ] = []string{sseCHeader.GetEncryption()}
			headers[HEADER_SSEC_KEY_AMZ] = []string{sseCHeader.GetKey()}
			headers[HEADER_SSEC_KEY_MD5_AMZ] = []string{sseCHeader.GetKeyMD5()}
		} else if sseKmsHeader, ok := sseHeader.(SseKmsHeader); !sseCOnly && ok {
			headers[HEADER_SSEKMS_ENCRYPTION_AMZ] = []string{sseKmsHeader.GetEncryption()}
			headers[HEADER_SSEKMS_KEY_AMZ] = []string{sseKmsHeader.GetKey()}
		}
	}
}

func (input GetObjectMetadataInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = make(map[string]string)
	if input.VersionId != "" {
		params[PARAM_VERSION_ID] = input.VersionId
	}
	headers = make(map[string][]string)

	if input.Origin != "" {
		headers[HEADER_ORIGIN_CAMEL] = []string{input.Origin}
	}

	if input.RequestHeader != "" {
		headers[HEADER_ACCESS_CONTROL_REQUEST_HEADER_CAMEL] = []string{input.RequestHeader}
	}
	setSseHeader(headers, input.SseHeader, true)
	return
}

func (input SetObjectMetadataInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = make(map[string]string)
	params = map[string]string{string(SubResourceMetadata): ""}
	if input.VersionId != "" {
		params[PARAM_VERSION_ID] = input.VersionId
	}
	headers = make(map[string][]string)

	if directive := string(input.MetadataDirective); directive != "" {
		headers[HEADER_METADATA_DIRECTIVE_AMZ] = []string{directive}
	}

	if input.CacheControl != "" {
		headers[HEADER_CACHE_CONTROL_CAMEL] = []string{input.CacheControl}
	}
	if input.ContentDisposition != "" {
		headers[HEADER_CONTENT_DISPOSITION_CAMEL] = []string{input.ContentDisposition}
	}
	if input.ContentEncoding != "" {
		headers[HEADER_CONTENT_ENCODING_CAMEL] = []string{input.ContentEncoding}
	}
	if input.ContentLanguage != "" {
		headers[HEADER_CONTENT_LANGUAGE_CAMEL] = []string{input.ContentLanguage}
	}

	if input.ContentType != "" {
		headers[HEADER_CONTENT_TYPE_CAML] = []string{input.ContentType}
	}
	if input.Expires != "" {
		headers[HEADER_EXPIRES_CAMEL] = []string{input.Expires}
	}
	if input.WebsiteRedirectLocation != "" {
		headers[HEADER_WEBSITE_REDIRECT_LOCATION_AMZ] = []string{input.WebsiteRedirectLocation}
	}
	if storageClass := string(input.StorageClass); storageClass != "" {
		headers[HEADER_STORAGE_CLASS2_AMZ] = []string{storageClass}
	}
	if input.Metadata != nil {
		for key, value := range input.Metadata {
			key = strings.TrimSpace(key)
			if !strings.HasPrefix(key, HEADER_PREFIX_META) {
				key = HEADER_PREFIX_META + key
			}
			headers[key] = []string{value}
		}
	}
	return
}

func (input GetObjectInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.GetObjectMetadataInput.trans()
	if input.ResponseCacheControl != "" {
		params[PARAM_RESPONSE_CACHE_CONTROL] = input.ResponseCacheControl
	}
	if input.ResponseContentDisposition != "" {
		params[PARAM_RESPONSE_CONTENT_DISPOSITION] = input.ResponseContentDisposition
	}
	if input.ResponseContentEncoding != "" {
		params[PARAM_RESPONSE_CONTENT_ENCODING] = input.ResponseContentEncoding
	}
	if input.ResponseContentLanguage != "" {
		params[PARAM_RESPONSE_CONTENT_LANGUAGE] = input.ResponseContentLanguage
	}
	if input.ResponseContentType != "" {
		params[PARAM_RESPONSE_CONTENT_TYPE] = input.ResponseContentType
	}
	if input.ResponseExpires != "" {
		params[PARAM_RESPONSE_EXPIRES] = input.ResponseExpires
	}
	if input.ImageProcess != "" {
		params[PARAM_IMAGE_PROCESS] = input.ImageProcess
	}
	if input.RangeStart >= 0 && input.RangeEnd > input.RangeStart {
		headers[HEADER_RANGE] = []string{fmt.Sprintf("bytes=%d-%d", input.RangeStart, input.RangeEnd)}
	}

	if input.IfMatch != "" {
		headers[HEADER_IF_MATCH] = []string{input.IfMatch}
	}
	if input.IfNoneMatch != "" {
		headers[HEADER_IF_NONE_MATCH] = []string{input.IfNoneMatch}
	}
	if !input.IfModifiedSince.IsZero() {
		headers[HEADER_IF_MODIFIED_SINCE] = []string{FormatUtcToRfc1123(input.IfModifiedSince)}
	}
	if !input.IfUnmodifiedSince.IsZero() {
		headers[HEADER_IF_UNMODIFIED_SINCE] = []string{FormatUtcToRfc1123(input.IfUnmodifiedSince)}
	}
	return
}

func (input ObjectOperationInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	headers = make(map[string][]string)
	params = make(map[string]string)
	if acl := string(input.ACL); acl != "" {
		headers[HEADER_ACL_AMZ] = []string{acl}
	}
	if storageClass := string(input.StorageClass); storageClass != "" {
		headers[HEADER_STORAGE_CLASS2_AMZ] = []string{storageClass}
	}
	if input.WebsiteRedirectLocation != "" {
		headers[HEADER_WEBSITE_REDIRECT_LOCATION_AMZ] = []string{input.WebsiteRedirectLocation}
	}
	setSseHeader(headers, input.SseHeader, false)
	if input.Metadata != nil {
		for key, value := range input.Metadata {
			key = strings.TrimSpace(key)
			if !strings.HasPrefix(key, HEADER_PREFIX_META) {
				key = HEADER_PREFIX_META + key
			}
			headers[key] = []string{value}
		}
	}
	return
}

func (input PutObjectBasicInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.ObjectOperationInput.trans()

	if input.ContentMD5 != "" {
		headers[HEADER_MD5_CAMEL] = []string{input.ContentMD5}
	}

	if input.ContentLength > 0 {
		headers[HEADER_CONTENT_LENGTH_CAMEL] = []string{Int64ToString(input.ContentLength)}
	}
	if input.ContentType != "" {
		headers[HEADER_CONTENT_TYPE_CAML] = []string{input.ContentType}
	}

	return
}

func (input PutObjectInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.PutObjectBasicInput.trans()
	if input.Body != nil {
		data = input.Body
	}
	return
}

func (input CopyObjectInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.ObjectOperationInput.trans()

	var copySource string
	if input.CopySourceVersionId != "" {
		copySource = fmt.Sprintf("%s/%s?versionId=%s", input.CopySourceBucket, UrlEncode(input.CopySourceKey, false), input.CopySourceVersionId)
	} else {
		copySource = fmt.Sprintf("%s/%s", input.CopySourceBucket, UrlEncode(input.CopySourceKey, false))
	}
	headers[HEADER_COPY_SOURCE_AMZ] = []string{copySource}

	if directive := string(input.MetadataDirective); directive != "" {
		headers[HEADER_METADATA_DIRECTIVE_AMZ] = []string{directive}
	}

	if input.MetadataDirective == ReplaceMetadata {
		if input.CacheControl != "" {
			headers[HEADER_CACHE_CONTROL] = []string{input.CacheControl}
		}
		if input.ContentDisposition != "" {
			headers[HEADER_CONTENT_DISPOSITION] = []string{input.ContentDisposition}
		}
		if input.ContentEncoding != "" {
			headers[HEADER_CONTENT_ENCODING] = []string{input.ContentEncoding}
		}
		if input.ContentLanguage != "" {
			headers[HEADER_CONTENT_LANGUAGE] = []string{input.ContentLanguage}
		}
		if input.ContentType != "" {
			headers[HEADER_CONTENT_TYPE] = []string{input.ContentType}
		}
		if input.Expires != "" {
			headers[HEADER_EXPIRES] = []string{input.Expires}
		}
	}

	if input.CopySourceIfMatch != "" {
		headers[HEADER_COPY_SOURCE_IF_MATCH_AMZ] = []string{input.CopySourceIfMatch}
	}
	if input.CopySourceIfNoneMatch != "" {
		headers[HEADER_COPY_SOURCE_IF_NONE_MATCH_AMZ] = []string{input.CopySourceIfNoneMatch}
	}
	if !input.CopySourceIfModifiedSince.IsZero() {
		headers[HEADER_COPY_SOURCE_IF_MODIFIED_SINCE_AMZ] = []string{FormatUtcToRfc1123(input.CopySourceIfModifiedSince)}
	}
	if !input.CopySourceIfUnmodifiedSince.IsZero() {
		headers[HEADER_COPY_SOURCE_IF_UNMODIFIED_SINCE_AMZ] = []string{FormatUtcToRfc1123(input.CopySourceIfUnmodifiedSince)}
	}
	if input.SourceSseHeader != nil {
		if sseCHeader, ok := input.SourceSseHeader.(SseCHeader); ok {
			headers[HEADER_SSEC_COPY_SOURCE_ENCRYPTION_AMZ] = []string{sseCHeader.GetEncryption()}
			headers[HEADER_SSEC_COPY_SOURCE_KEY_AMZ] = []string{sseCHeader.GetKey()}
			headers[HEADER_SSEC_COPY_SOURCE_KEY_MD5_AMZ] = []string{sseCHeader.GetKeyMD5()}
		}
	}
	return
}

func (input AbortMultipartUploadInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{"uploadId": input.UploadId}
	return
}

func (input InitiateMultipartUploadInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params, headers, data = input.ObjectOperationInput.trans()
	if input.ContentType != "" {
		headers[HEADER_CONTENT_TYPE_CAML] = []string{input.ContentType}
	}
	params[string(SubResourceUploads)] = ""
	return
}

func (input UploadPartInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{"uploadId": input.UploadId, "partNumber": IntToString(input.PartNumber)}
	headers = make(map[string][]string)

	if input.PartSize > 0 {
		headers[HEADER_CONTENT_LENGTH_CAMEL] = []string{Int64ToString(input.PartSize)}
	}

	setSseHeader(headers, input.SseHeader, true)
	if input.Body != nil {
		data = input.Body
	}
	return
}

func (input CompleteMultipartUploadInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{"uploadId": input.UploadId}
	data, _ = ConvertCompleteMultipartUploadInputToXml(input, false)
	return
}

func (input ListPartsInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{"uploadId": input.UploadId}
	if input.MaxParts > 0 {
		params["max-parts"] = IntToString(input.MaxParts)
	}
	if input.PartNumberMarker > 0 {
		params["part-number-marker"] = IntToString(input.PartNumberMarker)
	}
	return
}

func (input CopyPartInput) trans() (params map[string]string, headers map[string][]string, data interface{}) {
	params = map[string]string{"uploadId": input.UploadId, "partNumber": IntToString(input.PartNumber)}
	headers = make(map[string][]string, 1)
	var copySource string
	if input.CopySourceVersionId != "" {
		copySource = fmt.Sprintf("%s/%s?versionId=%s", input.CopySourceBucket, UrlEncode(input.CopySourceKey, false), input.CopySourceVersionId)
	} else {
		copySource = fmt.Sprintf("%s/%s", input.CopySourceBucket, UrlEncode(input.CopySourceKey, false))
	}
	headers[HEADER_COPY_SOURCE_AMZ] = []string{copySource}

	if input.CopySourceRangeStart >= 0 && input.CopySourceRangeEnd > input.CopySourceRangeStart {
		headers[HEADER_COPY_SOURCE_RANGE_AMZ] = []string{fmt.Sprintf("bytes=%d-%d", input.CopySourceRangeStart, input.CopySourceRangeEnd)}
	}

	setSseHeader(headers, input.SseHeader, true)
	if input.SourceSseHeader != nil {
		if sseCHeader, ok := input.SourceSseHeader.(SseCHeader); ok {
			headers[HEADER_SSEC_COPY_SOURCE_ENCRYPTION_AMZ] = []string{sseCHeader.GetEncryption()}
			headers[HEADER_SSEC_COPY_SOURCE_KEY_AMZ] = []string{sseCHeader.GetKey()}
			headers[HEADER_SSEC_COPY_SOURCE_KEY_MD5_AMZ] = []string{sseCHeader.GetKeyMD5()}
		}
	}
	return
}

type partSlice []Part

func (parts partSlice) Len() int {
	return len(parts)
}

func (parts partSlice) Less(i, j int) bool {
	return parts[i].PartNumber < parts[j].PartNumber
}

func (parts partSlice) Swap(i, j int) {
	parts[i], parts[j] = parts[j], parts[i]
}

type readerWrapper struct {
	reader      io.Reader
	mark        int64
	totalCount  int64
	readedCount int64
}

func (rw *readerWrapper) seek(offset int64, whence int) (int64, error) {
	if r, ok := rw.reader.(*strings.Reader); ok {
		return r.Seek(offset, whence)
	} else if r, ok := rw.reader.(*bytes.Reader); ok {
		return r.Seek(offset, whence)
	} else if r, ok := rw.reader.(*os.File); ok {
		return r.Seek(offset, whence)
	}
	return offset, nil
}

func (rw *readerWrapper) Read(p []byte) (n int, err error) {
	if rw.totalCount == 0 {
		return 0, io.EOF
	}
	if rw.totalCount > 0 {
		n, err = rw.reader.Read(p)
		readedOnce := int64(n)
		if remainCount := rw.totalCount - rw.readedCount; remainCount > readedOnce {
			rw.readedCount += readedOnce
			return n, err
		} else {
			rw.readedCount += remainCount
			return int(remainCount), io.EOF
		}
	}
	return rw.reader.Read(p)
}

type fileReaderWrapper struct {
	readerWrapper
	filePath string
}
