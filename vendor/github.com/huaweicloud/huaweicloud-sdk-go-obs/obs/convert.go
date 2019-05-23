package obs

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"time"
)

func cleanHeaderPrefix(header http.Header) map[string][]string {
	responseHeaders := make(map[string][]string)
	for key, value := range header {
		if len(value) > 0 {
			key = strings.ToLower(key)
			if strings.HasPrefix(key, HEADER_PREFIX) {
				key = key[len(HEADER_PREFIX):]
			}
			responseHeaders[key] = value
		}
	}
	return responseHeaders
}

func ParseStringToStorageClassType(value string) (ret StorageClassType) {
	switch value {
	case "STANDARD":
		ret = StorageClassStandard
	case "STANDARD_IA":
		ret = StorageClassWarm
	case "GLACIER":
		ret = StorageClassCold
	default:
		ret = ""
	}
	return
}

func convertGrantToXml(grant Grant) string {
	xml := make([]string, 0, 4)
	xml = append(xml, fmt.Sprintf("<Grant><Grantee xsi:type=\"%s\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">", grant.Grantee.Type))
	if grant.Grantee.Type == GranteeUser {
		xml = append(xml, fmt.Sprintf("<ID>%s</ID>", grant.Grantee.ID))
		if grant.Grantee.DisplayName != "" {
			xml = append(xml, fmt.Sprintf("<DisplayName>%s</DisplayName>", grant.Grantee.DisplayName))
		}
	} else {
		xml = append(xml, fmt.Sprintf("<URI>%s</URI>", grant.Grantee.URI))
	}
	xml = append(xml, fmt.Sprintf("</Grantee><Permission>%s</Permission></Grant>", grant.Permission))
	return strings.Join(xml, "")
}

func ConvertLoggingStatusToXml(input BucketLoggingStatus, returnMd5 bool) (data string, md5 string) {
	grantsLength := len(input.TargetGrants)
	xml := make([]string, 0, 8+grantsLength)

	xml = append(xml, "<BucketLoggingStatus>")
	if input.TargetBucket != "" || input.TargetPrefix != "" {
		xml = append(xml, "<LoggingEnabled>")
		xml = append(xml, fmt.Sprintf("<TargetBucket>%s</TargetBucket>", input.TargetBucket))
		xml = append(xml, fmt.Sprintf("<TargetPrefix>%s</TargetPrefix>", input.TargetPrefix))

		if grantsLength > 0 {
			xml = append(xml, "<TargetGrants>")
			for _, grant := range input.TargetGrants {
				xml = append(xml, convertGrantToXml(grant))
			}
			xml = append(xml, "</TargetGrants>")
		}

		xml = append(xml, "</LoggingEnabled>")
	}
	xml = append(xml, "</BucketLoggingStatus>")
	data = strings.Join(xml, "")
	if returnMd5 {
		md5 = Base64Md5([]byte(data))
	}
	return
}

func ConvertAclToXml(input AccessControlPolicy, returnMd5 bool) (data string, md5 string) {
	xml := make([]string, 0, 4+len(input.Grants))
	xml = append(xml, fmt.Sprintf("<AccessControlPolicy><Owner><ID>%s</ID>", input.Owner.ID))
	if input.Owner.DisplayName != "" {
		xml = append(xml, fmt.Sprintf("<DisplayName>%s</DisplayName>", input.Owner.DisplayName))
	}
	xml = append(xml, "</Owner><AccessControlList>")
	for _, grant := range input.Grants {
		xml = append(xml, convertGrantToXml(grant))
	}
	xml = append(xml, "</AccessControlList></AccessControlPolicy>")
	data = strings.Join(xml, "")
	if returnMd5 {
		md5 = Base64Md5([]byte(data))
	}
	return
}

func convertConditionToXml(condition Condition) string {
	xml := make([]string, 0, 2)
	if condition.KeyPrefixEquals != "" {
		xml = append(xml, fmt.Sprintf("<KeyPrefixEquals>%s</KeyPrefixEquals>", condition.KeyPrefixEquals))
	}
	if condition.HttpErrorCodeReturnedEquals != "" {
		xml = append(xml, fmt.Sprintf("<HttpErrorCodeReturnedEquals>%s</HttpErrorCodeReturnedEquals>", condition.HttpErrorCodeReturnedEquals))
	}
	if len(xml) > 0 {
		return fmt.Sprintf("<Condition>%s</Condition>", strings.Join(xml, ""))
	}
	return ""
}

func ConvertWebsiteConfigurationToXml(input BucketWebsiteConfiguration, returnMd5 bool) (data string, md5 string) {
	routingRuleLength := len(input.RoutingRules)
	xml := make([]string, 0, 6+routingRuleLength*10)
	xml = append(xml, "<WebsiteConfiguration>")

	if input.RedirectAllRequestsTo.HostName != "" {
		xml = append(xml, fmt.Sprintf("<RedirectAllRequestsTo><HostName>%s</HostName>", input.RedirectAllRequestsTo.HostName))
		if input.RedirectAllRequestsTo.Protocol != "" {
			xml = append(xml, fmt.Sprintf("<Protocol>%s</Protocol>", input.RedirectAllRequestsTo.Protocol))
		}
		xml = append(xml, "</RedirectAllRequestsTo>")
	} else {
		xml = append(xml, fmt.Sprintf("<IndexDocument><Suffix>%s</Suffix></IndexDocument>", input.IndexDocument.Suffix))
		if input.ErrorDocument.Key != "" {
			xml = append(xml, fmt.Sprintf("<ErrorDocument><Key>%s</Key></ErrorDocument>", input.ErrorDocument.Key))
		}
		if routingRuleLength > 0 {
			xml = append(xml, "<RoutingRules>")
			for _, routingRule := range input.RoutingRules {
				xml = append(xml, "<RoutingRule>")
				xml = append(xml, "<Redirect>")
				if routingRule.Redirect.Protocol != "" {
					xml = append(xml, fmt.Sprintf("<Protocol>%s</Protocol>", routingRule.Redirect.Protocol))
				}
				if routingRule.Redirect.HostName != "" {
					xml = append(xml, fmt.Sprintf("<HostName>%s</HostName>", routingRule.Redirect.HostName))
				}
				if routingRule.Redirect.ReplaceKeyPrefixWith != "" {
					xml = append(xml, fmt.Sprintf("<ReplaceKeyPrefixWith>%s</ReplaceKeyPrefixWith>", routingRule.Redirect.ReplaceKeyPrefixWith))
				}

				if routingRule.Redirect.ReplaceKeyWith != "" {
					xml = append(xml, fmt.Sprintf("<ReplaceKeyWith>%s</ReplaceKeyWith>", routingRule.Redirect.ReplaceKeyWith))
				}
				if routingRule.Redirect.HttpRedirectCode != "" {
					xml = append(xml, fmt.Sprintf("<HttpRedirectCode>%s</HttpRedirectCode>", routingRule.Redirect.HttpRedirectCode))
				}
				xml = append(xml, "</Redirect>")

				if ret := convertConditionToXml(routingRule.Condition); ret != "" {
					xml = append(xml, ret)
				}
				xml = append(xml, "</RoutingRule>")
			}
			xml = append(xml, "</RoutingRules>")
		}
	}

	xml = append(xml, "</WebsiteConfiguration>")
	data = strings.Join(xml, "")
	if returnMd5 {
		md5 = Base64Md5([]byte(data))
	}
	return
}

func convertTransitionsToXml(transitions []Transition) string {
	if length := len(transitions); length > 0 {
		xml := make([]string, 0, length)
		for _, transition := range transitions {
			var temp string
			if transition.Days > 0 {
				temp = fmt.Sprintf("<Days>%d</Days>", transition.Days)
			} else if !transition.Date.IsZero() {
				temp = fmt.Sprintf("<Date>%s</Date>", transition.Date.UTC().Format(ISO8601_MIDNIGHT_DATE_FORMAT))
			}
			if temp != "" {
				xml = append(xml, fmt.Sprintf("<Transition>%s<StorageClass>%s</StorageClass></Transition>", temp, transition.StorageClass))
			}
		}
		return strings.Join(xml, "")
	}
	return ""
}

func convertExpirationToXml(expiration Expiration) string {
	if expiration.Days > 0 {
		return fmt.Sprintf("<Expiration><Days>%d</Days></Expiration>", expiration.Days)
	} else if !expiration.Date.IsZero() {
		return fmt.Sprintf("<Expiration><Date>%s</Date></Expiration>", expiration.Date.UTC().Format(ISO8601_MIDNIGHT_DATE_FORMAT))
	}
	return ""
}
func convertNoncurrentVersionTransitionsToXml(noncurrentVersionTransitions []NoncurrentVersionTransition) string {
	if length := len(noncurrentVersionTransitions); length > 0 {
		xml := make([]string, 0, length)
		for _, noncurrentVersionTransition := range noncurrentVersionTransitions {
			if noncurrentVersionTransition.NoncurrentDays > 0 {
				xml = append(xml, fmt.Sprintf("<NoncurrentVersionTransition><NoncurrentDays>%d</NoncurrentDays>"+
					"<StorageClass>%s</StorageClass></NoncurrentVersionTransition>",
					noncurrentVersionTransition.NoncurrentDays, noncurrentVersionTransition.StorageClass))
			}
		}
		return strings.Join(xml, "")
	}
	return ""
}
func convertNoncurrentVersionExpirationToXml(noncurrentVersionExpiration NoncurrentVersionExpiration) string {
	if noncurrentVersionExpiration.NoncurrentDays > 0 {
		return fmt.Sprintf("<NoncurrentVersionExpiration><NoncurrentDays>%d</NoncurrentDays></NoncurrentVersionExpiration>", noncurrentVersionExpiration.NoncurrentDays)
	}
	return ""
}

func ConvertLifecyleConfigurationToXml(input BucketLifecyleConfiguration, returnMd5 bool) (data string, md5 string) {
	xml := make([]string, 0, 2+len(input.LifecycleRules)*9)
	xml = append(xml, "<LifecycleConfiguration>")
	for _, lifecyleRule := range input.LifecycleRules {
		xml = append(xml, "<Rule>")
		if lifecyleRule.ID != "" {
			xml = append(xml, fmt.Sprintf("<ID>%s</ID>", lifecyleRule.ID))
		}
		xml = append(xml, fmt.Sprintf("<Prefix>%s</Prefix>", lifecyleRule.Prefix))
		xml = append(xml, fmt.Sprintf("<Status>%s</Status>", lifecyleRule.Status))
		if ret := convertTransitionsToXml(lifecyleRule.Transitions); ret != "" {
			xml = append(xml, ret)
		}
		if ret := convertExpirationToXml(lifecyleRule.Expiration); ret != "" {
			xml = append(xml, ret)
		}
		if ret := convertNoncurrentVersionTransitionsToXml(lifecyleRule.NoncurrentVersionTransitions); ret != "" {
			xml = append(xml, ret)
		}
		if ret := convertNoncurrentVersionExpirationToXml(lifecyleRule.NoncurrentVersionExpiration); ret != "" {
			xml = append(xml, ret)
		}
		xml = append(xml, "</Rule>")
	}
	xml = append(xml, "</LifecycleConfiguration>")
	data = strings.Join(xml, "")
	if returnMd5 {
		md5 = Base64Md5([]byte(data))
	}
	return
}

func converntFilterRulesToXml(filterRules []FilterRule) string {
	if length := len(filterRules); length > 0 {
		xml := make([]string, 0, length*4)
		for _, filterRule := range filterRules {
			xml = append(xml, "<FilterRule>")
			if filterRule.Name != "" {
				xml = append(xml, fmt.Sprintf("<Name>%s</Name>", filterRule.Name))
			}
			if filterRule.Value != "" {
				xml = append(xml, fmt.Sprintf("<Value>%s</Value>", filterRule.Value))
			}
			xml = append(xml, "</FilterRule>")
		}
		return fmt.Sprintf("<Filter><S3Key>%s</S3Key></Filter>", strings.Join(xml, ""))
	}
	return ""
}

func converntEventsToXml(events []string) string {
	if length := len(events); length > 0 {
		xml := make([]string, 0, length)
		for _, event := range events {
			xml = append(xml, fmt.Sprintf("<Event>%s</Event>", event))
		}
		return strings.Join(xml, "")
	}
	return ""
}

func ConvertNotificationToXml(input BucketNotification, returnMd5 bool) (data string, md5 string) {
	xml := make([]string, 0, 2+len(input.TopicConfigurations)*6)
	xml = append(xml, "<NotificationConfiguration>")
	for _, topicConfiguration := range input.TopicConfigurations {
		xml = append(xml, "<TopicConfiguration>")
		if topicConfiguration.ID != "" {
			xml = append(xml, fmt.Sprintf("<Id>%s</Id>", topicConfiguration.ID))
		}
		xml = append(xml, fmt.Sprintf("<Topic>%s</Topic>", topicConfiguration.Topic))

		if ret := converntEventsToXml(topicConfiguration.Events); ret != "" {
			xml = append(xml, ret)
		}
		if ret := converntFilterRulesToXml(topicConfiguration.FilterRules); ret != "" {
			xml = append(xml, ret)
		}
		xml = append(xml, "</TopicConfiguration>")
	}
	xml = append(xml, "</NotificationConfiguration>")
	data = strings.Join(xml, "")
	if returnMd5 {
		md5 = Base64Md5([]byte(data))
	}
	return
}

func ConvertCompleteMultipartUploadInputToXml(input CompleteMultipartUploadInput, returnMd5 bool) (data string, md5 string) {
	xml := make([]string, 0, 2+len(input.Parts)*4)
	xml = append(xml, "<CompleteMultipartUpload>")
	for _, part := range input.Parts {
		xml = append(xml, "<Part>")
		xml = append(xml, fmt.Sprintf("<PartNumber>%d</PartNumber>", part.PartNumber))
		xml = append(xml, fmt.Sprintf("<ETag>%s</ETag>", part.ETag))
		xml = append(xml, "</Part>")
	}
	xml = append(xml, "</CompleteMultipartUpload>")
	data = strings.Join(xml, "")
	if returnMd5 {
		md5 = Base64Md5([]byte(data))
	}
	return
}

func parseSseHeader(responseHeaders map[string][]string) (sseHeader ISseHeader) {
	if ret, ok := responseHeaders[HEADER_SSEC_ENCRYPTION]; ok {
		sseCHeader := SseCHeader{Encryption: ret[0]}
		if ret, ok = responseHeaders[HEADER_SSEC_KEY_MD5]; ok {
			sseCHeader.KeyMD5 = ret[0]
		}
		sseHeader = sseCHeader
	} else if ret, ok := responseHeaders[HEADER_SSEKMS_ENCRYPTION]; ok {
		sseKmsHeader := SseKmsHeader{Encryption: ret[0]}
		if ret, ok = responseHeaders[HEADER_SSEKMS_KEY]; ok {
			sseKmsHeader.Key = ret[0]
		}
		sseHeader = sseKmsHeader
	}
	return
}

func ParseGetObjectMetadataOutput(output *GetObjectMetadataOutput) {
	if ret, ok := output.ResponseHeaders[HEADER_VERSION_ID]; ok {
		output.VersionId = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_WEBSITE_REDIRECT_LOCATION]; ok {
		output.WebsiteRedirectLocation = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_EXPIRATION]; ok {
		output.Expiration = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_RESTORE]; ok {
		output.Restore = ret[0]
	}

	if ret, ok := output.ResponseHeaders[HEADER_STORAGE_CLASS2]; ok {
		output.StorageClass = ParseStringToStorageClassType(ret[0])
	}
	if ret, ok := output.ResponseHeaders[HEADER_ETAG]; ok {
		output.ETag = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_TYPE]; ok {
		output.ContentType = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_ALLOW_ORIGIN]; ok {
		output.AllowOrigin = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_ALLOW_HEADERS]; ok {
		output.AllowHeader = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_MAX_AGE]; ok {
		output.MaxAgeSeconds = StringToInt(ret[0], 0)
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_ALLOW_METHODS]; ok {
		output.AllowMethod = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_EXPOSE_HEADERS]; ok {
		output.ExposeHeader = ret[0]
	}

	output.SseHeader = parseSseHeader(output.ResponseHeaders)
	if ret, ok := output.ResponseHeaders[HEADER_LASTMODIFIED]; ok {
		ret, err := time.Parse(time.RFC1123, ret[0])
		if err == nil {
			output.LastModified = ret
		}
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_LENGTH]; ok {
		output.ContentLength = StringToInt64(ret[0], 0)
	}

	output.Metadata = make(map[string]string)

	for key, value := range output.ResponseHeaders {
		if strings.HasPrefix(key, PREFIX_META) {
			_key := key[len(PREFIX_META):]
			output.ResponseHeaders[_key] = value
			output.Metadata[_key] = value[0]
			delete(output.ResponseHeaders, key)
		}
	}

}

func ParseCopyObjectOutput(output *CopyObjectOutput) {
	if ret, ok := output.ResponseHeaders[HEADER_VERSION_ID]; ok {
		output.VersionId = ret[0]
	}
	output.SseHeader = parseSseHeader(output.ResponseHeaders)
	if ret, ok := output.ResponseHeaders[HEADER_COPY_SOURCE_VERSION_ID]; ok {
		output.CopySourceVersionId = ret[0]
	}
}

func ParsePutObjectOutput(output *PutObjectOutput) {
	if ret, ok := output.ResponseHeaders[HEADER_VERSION_ID]; ok {
		output.VersionId = ret[0]
	}
	output.SseHeader = parseSseHeader(output.ResponseHeaders)
	if ret, ok := output.ResponseHeaders[HEADER_STORAGE_CLASS2]; ok {
		output.StorageClass = ParseStringToStorageClassType(ret[0])
	}

	if ret, ok := output.ResponseHeaders[HEADER_ETAG]; ok {
		output.ETag = ret[0]
	}
}

func ParseInitiateMultipartUploadOutput(output *InitiateMultipartUploadOutput) {
	output.SseHeader = parseSseHeader(output.ResponseHeaders)
}

func ParseUploadPartOutput(output *UploadPartOutput) {
	output.SseHeader = parseSseHeader(output.ResponseHeaders)
	if ret, ok := output.ResponseHeaders[HEADER_ETAG]; ok {
		output.ETag = ret[0]
	}
}

func ParseCompleteMultipartUploadOutput(output *CompleteMultipartUploadOutput) {
	output.SseHeader = parseSseHeader(output.ResponseHeaders)
	if ret, ok := output.ResponseHeaders[HEADER_VERSION_ID]; ok {
		output.VersionId = ret[0]
	}
}

func ParseCopyPartOutput(output *CopyPartOutput) {
	output.SseHeader = parseSseHeader(output.ResponseHeaders)
}

func ParseGetBucketMetadataOutput(output *GetBucketMetadataOutput) {
	if ret, ok := output.ResponseHeaders[HEADER_STORAGE_CLASS]; ok {
		output.StorageClass = ParseStringToStorageClassType(ret[0])
	}
	if ret, ok := output.ResponseHeaders[HEADER_BUCKET_REGION]; ok {
		output.Location = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_VERSION_OBS]; ok {
		output.ObsVersion = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_ALLOW_ORIGIN]; ok {
		output.AllowOrigin = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_ALLOW_HEADERS]; ok {
		output.AllowHeader = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_MAX_AGE]; ok {
		output.MaxAgeSeconds = StringToInt(ret[0], 0)
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_ALLOW_METHODS]; ok {
		output.AllowMethod = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_ACCESS_CONRTOL_EXPOSE_HEADERS]; ok {
		output.ExposeHeader = ret[0]
	}
}

func ParseSetObjectMetadataOutput(output *SetObjectMetadataOutput) {
	if ret, ok := output.ResponseHeaders[HEADER_STORAGE_CLASS2]; ok {
		output.StorageClass = ParseStringToStorageClassType(ret[0])
	}

	if ret, ok := output.ResponseHeaders[HEADER_METADATA_DIRECTIVE]; ok {
		output.MetadataDirective = MetadataDirectiveType(ret[0])
	}
	if ret, ok := output.ResponseHeaders[HEADER_CACHE_CONTROL]; ok {
		output.CacheControl = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_DISPOSITION]; ok {
		output.ContentDisposition = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_ENCODING]; ok {
		output.ContentEncoding = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_LANGUAGE]; ok {
		output.ContentLanguage = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_TYPE]; ok {
		output.ContentType = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_EXPIRES]; ok {
		output.Expires = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_WEBSITE_REDIRECT_LOCATION]; ok {
		output.WebsiteRedirectLocation = ret[0]
	}
	output.Metadata = make(map[string]string)

	for key, value := range output.ResponseHeaders {
		if strings.HasPrefix(key, PREFIX_META) {
			_key := key[len(PREFIX_META):]
			output.ResponseHeaders[_key] = value
			output.Metadata[_key] = value[0]
			delete(output.ResponseHeaders, key)
		}
	}
}

func ParseDeleteObjectOutput(output *DeleteObjectOutput) {
	if versionId, ok := output.ResponseHeaders[HEADER_VERSION_ID]; ok {
		output.VersionId = versionId[0]
	}

	if deleteMarker, ok := output.ResponseHeaders[HEADER_DELETE_MARKER]; ok {
		output.DeleteMarker = deleteMarker[0] == "true"
	}
}

func ParseGetObjectOutput(output *GetObjectOutput) {
	ParseGetObjectMetadataOutput(&output.GetObjectMetadataOutput)
	if ret, ok := output.ResponseHeaders[HEADER_DELETE_MARKER]; ok {
		output.DeleteMarker = ret[0] == "true"
	}
	if ret, ok := output.ResponseHeaders[HEADER_CACHE_CONTROL]; ok {
		output.CacheControl = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_DISPOSITION]; ok {
		output.ContentDisposition = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_ENCODING]; ok {
		output.ContentEncoding = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_CONTENT_LANGUAGE]; ok {
		output.ContentLanguage = ret[0]
	}
	if ret, ok := output.ResponseHeaders[HEADER_EXPIRES]; ok {
		output.Expires = ret[0]
	}
}

func ConvertRequestToIoReaderV2(req interface{}) (io.Reader, string, error) {
	data, err := TransToXml(req)
	if err == nil {
		if isDebugLogEnabled() {
			doLog(LEVEL_DEBUG, "Do http request with data: %s", string(data))
		}
		return bytes.NewReader(data), Base64Md5(data), nil
	}
	return nil, "", err
}

func ConvertRequestToIoReader(req interface{}) (io.Reader, error) {
	body, err := TransToXml(req)
	if err == nil {
		if isDebugLogEnabled() {
			doLog(LEVEL_DEBUG, "Do http request with data: %s", string(body))
		}
		return bytes.NewReader(body), nil
	}
	return nil, err
}

func ParseResponseToBaseModel(resp *http.Response, baseModel IBaseModel, xmlResult bool) (err error) {
	readCloser, ok := baseModel.(IReadCloser)
	if !ok {
		defer resp.Body.Close()
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		if err == nil && len(body) > 0 {
			if xmlResult {
				err = ParseXml(body, baseModel)
				if err != nil {
					doLog(LEVEL_ERROR, "Unmarshal error: %v", err)
				}
			} else {
				s := reflect.TypeOf(baseModel).Elem()
				for i := 0; i < s.NumField(); i++ {
					if s.Field(i).Tag == "body" {
						reflect.ValueOf(baseModel).Elem().FieldByName(s.Field(i).Name).SetString(string(body))
						break
					}
				}
			}
		}
	} else {
		readCloser.setReadCloser(resp.Body)
	}

	baseModel.setStatusCode(resp.StatusCode)
	responseHeaders := cleanHeaderPrefix(resp.Header)
	baseModel.setResponseHeaders(responseHeaders)
	if values, ok := responseHeaders[HEADER_REQUEST_ID]; ok {
		baseModel.setRequestId(values[0])
	}
	return
}

func ParseResponseToObsError(resp *http.Response) error {
	obsError := ObsError{}
	ParseResponseToBaseModel(resp, &obsError, true)
	obsError.Status = resp.Status
	return obsError
}
