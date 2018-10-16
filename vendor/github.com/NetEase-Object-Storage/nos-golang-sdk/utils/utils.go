package utils

import (
	"encoding/json"
	"encoding/xml"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/model"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/nosconst"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/noserror"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"runtime"
)

// VerifyObjectName check if the BucketName is legal
func VerifyBucketName(bucketName string) bool {
	if bucketName == "" {
		return false
	}

	length := len(bucketName)
	if length < 3 || length > 63 {
		return false
	}

	if bucketName != strings.ToLower(bucketName) {
		return false
	}

	if strings.Contains(bucketName, ".") {
		return false
	}

	specialCharactersCursor := false
	for i := 0; i != length; i++ {
		ch := bucketName[i]
		if !unicode.IsLetter(rune(ch)) && !unicode.IsDigit(rune(ch)) {
			//no start or end with special characters
			if i == 0 || i == (length-1) {
				return false
			}
			//no Continuous two speical characters
			if specialCharactersCursor {
				return false
			}
			specialCharactersCursor = true
			if ch != '-' {
				return false
			}
		} else {
			specialCharactersCursor = false
		}
	}

	return true
}

// VerifyObjectName check if the object name is legal
func VerifyObjectName(object string) bool {
	if object == "" {
		return false
	}
	if len(object) > 1000 {
		return false
	}
	return true
}

func VerifyParams(bucket string) error {
	if !VerifyBucketName(bucket) {
		return ProcessClientError(noserror.ERROR_CODE_BUCKET_INVALID, bucket, "", "")
	}

	return nil
}

func VerifyParamsWithObject(bucket string, object string) error {

	err := VerifyParams(bucket)
	if err != nil {
		return err
	}

	if !VerifyObjectName(object) {
		return ProcessClientError(noserror.ERROR_CODE_OBJECT_INVALID, bucket, object, "")
	}

	return nil
}

func VerifyParamsWithLength(bucket string, object string, length int64) error {

	err := VerifyParamsWithObject(bucket, object)
	if err != nil {
		return err
	}

	if length > nosconst.MAX_FILESIZE {
		return ProcessClientError(noserror.ERROR_CODE_FILELENGTH_INVALID, bucket, object, "")
	}

	return nil
}

func ParseXmlBody(body io.Reader, value interface{}) error {
	content, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}
	err = xml.Unmarshal(content, value)
	if err != nil {
		return err
	}

	return nil
}

func RemoveQuotes(orig string) string {
	s := strings.TrimSpace(orig)

	if strings.HasPrefix(s, "\"") {
		s = s[1:len(s)]
	}

	if strings.HasSuffix(s, "\"") {
		s = s[0 : len(s)-1]
	}

	return s
}

func PopulateResponseHeader(response *http.Response) (requestid, etag string) {
	hdr := response.Header

	etag = RemoveQuotes(hdr.Get(nosconst.ETAG))
	requestid = hdr.Get(nosconst.X_NOS_REQUEST_ID)

	return requestid, etag
}

func PopulateAllHeader(response *http.Response) *model.ObjectMetadata {
	hdr := response.Header
	result := &model.ObjectMetadata{
		Metadata: map[string]string{},
	}

	for key, value := range hdr {
		if value != nil {
			if strings.EqualFold(key, nosconst.CONTENT_LENGTH) {
				result.ContentLength, _ = strconv.ParseInt(value[0], 10, 64)
			} else if strings.EqualFold(key, nosconst.ETAG) {
				result.Metadata[nosconst.ETAG] = RemoveQuotes(value[0])
			} else {
				result.Metadata[key] = value[0]
			}
		}
	}

	return result
}

func ProcessClientError(statCode int, bucket, object string, msg string) error {
	var resource string
	if bucket != "" {
		resource += "/" + bucket
	}
	if object != "" {
		resource += "/" + object
	}
	clientError := noserror.NewClientError(statCode, resource, msg)

	return clientError
}

func ProcessServerError(response *http.Response, bucketName, objectName string) error {
	var nosErr *noserror.NosError

	resource := bucketName + "/" + objectName
	requestId := response.Header.Get(nosconst.X_NOS_REQUEST_ID)
	contenttype := response.Header.Get(nosconst.CONTENT_TYPE)

	serverError := &noserror.ServerError{
		StatusCode: response.StatusCode,
		RequestId:  requestId,
	}

	content, err := ioutil.ReadAll(response.Body)
	if err != nil {
		nosErr = noserror.NewNosError("", noserror.ERROR_MSG_READCONTENT_ERROR, resource, requestId)
		serverError.NosErr = nosErr
	} else {
		if strings.Contains(contenttype, nosconst.JSON_TYPE) {
			if err = json.Unmarshal(content, &serverError); err != nil {
				nosErr = noserror.NewNosError("", noserror.ERROR_MSG_PARSEJSON_ERROR, resource, requestId)
				serverError.NosErr = nosErr
			}
		} else {
			if err = xml.Unmarshal(content, &nosErr); err != nil {
				nosErr = noserror.NewNosError("", noserror.ERROR_MSG_PARSEXML_ERROR, resource, requestId)
			}
			serverError.NosErr = nosErr
		}
	}

	return serverError
}

func NosUrlEncode(origin string) string {
	str := strings.Replace(url.QueryEscape(origin), "+", "%20", -1)
	str = strings.Replace(str, "~", "%7E", -1)
	str = strings.Replace(str, "%2A", "*", -1)
	return str
}

func InitUserAgent() string {
	str := nosconst.SDKNAME + "/" + nosconst.VERSION + " "
	str += runtime.GOOS + "/" + runtime.GOARCH + "/"
	str += "golang version:" + runtime.Version()

	return str
}