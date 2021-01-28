package obs

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func prepareHeaders(headers map[string][]string, meta bool) map[string][]string {
	_headers := make(map[string][]string, len(headers))
	if headers != nil {
		for key, value := range headers {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			_key := strings.ToLower(key)
			if _, ok := allowedRequestHttpHeaderMetadataNames[_key]; !ok && !strings.HasPrefix(key, HEADER_PREFIX) && !strings.HasPrefix(key, HEADER_PREFIX_OBS) {
				if !meta {
					continue
				}
				_key = HEADER_PREFIX_META + _key
			} else {
				_key = key
			}
			_headers[_key] = value
		}
	}
	return _headers
}

func (obsClient ObsClient) doActionWithoutBucket(action, method string, input ISerializable, output IBaseModel) error {
	return obsClient.doAction(action, method, "", "", input, output, true, true)
}

func (obsClient ObsClient) doActionWithBucketV2(action, method, bucketName string, input ISerializable, output IBaseModel) error {
	if strings.TrimSpace(bucketName) == "" {
		return errors.New("Bucket is empty")
	}
	return obsClient.doAction(action, method, bucketName, "", input, output, false, true)
}

func (obsClient ObsClient) doActionWithBucket(action, method, bucketName string, input ISerializable, output IBaseModel) error {
	if strings.TrimSpace(bucketName) == "" {
		return errors.New("Bucket is empty")
	}
	return obsClient.doAction(action, method, bucketName, "", input, output, true, true)
}

func (obsClient ObsClient) doActionWithBucketAndKey(action, method, bucketName, objectKey string, input ISerializable, output IBaseModel) error {
	return obsClient._doActionWithBucketAndKey(action, method, bucketName, objectKey, input, output, true)
}

func (obsClient ObsClient) doActionWithBucketAndKeyUnRepeatable(action, method, bucketName, objectKey string, input ISerializable, output IBaseModel) error {
	return obsClient._doActionWithBucketAndKey(action, method, bucketName, objectKey, input, output, false)
}

func (obsClient ObsClient) _doActionWithBucketAndKey(action, method, bucketName, objectKey string, input ISerializable, output IBaseModel, repeatable bool) error {
	if strings.TrimSpace(bucketName) == "" {
		return errors.New("Key is empty")
	}
	if strings.TrimSpace(objectKey) == "" {
		return errors.New("Key is empty")
	}
	return obsClient.doAction(action, method, bucketName, objectKey, input, output, true, repeatable)
}

func (obsClient ObsClient) doAction(action, method, bucketName, objectKey string, input ISerializable, output IBaseModel, xmlResult bool, repeatable bool) error {

	var resp *http.Response
	var respError error
	doLog(LEVEL_INFO, "Enter method %s...", action)
	start := GetCurrentTimestamp()

	params, headers, data := input.trans()

	if params == nil {
		params = make(map[string]string)
	}

	if headers == nil {
		headers = make(map[string][]string)
	}

	switch method {
	case HTTP_GET:
		resp, respError = obsClient.doHttpGet(bucketName, objectKey, params, headers, data, repeatable)
	case HTTP_POST:
		resp, respError = obsClient.doHttpPost(bucketName, objectKey, params, headers, data, repeatable)
	case HTTP_PUT:
		resp, respError = obsClient.doHttpPut(bucketName, objectKey, params, headers, data, repeatable)
	case HTTP_DELETE:
		resp, respError = obsClient.doHttpDelete(bucketName, objectKey, params, headers, data, repeatable)
	case HTTP_HEAD:
		resp, respError = obsClient.doHttpHead(bucketName, objectKey, params, headers, data, repeatable)
	case HTTP_OPTIONS:
		resp, respError = obsClient.doHttpOptions(bucketName, objectKey, params, headers, data, repeatable)
	default:
		respError = errors.New("Unexpect http method error")
	}
	if respError == nil && output != nil {
		respError = ParseResponseToBaseModel(resp, output, xmlResult)
		if respError != nil {
			doLog(LEVEL_WARN, "Parse response to BaseModel with error: %v", respError)
		}
	} else {
		doLog(LEVEL_WARN, "Do http request with error: %v", respError)
	}

	if isDebugLogEnabled() {
		doLog(LEVEL_DEBUG, "End method %s, obsclient cost %d ms", action, (GetCurrentTimestamp() - start))
	}

	return respError
}

func (obsClient ObsClient) doHttpGet(bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (*http.Response, error) {
	return obsClient.doHttp(HTTP_GET, bucketName, objectKey, params, prepareHeaders(headers, false), data, repeatable)
}

func (obsClient ObsClient) doHttpHead(bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (*http.Response, error) {
	return obsClient.doHttp(HTTP_HEAD, bucketName, objectKey, params, prepareHeaders(headers, false), data, repeatable)
}

func (obsClient ObsClient) doHttpOptions(bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (*http.Response, error) {
	return obsClient.doHttp(HTTP_OPTIONS, bucketName, objectKey, params, prepareHeaders(headers, false), data, repeatable)
}

func (obsClient ObsClient) doHttpDelete(bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (*http.Response, error) {
	return obsClient.doHttp(HTTP_DELETE, bucketName, objectKey, params, prepareHeaders(headers, false), data, repeatable)
}

func (obsClient ObsClient) doHttpPut(bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (*http.Response, error) {
	return obsClient.doHttp(HTTP_PUT, bucketName, objectKey, params, prepareHeaders(headers, true), data, repeatable)
}

func (obsClient ObsClient) doHttpPost(bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (*http.Response, error) {
	return obsClient.doHttp(HTTP_POST, bucketName, objectKey, params, prepareHeaders(headers, true), data, repeatable)
}

func (obsClient ObsClient) doHttpWithSignedUrl(action, method string, signedUrl string, actualSignedRequestHeaders http.Header, data io.Reader, output IBaseModel, xmlResult bool) (respError error) {
	req, err := http.NewRequest(method, signedUrl, data)
	if err != nil {
		return err
	}

	var resp *http.Response

	doLog(LEVEL_INFO, "Do %s with signedUrl %s...", action, signedUrl)

	req.Header = actualSignedRequestHeaders
	if value, ok := req.Header[HEADER_HOST_CAMEL]; ok {
		req.Host = value[0]
		delete(req.Header, HEADER_HOST_CAMEL)
	} else if value, ok := req.Header[HEADER_HOST]; ok {
		req.Host = value[0]
		delete(req.Header, HEADER_HOST)
	}

	if value, ok := req.Header[HEADER_CONTENT_LENGTH_CAMEL]; ok {
		req.ContentLength = StringToInt64(value[0], -1)
		delete(req.Header, HEADER_CONTENT_LENGTH_CAMEL)
	} else if value, ok := req.Header[HEADER_CONTENT_LENGTH]; ok {
		req.ContentLength = StringToInt64(value[0], -1)
		delete(req.Header, HEADER_CONTENT_LENGTH)
	}

	req.Header[HEADER_USER_AGENT_CAMEL] = []string{USER_AGENT}
	start := GetCurrentTimestamp()
	resp, err = obsClient.httpClient.Do(req)
	if isInfoLogEnabled() {
		doLog(LEVEL_INFO, "Do http request cost %d ms", (GetCurrentTimestamp() - start))
	}

	var msg interface{}
	if err != nil {
		respError = err
		resp = nil
	} else {
		doLog(LEVEL_DEBUG, "Response headers: %v", resp.Header)
		if resp.StatusCode >= 300 {
			respError = ParseResponseToObsError(resp)
			msg = resp.Status
			resp = nil
		} else {
			if output != nil {
				respError = ParseResponseToBaseModel(resp, output, xmlResult)
			}
			if respError != nil {
				doLog(LEVEL_WARN, "Parse response to BaseModel with error: %v", respError)
			}
		}
	}

	if msg != nil {
		doLog(LEVEL_ERROR, "Failed to send request with reason:%v", msg)
	}

	if isDebugLogEnabled() {
		doLog(LEVEL_DEBUG, "End method %s, obsclient cost %d ms", action, (GetCurrentTimestamp() - start))
	}

	return
}

func (obsClient ObsClient) doHttp(method, bucketName, objectKey string, params map[string]string,
	headers map[string][]string, data interface{}, repeatable bool) (resp *http.Response, respError error) {

	bucketName = strings.TrimSpace(bucketName)

	method = strings.ToUpper(method)

	var redirectUrl string
	var requestUrl string
	maxRetryCount := obsClient.conf.maxRetryCount

	var _data io.Reader
	if data != nil {
		if dataStr, ok := data.(string); ok {
			doLog(LEVEL_DEBUG, "Do http request with string: %s", dataStr)
			headers["Content-Length"] = []string{IntToString(len(dataStr))}
			_data = strings.NewReader(dataStr)
		} else if dataByte, ok := data.([]byte); ok {
			doLog(LEVEL_DEBUG, "Do http request with byte array")
			headers["Content-Length"] = []string{IntToString(len(dataByte))}
			_data = bytes.NewReader(dataByte)
		} else if dataReader, ok := data.(io.Reader); ok {
			_data = dataReader
		} else {
			doLog(LEVEL_WARN, "Data is not a valid io.Reader")
			return nil, errors.New("Data is not a valid io.Reader")
		}
	}

	for i := 0; i <= maxRetryCount; i++ {
		if redirectUrl != "" {
			parsedRedirectUrl, err := url.Parse(redirectUrl)
			if err != nil {
				return nil, err
			}
			requestUrl, _ = obsClient.doAuth(method, bucketName, objectKey, params, headers, parsedRedirectUrl.Host)
			if parsedRequestUrl, _ := url.Parse(requestUrl); parsedRequestUrl.RawQuery != "" && parsedRedirectUrl.RawQuery == "" {
				redirectUrl += "?" + parsedRequestUrl.RawQuery
			}
			requestUrl = redirectUrl
		} else {
			var err error
			requestUrl, err = obsClient.doAuth(method, bucketName, objectKey, params, headers, "")
			if err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequest(method, requestUrl, _data)
		if err != nil {
			return nil, err
		}
		doLog(LEVEL_DEBUG, "Do request with url [%s] and method [%s]", requestUrl, method)

		if isDebugLogEnabled() {
			auth := headers[HEADER_AUTH_CAMEL]
			delete(headers, HEADER_AUTH_CAMEL)
			doLog(LEVEL_DEBUG, "Request headers: %v", headers)
			headers[HEADER_AUTH_CAMEL] = auth
		}

		for key, value := range headers {
			if key == HEADER_HOST_CAMEL {
				req.Host = value[0]
				delete(headers, key)
			} else if key == HEADER_CONTENT_LENGTH_CAMEL {
				req.ContentLength = StringToInt64(value[0], -1)
				delete(headers, key)
			} else {
				req.Header[key] = value
			}
		}

		req.Header[HEADER_USER_AGENT_CAMEL] = []string{USER_AGENT}

		start := GetCurrentTimestamp()
		resp, err = obsClient.httpClient.Do(req)
		if isInfoLogEnabled() {
			doLog(LEVEL_INFO, "Do http request cost %d ms", (GetCurrentTimestamp() - start))
		}

		var msg interface{}
		if err != nil {
			msg = err
			respError = err
			resp = nil
		} else {
			doLog(LEVEL_DEBUG, "Response headers: %v", resp.Header)
			if resp.StatusCode < 300 {
				break
			} else if !repeatable || (resp.StatusCode >= 400 && resp.StatusCode < 500) || resp.StatusCode == 304 {
				respError = ParseResponseToObsError(resp)
				resp = nil
				break
			} else if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				if location := resp.Header.Get(HEADER_LOCATION_CAMEL); location != "" {
					redirectUrl = location
					doLog(LEVEL_WARN, "Redirect request to %s", redirectUrl)
					msg = resp.Status
					maxRetryCount++
				} else {
					respError = ParseResponseToObsError(resp)
					resp = nil
					break
				}
			} else {
				msg = resp.Status
			}
		}
		if i != maxRetryCount {
			if resp != nil {
				resp.Body.Close()
				resp = nil
			}
			if _, ok := headers[HEADER_AUTH_CAMEL]; ok {
				delete(headers, HEADER_AUTH_CAMEL)
			}
			doLog(LEVEL_WARN, "Failed to send request with reason:%v, will try again", msg)
			if r, ok := _data.(*strings.Reader); ok {
				_, err := r.Seek(0, 0)
				if err != nil {
					return nil, err
				}
			} else if r, ok := _data.(*bytes.Reader); ok {
				_, err := r.Seek(0, 0)
				if err != nil {
					return nil, err
				}
			} else if r, ok := _data.(*fileReaderWrapper); ok {
				fd, err := os.Open(r.filePath)
				if err != nil {
					return nil, err
				}
				defer fd.Close()
				fileReaderWrapper := &fileReaderWrapper{filePath: r.filePath}
				fileReaderWrapper.mark = r.mark
				fileReaderWrapper.reader = fd
				fileReaderWrapper.totalCount = r.totalCount
				_data = fileReaderWrapper
				_, err = fd.Seek(r.mark, 0)
				if err != nil {
					return nil, err
				}
			} else if r, ok := _data.(*readerWrapper); ok {
				_, err := r.seek(0, 0)
				if err != nil {
					return nil, err
				}
			}
			time.Sleep(time.Duration(float64(i+2) * rand.Float64() * float64(time.Second)))
		} else {
			doLog(LEVEL_ERROR, "Failed to send request with reason:%v", msg)
			if resp != nil {
				respError = ParseResponseToObsError(resp)
				resp = nil
			}
		}
	}
	return
}

type connDelegate struct {
	conn          net.Conn
	socketTimeout time.Duration
	finalTimeout  time.Duration
}

func getConnDelegate(conn net.Conn, socketTimeout int, finalTimeout int) *connDelegate {
	return &connDelegate{
		conn:          conn,
		socketTimeout: time.Second * time.Duration(socketTimeout),
		finalTimeout:  time.Second * time.Duration(finalTimeout),
	}
}

func (delegate *connDelegate) Read(b []byte) (n int, err error) {
	delegate.SetReadDeadline(time.Now().Add(delegate.socketTimeout))
	n, err = delegate.conn.Read(b)
	delegate.SetReadDeadline(time.Now().Add(delegate.finalTimeout))
	return n, err
}

func (delegate *connDelegate) Write(b []byte) (n int, err error) {
	delegate.SetWriteDeadline(time.Now().Add(delegate.socketTimeout))
	n, err = delegate.conn.Write(b)
	finalTimeout := time.Now().Add(delegate.finalTimeout)
	delegate.SetWriteDeadline(finalTimeout)
	delegate.SetReadDeadline(finalTimeout)
	return n, err
}

func (delegate *connDelegate) Close() error {
	return delegate.conn.Close()
}

func (delegate *connDelegate) LocalAddr() net.Addr {
	return delegate.conn.LocalAddr()
}

func (delegate *connDelegate) RemoteAddr() net.Addr {
	return delegate.conn.RemoteAddr()
}

func (delegate *connDelegate) SetDeadline(t time.Time) error {
	return delegate.conn.SetDeadline(t)
}

func (delegate *connDelegate) SetReadDeadline(t time.Time) error {
	return delegate.conn.SetReadDeadline(t)
}

func (delegate *connDelegate) SetWriteDeadline(t time.Time) error {
	return delegate.conn.SetWriteDeadline(t)
}
