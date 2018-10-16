package noserror

import (
	"encoding/xml"
	"strconv"
)

const (
	BASE_ERROR_CODE = 400

	/*client error codes*/
	//4xx errors
	ERROR_CODE_CFG_ENDPOINT             = BASE_ERROR_CODE + 20
	ERROR_CODE_CFG_CONNECT_TIMEOUT      = BASE_ERROR_CODE + 21
	ERROR_CODE_CFG_READWRITE_TIMEOUT    = BASE_ERROR_CODE + 22
	ERROR_CODE_CFG_MAXIDLECONNECT       = BASE_ERROR_CODE + 23
	ERROR_CODE_BUCKET_INVALID           = BASE_ERROR_CODE + 30
	ERROR_CODE_OBJECT_INVALID           = BASE_ERROR_CODE + 31
	ERROR_CODE_FILELENGTH_INVALID       = BASE_ERROR_CODE + 32
	ERROR_CODE_FILE_INVALID         = BASE_ERROR_CODE + 33
	ERROR_CODE_REQUEST_ERROR            = BASE_ERROR_CODE + 34
	ERROR_CODE_READCONTENT_ERROR        = BASE_ERROR_CODE + 35
	ERROR_CODE_PARSEJSON_ERROR          = BASE_ERROR_CODE + 36
	ERROR_CODE_PARSEXML_ERROR           = BASE_ERROR_CODE + 37
	ERROR_CODE_SRCBUCKETANDOBJECT_ERROR = BASE_ERROR_CODE + 38
	ERROR_CODE_DELETEMULTIOBJECTS_ERROR = BASE_ERROR_CODE + 39
	ERROR_CODE_OBJECTSBIGGER_ERROR      = BASE_ERROR_CODE + 40
	ERROR_CODE_PARTLENGTH_ERROR         = BASE_ERROR_CODE + 41

	/*short message code*/
	ERROR_MSG_CFG_ENDPOINT             = "Config: InvalidEndpoint"
	ERROR_MSG_CFG_CONNECT_TIMEOUT      = "Config: InvalidConnectionTimeout"
	ERROR_MSG_CFG_READWRITE_TIMEOUT    = "Config: InvalidReadWriteTimeout"
	ERROR_MSG_CFG_MAXIDLECONNECT       = "Config: InvalidMaxIdleConnect"
	ERROR_MSG_BUCKET_INVALID           = "InvalidBucketName"
	ERROR_MSG_OBJECT_INVALID           = "InvalidObjectName"
	ERROR_MSG_FILELENGTH_INVALID       = "InvalidFileSize"
	ERROR_MSG_FILE_INVALID             = "Failed to open file. "
	ERROR_MSG_REQUEST_ERROR            = "Request is nil"
	ERROR_MSG_READCONTENT_ERROR        = "ReadContentError"
	ERROR_MSG_PARSEJSON_ERROR          = "InvalidJSONContent"
	ERROR_MSG_PARSEXML_ERROR           = "InvalidXmlContent"
	ERROR_MSG_SRCBUCKETANDOBJECT_ERROR = "SrcBucket or SrcObject is invalid"
	ERROR_MSG_DELETEMULTIOBJECTS_ERROR = "InvalidDeleteMultiObjects"
	ERROR_MSG_OBJECTSBIGGER_ERROR      = "InvalidObjects: the number is < 1000 and size of body is < 2M"
	ERROR_MSG_PARTLENGTH_ERROR         = "InvalidPartLength: the length should be between  16k and 100M"
)

// mErrHttpCodeMap is map of Http Code
var mErrMsgMap map[int]string

func Init() {
	mErrMsgMap = make(map[int]string)

	//init err msg map
	mErrMsgMap[ERROR_CODE_CFG_ENDPOINT] = ERROR_MSG_CFG_ENDPOINT
	mErrMsgMap[ERROR_CODE_CFG_CONNECT_TIMEOUT] = ERROR_MSG_CFG_CONNECT_TIMEOUT
	mErrMsgMap[ERROR_CODE_CFG_READWRITE_TIMEOUT] = ERROR_MSG_CFG_READWRITE_TIMEOUT
	mErrMsgMap[ERROR_CODE_CFG_MAXIDLECONNECT] = ERROR_MSG_CFG_MAXIDLECONNECT
	mErrMsgMap[ERROR_CODE_BUCKET_INVALID] = ERROR_MSG_BUCKET_INVALID
	mErrMsgMap[ERROR_CODE_OBJECT_INVALID] = ERROR_MSG_OBJECT_INVALID
	mErrMsgMap[ERROR_CODE_FILELENGTH_INVALID] = ERROR_MSG_FILELENGTH_INVALID
	mErrMsgMap[ERROR_CODE_FILE_INVALID] = ERROR_MSG_FILE_INVALID
	mErrMsgMap[ERROR_CODE_REQUEST_ERROR] = ERROR_MSG_REQUEST_ERROR
	mErrMsgMap[ERROR_CODE_READCONTENT_ERROR] = ERROR_MSG_READCONTENT_ERROR
	mErrMsgMap[ERROR_CODE_PARSEJSON_ERROR] = ERROR_MSG_PARSEJSON_ERROR
	mErrMsgMap[ERROR_CODE_PARSEXML_ERROR] = ERROR_MSG_PARSEXML_ERROR
	mErrMsgMap[ERROR_CODE_SRCBUCKETANDOBJECT_ERROR] = ERROR_MSG_SRCBUCKETANDOBJECT_ERROR
	mErrMsgMap[ERROR_CODE_DELETEMULTIOBJECTS_ERROR] = ERROR_MSG_DELETEMULTIOBJECTS_ERROR
	mErrMsgMap[ERROR_CODE_OBJECTSBIGGER_ERROR] = ERROR_MSG_OBJECTSBIGGER_ERROR
	mErrMsgMap[ERROR_CODE_PARTLENGTH_ERROR] = ERROR_MSG_PARTLENGTH_ERROR
}

type NosError struct {
	XMLName      xml.Name `xml:"Error" json:"-"`
	Code         string   `xml:"Code" json:"Code"`
	Message      string   `xml:"Message" json:"Message"`
	Resource     string   `xml:"Resource" json:"Resource"`
	NosRequestId string   `xml:"RequestId" json:"RequestId"`
}

func NewNosError(code string, message string, resource string, requestid string) *NosError {
	nosError := &(NosError{
		Code:         code,
		Message:      message,
		Resource:     resource,
		NosRequestId: requestid,
	})
	return nosError
}

func (nosError *NosError) Error() string {
	return "Code = " + nosError.Code +
		", Message = " + nosError.Message +
		", Resource = " + nosError.Resource +
		", NosRequestId = " + nosError.NosRequestId
}

type ServerError struct {
	StatusCode int
	RequestId  string
	NosErr     *NosError `json:"Error"`
}

func NewServerError(errCode int, requestid string, nosErr *NosError) error {
	serverError := &(ServerError{
		StatusCode: errCode,
		RequestId:  requestid,
		NosErr:     nosErr,
	})
	return serverError
}

func (serverError *ServerError) Error() string {
	return "StatusCode = " + strconv.Itoa(serverError.StatusCode) +
		", RequestId = " + serverError.RequestId +
		", NosError: " + serverError.NosErr.Error()
}

type ClientError struct {
	StatusCode int
	Resource   string
	Message    string
}

func NewClientError(errCode int, resource string, msg string) error {
	clientError := &(ClientError{
		StatusCode: errCode,
		Resource:   resource,
		Message:    mErrMsgMap[errCode],
	})
	if msg != ""{
		clientError.Message += ": " + msg
	}
	return clientError
}

func (clientError *ClientError) Error() string {
	return "StatusCode = " + strconv.Itoa(clientError.StatusCode) +
		", Resource = " + clientError.Resource +
		", Message = " + clientError.Message
}
