package obs

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

type ObsClient struct {
	conf       *config
	httpClient *http.Client
	transport  *http.Transport
}

func New(ak, sk, endpoint string, configurers ...configurer) (*ObsClient, error) {
	conf := &config{securityProvider: &securityProvider{ak: ak, sk: sk}, endpoint: endpoint}
	conf.maxRetryCount = -1
	for _, configurer := range configurers {
		configurer(conf)
	}

	if err := conf.initConfigWithDefault(); err != nil {
		return nil, err
	}

	transport, err := conf.getTransport()
	if err != nil {
		return nil, err
	}

	if isWarnLogEnabled() {
		info := make([]string, 3)
		info[0] = fmt.Sprintf("[OBS SDK Version=%s", OBS_SDK_VERSION)
		info[1] = fmt.Sprintf("Endpoint=%s", conf.endpoint)
		accessMode := "Virtual Hosting"
		if conf.pathStyle {
			accessMode = "Path"
		}
		info[2] = fmt.Sprintf("Access Mode=%s]", accessMode)
		doLog(LEVEL_WARN, strings.Join(info, "];["))
	}
	doLog(LEVEL_DEBUG, "Create obsclient with config:\n%s\n", conf)
	obsClient := &ObsClient{conf: conf, httpClient: &http.Client{Transport: transport, CheckRedirect: checkRedirectFunc}, transport: transport}
	return obsClient, nil
}

func (obsClient ObsClient) Refresh(ak, sk, securityToken string) {
	sp := &securityProvider{ak: strings.TrimSpace(ak), sk: strings.TrimSpace(sk), securityToken: strings.TrimSpace(securityToken)}
	obsClient.conf.securityProvider = sp
}

func (obsClient ObsClient) Close() {
	obsClient.transport.CloseIdleConnections()
	obsClient.transport = nil
	obsClient.httpClient = nil
	obsClient.conf = nil
	SyncLog()
}

func (obsClient ObsClient) ListBuckets(input *ListBucketsInput) (output *ListBucketsOutput, err error) {
	if input == nil {
		input = &ListBucketsInput{}
	}
	output = &ListBucketsOutput{}
	err = obsClient.doActionWithoutBucket("ListBuckets", HTTP_GET, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) CreateBucket(input *CreateBucketInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("CreateBucketInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("CreateBucket", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteBucket(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("DeleteBucket", HTTP_DELETE, bucketName, defaultSerializable, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketStoragePolicy(input *SetBucketStoragePolicyInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketStoragePolicyInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketStoragePolicy", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketStoragePolicy(bucketName string) (output *GetBucketStoragePolicyOutput, err error) {
	output = &GetBucketStoragePolicyOutput{}
	err = obsClient.doActionWithBucket("GetBucketStoragePolicy", HTTP_GET, bucketName, newSubResourceSerial(SubResourceStoragePolicy), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) ListObjects(input *ListObjectsInput) (output *ListObjectsOutput, err error) {
	if input == nil {
		return nil, errors.New("ListObjectsInput is nil")
	}
	output = &ListObjectsOutput{}
	err = obsClient.doActionWithBucket("ListObjects", HTTP_GET, input.Bucket, input, output)
	if err != nil {
		output = nil
	} else {
		if location, ok := output.ResponseHeaders[HEADER_BUCKET_REGION]; ok {
			output.Location = location[0]
		}
	}
	return
}

func (obsClient ObsClient) ListVersions(input *ListVersionsInput) (output *ListVersionsOutput, err error) {
	if input == nil {
		return nil, errors.New("ListVersionsInput is nil")
	}
	output = &ListVersionsOutput{}
	err = obsClient.doActionWithBucket("ListVersions", HTTP_GET, input.Bucket, input, output)
	if err != nil {
		output = nil
	} else {
		if location, ok := output.ResponseHeaders[HEADER_BUCKET_REGION]; ok {
			output.Location = location[0]
		}
	}
	return
}

func (obsClient ObsClient) ListMultipartUploads(input *ListMultipartUploadsInput) (output *ListMultipartUploadsOutput, err error) {
	if input == nil {
		return nil, errors.New("ListMultipartUploadsInput is nil")
	}
	output = &ListMultipartUploadsOutput{}
	err = obsClient.doActionWithBucket("ListMultipartUploads", HTTP_GET, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketQuota(input *SetBucketQuotaInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketQuotaInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketQuota", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketQuota(bucketName string) (output *GetBucketQuotaOutput, err error) {
	output = &GetBucketQuotaOutput{}
	err = obsClient.doActionWithBucket("GetBucketQuota", HTTP_GET, bucketName, newSubResourceSerial(SubResourceQuota), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) HeadBucket(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("HeadBucket", HTTP_HEAD, bucketName, defaultSerializable, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketMetadata(input *GetBucketMetadataInput) (output *GetBucketMetadataOutput, err error) {
	output = &GetBucketMetadataOutput{}
	err = obsClient.doActionWithBucket("GetBucketMetadata", HTTP_HEAD, input.Bucket, input, output)
	if err != nil {
		output = nil
	} else {
		ParseGetBucketMetadataOutput(output)
	}
	return
}

func (obsClient ObsClient) GetBucketStorageInfo(bucketName string) (output *GetBucketStorageInfoOutput, err error) {
	output = &GetBucketStorageInfoOutput{}
	err = obsClient.doActionWithBucket("GetBucketStorageInfo", HTTP_GET, bucketName, newSubResourceSerial(SubResourceStorageInfo), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketLocation(bucketName string) (output *GetBucketLocationOutput, err error) {
	output = &GetBucketLocationOutput{}
	err = obsClient.doActionWithBucket("GetBucketLocation", HTTP_GET, bucketName, newSubResourceSerial(SubResourceLocation), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketAcl(input *SetBucketAclInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketAclInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketAcl", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketAcl(bucketName string) (output *GetBucketAclOutput, err error) {
	output = &GetBucketAclOutput{}
	err = obsClient.doActionWithBucket("GetBucketAcl", HTTP_GET, bucketName, newSubResourceSerial(SubResourceAcl), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketPolicy(input *SetBucketPolicyInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketPolicy is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketPolicy", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketPolicy(bucketName string) (output *GetBucketPolicyOutput, err error) {
	output = &GetBucketPolicyOutput{}
	err = obsClient.doActionWithBucketV2("GetBucketPolicy", HTTP_GET, bucketName, newSubResourceSerial(SubResourcePolicy), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteBucketPolicy(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("DeleteBucketPolicy", HTTP_DELETE, bucketName, newSubResourceSerial(SubResourcePolicy), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketCors(input *SetBucketCorsInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketCorsInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketCors", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketCors(bucketName string) (output *GetBucketCorsOutput, err error) {
	output = &GetBucketCorsOutput{}
	err = obsClient.doActionWithBucket("GetBucketCors", HTTP_GET, bucketName, newSubResourceSerial(SubResourceCors), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteBucketCors(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("DeleteBucketCors", HTTP_DELETE, bucketName, newSubResourceSerial(SubResourceCors), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketVersioning(input *SetBucketVersioningInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketVersioningInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketVersioning", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketVersioning(bucketName string) (output *GetBucketVersioningOutput, err error) {
	output = &GetBucketVersioningOutput{}
	err = obsClient.doActionWithBucket("GetBucketVersioning", HTTP_GET, bucketName, newSubResourceSerial(SubResourceVersioning), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketWebsiteConfiguration(input *SetBucketWebsiteConfigurationInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketWebsiteConfigurationInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketWebsiteConfiguration", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketWebsiteConfiguration(bucketName string) (output *GetBucketWebsiteConfigurationOutput, err error) {
	output = &GetBucketWebsiteConfigurationOutput{}
	err = obsClient.doActionWithBucket("GetBucketWebsiteConfiguration", HTTP_GET, bucketName, newSubResourceSerial(SubResourceWebsite), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteBucketWebsiteConfiguration(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("DeleteBucketWebsiteConfiguration", HTTP_DELETE, bucketName, newSubResourceSerial(SubResourceWebsite), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketLoggingConfiguration(input *SetBucketLoggingConfigurationInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketLoggingConfigurationInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketLoggingConfiguration", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketLoggingConfiguration(bucketName string) (output *GetBucketLoggingConfigurationOutput, err error) {
	output = &GetBucketLoggingConfigurationOutput{}
	err = obsClient.doActionWithBucket("GetBucketLoggingConfiguration", HTTP_GET, bucketName, newSubResourceSerial(SubResourceLogging), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketLifecycleConfiguration(input *SetBucketLifecycleConfigurationInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketLifecycleConfigurationInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketLifecycleConfiguration", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketLifecycleConfiguration(bucketName string) (output *GetBucketLifecycleConfigurationOutput, err error) {
	output = &GetBucketLifecycleConfigurationOutput{}
	err = obsClient.doActionWithBucket("GetBucketLifecycleConfiguration", HTTP_GET, bucketName, newSubResourceSerial(SubResourceLifecycle), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteBucketLifecycleConfiguration(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("DeleteBucketLifecycleConfiguration", HTTP_DELETE, bucketName, newSubResourceSerial(SubResourceLifecycle), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketTagging(input *SetBucketTaggingInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketTaggingInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketTagging", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketTagging(bucketName string) (output *GetBucketTaggingOutput, err error) {
	output = &GetBucketTaggingOutput{}
	err = obsClient.doActionWithBucket("GetBucketTagging", HTTP_GET, bucketName, newSubResourceSerial(SubResourceTagging), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteBucketTagging(bucketName string) (output *BaseModel, err error) {
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("DeleteBucketTagging", HTTP_DELETE, bucketName, newSubResourceSerial(SubResourceTagging), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetBucketNotification(input *SetBucketNotificationInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetBucketNotificationInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucket("SetBucketNotification", HTTP_PUT, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetBucketNotification(bucketName string) (output *GetBucketNotificationOutput, err error) {
	output = &GetBucketNotificationOutput{}
	err = obsClient.doActionWithBucket("GetBucketNotification", HTTP_GET, bucketName, newSubResourceSerial(SubResourceNotification), output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) DeleteObject(input *DeleteObjectInput) (output *DeleteObjectOutput, err error) {
	if input == nil {
		return nil, errors.New("DeleteObjectInput is nil")
	}
	output = &DeleteObjectOutput{}
	err = obsClient.doActionWithBucketAndKey("DeleteObject", HTTP_DELETE, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseDeleteObjectOutput(output)
	}
	return
}

func (obsClient ObsClient) DeleteObjects(input *DeleteObjectsInput) (output *DeleteObjectsOutput, err error) {
	if input == nil {
		return nil, errors.New("DeleteObjectsInput is nil")
	}
	output = &DeleteObjectsOutput{}
	err = obsClient.doActionWithBucket("DeleteObjects", HTTP_POST, input.Bucket, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) SetObjectAcl(input *SetObjectAclInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("SetObjectAclInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucketAndKey("SetObjectAcl", HTTP_PUT, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetObjectAcl(input *GetObjectAclInput) (output *GetObjectAclOutput, err error) {
	if input == nil {
		return nil, errors.New("GetObjectAclInput is nil")
	}
	output = &GetObjectAclOutput{}
	err = obsClient.doActionWithBucketAndKey("GetObjectAcl", HTTP_GET, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		if versionId, ok := output.ResponseHeaders[HEADER_VERSION_ID]; ok {
			output.VersionId = versionId[0]
		}
	}
	return
}

func (obsClient ObsClient) RestoreObject(input *RestoreObjectInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("RestoreObjectInput is nil")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucketAndKey("RestoreObject", HTTP_POST, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) GetObjectMetadata(input *GetObjectMetadataInput) (output *GetObjectMetadataOutput, err error) {
	if input == nil {
		return nil, errors.New("GetObjectMetadataInput is nil")
	}
	output = &GetObjectMetadataOutput{}
	err = obsClient.doActionWithBucketAndKey("GetObjectMetadata", HTTP_HEAD, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseGetObjectMetadataOutput(output)
	}
	return
}

func (obsClient ObsClient) SetObjectMetadata(input *SetObjectMetadataInput) (output *SetObjectMetadataOutput, err error) {
	output = &SetObjectMetadataOutput{}
	err = obsClient.doActionWithBucketAndKey("SetObjectMetadata", HTTP_PUT, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseSetObjectMetadataOutput(output)
	}
	return
}

func (obsClient ObsClient) GetObject(input *GetObjectInput) (output *GetObjectOutput, err error) {
	if input == nil {
		return nil, errors.New("GetObjectInput is nil")
	}
	output = &GetObjectOutput{}
	err = obsClient.doActionWithBucketAndKey("GetObject", HTTP_GET, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseGetObjectOutput(output)
	}
	return
}

func (obsClient ObsClient) PutObject(input *PutObjectInput) (output *PutObjectOutput, err error) {
	if input == nil {
		return nil, errors.New("PutObjectInput is nil")
	}

	if input.ContentType == "" && input.Key != "" {
		if contentType, ok := mimeTypes[input.Key[strings.LastIndex(input.Key, ".")+1:]]; ok {
			input.ContentType = contentType
		}
	}

	output = &PutObjectOutput{}
	var repeatable bool
	if input.Body != nil {
		_, repeatable = input.Body.(*strings.Reader)
		if input.ContentLength > 0 {
			input.Body = &readerWrapper{reader: input.Body, totalCount: input.ContentLength}
		}
	}
	if repeatable {
		err = obsClient.doActionWithBucketAndKey("PutObject", HTTP_PUT, input.Bucket, input.Key, input, output)
	} else {
		err = obsClient.doActionWithBucketAndKeyUnRepeatable("PutObject", HTTP_PUT, input.Bucket, input.Key, input, output)
	}
	if err != nil {
		output = nil
	} else {
		ParsePutObjectOutput(output)
	}
	return
}

func (obsClient ObsClient) PutFile(input *PutFileInput) (output *PutObjectOutput, err error) {
	if input == nil {
		return nil, errors.New("PutFileInput is nil")
	}

	var body io.Reader
	sourceFile := strings.TrimSpace(input.SourceFile)
	if sourceFile != "" {
		fd, err := os.Open(sourceFile)
		if err != nil {
			return nil, err
		}
		defer fd.Close()

		stat, err := fd.Stat()
		if err != nil {
			return nil, err
		}
		fileReaderWrapper := &fileReaderWrapper{filePath: sourceFile}
		fileReaderWrapper.reader = fd
		if input.ContentLength > 0 {
			if input.ContentLength > stat.Size() {
				input.ContentLength = stat.Size()
			}
			fileReaderWrapper.totalCount = input.ContentLength
		} else {
			fileReaderWrapper.totalCount = stat.Size()
		}
		body = fileReaderWrapper
	}

	_input := &PutObjectInput{}
	_input.PutObjectBasicInput = input.PutObjectBasicInput
	_input.Body = body

	if _input.ContentType == "" && _input.Key != "" {
		if contentType, ok := mimeTypes[_input.Key[strings.LastIndex(_input.Key, ".")+1:]]; ok {
			_input.ContentType = contentType
		} else if contentType, ok := mimeTypes[sourceFile[strings.LastIndex(sourceFile, ".")+1:]]; ok {
			_input.ContentType = contentType
		}
	}

	output = &PutObjectOutput{}
	err = obsClient.doActionWithBucketAndKey("PutFile", HTTP_PUT, _input.Bucket, _input.Key, _input, output)
	if err != nil {
		output = nil
	} else {
		ParsePutObjectOutput(output)
	}
	return
}

func (obsClient ObsClient) CopyObject(input *CopyObjectInput) (output *CopyObjectOutput, err error) {
	if input == nil {
		return nil, errors.New("CopyObjectInput is nil")
	}

	if strings.TrimSpace(input.CopySourceBucket) == "" {
		return nil, errors.New("Source bucket is empty")
	}
	if strings.TrimSpace(input.CopySourceKey) == "" {
		return nil, errors.New("Source key is empty")
	}

	output = &CopyObjectOutput{}
	err = obsClient.doActionWithBucketAndKey("CopyObject", HTTP_PUT, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseCopyObjectOutput(output)
	}
	return
}

func (obsClient ObsClient) AbortMultipartUpload(input *AbortMultipartUploadInput) (output *BaseModel, err error) {
	if input == nil {
		return nil, errors.New("AbortMultipartUploadInput is nil")
	}
	if input.UploadId == "" {
		return nil, errors.New("UploadId is empty")
	}
	output = &BaseModel{}
	err = obsClient.doActionWithBucketAndKey("AbortMultipartUpload", HTTP_DELETE, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) InitiateMultipartUpload(input *InitiateMultipartUploadInput) (output *InitiateMultipartUploadOutput, err error) {
	if input == nil {
		return nil, errors.New("InitiateMultipartUploadInput is nil")
	}

	if input.ContentType == "" && input.Key != "" {
		if contentType, ok := mimeTypes[input.Key[strings.LastIndex(input.Key, ".")+1:]]; ok {
			input.ContentType = contentType
		}
	}

	output = &InitiateMultipartUploadOutput{}
	err = obsClient.doActionWithBucketAndKey("InitiateMultipartUpload", HTTP_POST, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseInitiateMultipartUploadOutput(output)
	}
	return
}

func (obsClient ObsClient) UploadPart(input *UploadPartInput) (output *UploadPartOutput, err error) {
	if input == nil {
		return nil, errors.New("UploadPartInput is nil")
	}

	if input.UploadId == "" {
		return nil, errors.New("UploadId is empty")
	}

	output = &UploadPartOutput{}
	var repeatable bool
	if input.Body != nil {
		_, repeatable = input.Body.(*strings.Reader)
		if _, ok := input.Body.(*readerWrapper); !ok && input.PartSize > 0 {
			input.Body = &readerWrapper{reader: input.Body, totalCount: input.PartSize}
		}
	} else if sourceFile := strings.TrimSpace(input.SourceFile); sourceFile != "" {
		fd, err := os.Open(sourceFile)
		if err != nil {
			return nil, err
		}
		defer fd.Close()

		stat, err := fd.Stat()
		if err != nil {
			return nil, err
		}
		fileSize := stat.Size()
		fileReaderWrapper := &fileReaderWrapper{filePath: sourceFile}
		fileReaderWrapper.reader = fd

		if input.Offset < 0 || input.Offset > fileSize {
			input.Offset = 0
		}

		if input.PartSize <= 0 || input.PartSize > (fileSize-input.Offset) {
			input.PartSize = fileSize - input.Offset
		}
		fileReaderWrapper.totalCount = input.PartSize
		if _, err := fd.Seek(input.Offset, 0); err != nil {
			return nil, err
		}

		input.Body = fileReaderWrapper
		repeatable = true
	}
	if repeatable {
		err = obsClient.doActionWithBucketAndKey("UploadPart", HTTP_PUT, input.Bucket, input.Key, input, output)
	} else {
		err = obsClient.doActionWithBucketAndKeyUnRepeatable("UploadPart", HTTP_PUT, input.Bucket, input.Key, input, output)
	}
	if err != nil {
		output = nil
	} else {
		ParseUploadPartOutput(output)
		output.PartNumber = input.PartNumber
	}
	return
}

func (obsClient ObsClient) CompleteMultipartUpload(input *CompleteMultipartUploadInput) (output *CompleteMultipartUploadOutput, err error) {
	if input == nil {
		return nil, errors.New("CompleteMultipartUploadInput is nil")
	}

	if input.UploadId == "" {
		return nil, errors.New("UploadId is empty")
	}

	var parts partSlice = input.Parts
	sort.Sort(parts)

	output = &CompleteMultipartUploadOutput{}
	err = obsClient.doActionWithBucketAndKey("CompleteMultipartUpload", HTTP_POST, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseCompleteMultipartUploadOutput(output)
	}
	return
}

func (obsClient ObsClient) ListParts(input *ListPartsInput) (output *ListPartsOutput, err error) {
	if input == nil {
		return nil, errors.New("ListPartsInput is nil")
	}
	if input.UploadId == "" {
		return nil, errors.New("UploadId is empty")
	}
	output = &ListPartsOutput{}
	err = obsClient.doActionWithBucketAndKey("ListParts", HTTP_GET, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	}
	return
}

func (obsClient ObsClient) CopyPart(input *CopyPartInput) (output *CopyPartOutput, err error) {
	if input == nil {
		return nil, errors.New("CopyPartInput is nil")
	}
	if input.UploadId == "" {
		return nil, errors.New("UploadId is empty")
	}
	if strings.TrimSpace(input.CopySourceBucket) == "" {
		return nil, errors.New("Source bucket is empty")
	}
	if strings.TrimSpace(input.CopySourceKey) == "" {
		return nil, errors.New("Source key is empty")
	}

	output = &CopyPartOutput{}
	err = obsClient.doActionWithBucketAndKey("CopyPart", HTTP_PUT, input.Bucket, input.Key, input, output)
	if err != nil {
		output = nil
	} else {
		ParseCopyPartOutput(output)
		output.PartNumber = input.PartNumber
	}
	return
}
