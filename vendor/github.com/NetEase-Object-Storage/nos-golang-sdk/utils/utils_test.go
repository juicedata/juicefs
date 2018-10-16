package utils

import (
	. "gopkg.in/check.v1"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
)

func Test(t *testing.T) { TestingT(t) }

type UtilsTestSuite struct{}

func (s *UtilsTestSuite) SetUpSuite(c *C) {
}

var _ = Suite(&UtilsTestSuite{})

func (s *UtilsTestSuite) TestNosUrlEncode(c *C) {
	//test normal
	str := "123/456"
	desStr := "123%2F456"
	encodeStr := NosUrlEncode(str)
	c.Assert(encodeStr, Equals, desStr)

	//test + replace by %20
	str = ` 特殊   字符-=[]\\;',./ ~!@#$%^&*()_+{}|:\"<>?`
	desStr2 := `%20%E7%89%B9%E6%AE%8A%20%20%20%E5%AD%97%E7%AC%A6-%3D%5B%5D%5C%5C%3B%27%2C.%2F%20%7E%21%40%23%24%25%5E%26*%28%29_%2B%7B%7D%7C%3A%5C%22%3C%3E%3F`
	encodeStr = NosUrlEncode(str)
	c.Assert(encodeStr, Equals, desStr2)
}

func (s *UtilsTestSuite) TestVerifyBucketName(c *C) {
	ok := VerifyBucketName("")
	c.Assert(ok, Equals, false)

	// len < 3
	ok = VerifyBucketName("2")
	c.Assert(ok, Equals, false)

	// len > 63
	bucketName := ""
	for i := 0; i != 6; i++ {
		bucketName += "1234567890"
	}
	bucketName += "1234"

	ok = VerifyBucketName(bucketName)
	c.Assert(ok, Equals, false)

	//test Uppercase letter
	ok = VerifyBucketName("docA")
	c.Assert(ok, Equals, false)

	//test start special
	ok = VerifyBucketName("-123")
	c.Assert(ok, Equals, false)

	//test end special
	ok = VerifyBucketName("123-")
	c.Assert(ok, Equals, false)

	//test not supported special
	ok = VerifyBucketName("12&3")
	c.Assert(ok, Equals, false)

	//test continuous 2 special
	ok = VerifyBucketName("12..3")
	c.Assert(ok, Equals, false)

	//test ip style
	ok = VerifyBucketName("255.255.255.255")
	c.Assert(ok, Equals, false)

	//test ok
	ok = VerifyBucketName("doc")
	c.Assert(ok, Equals, true)
}

func (s *UtilsTestSuite) TestVerifyParam(c *C) {
	ok := VerifyParams("")
	c.Assert(ok, NotNil)

	ok = VerifyParams("12.12")
	c.Assert(ok, NotNil)

	ok = VerifyParams("object")
	c.Assert(ok, IsNil)
}

func (s *UtilsTestSuite) TestVerifyParamsWithLength(c *C) {
	ok := VerifyParamsWithLength("123", "21", int64(0))
	c.Assert(ok, IsNil)

	ok = VerifyParamsWithLength("123.112", "21", int64(23))
	c.Assert(ok, NotNil)

	ok = VerifyParamsWithLength("object", "123", int64(1111111111111))
	c.Assert(ok, NotNil)
}

func (s *UtilsTestSuite) TestVerifyParamsWithObject(c *C) {
	ok := VerifyParamsWithObject("123", "21")
	c.Assert(ok, IsNil)

	ok = VerifyParamsWithObject("object", "")
	c.Assert(ok, NotNil)
}

func (s *UtilsTestSuite) TestVerifyObjectName(c *C) {
	ok := VerifyObjectName("")
	c.Assert(ok, Equals, false)

	// len > 1000
	objectName := ""
	for i := 0; i != 100; i++ {
		objectName += "1234567890"
	}
	objectName += "1"

	ok = VerifyObjectName(objectName)
	c.Assert(ok, Equals, false)

	ok = VerifyObjectName("object")
	c.Assert(ok, Equals, true)
}

func (s *UtilsTestSuite) TestProcessClientError(c *C) {
	err := ProcessClientError(400, "", "")
	c.Assert(err.Error(), Equals, "StatusCode = 400, Resource = , Message = ")

	err = ProcessClientError(400, "123", "123")
	c.Assert(err.Error(), Equals, "StatusCode = 400, Resource = /123/123, Message = ")
}

func (s *UtilsTestSuite) TestRemoveQuotes(c *C) {
	str := RemoveQuotes("\"213\"")
	c.Assert(str, Equals, "213")

	str = RemoveQuotes("123")
	c.Assert(str, Equals, "123")
}

func (s *UtilsTestSuite) TestPopulateResponseHeader(c *C) {
	response := &http.Response{
		Header: map[string][]string{"Etag": []string{"123"}, "X-Nos-Request-Id": []string{"123"}},
	}
	request, etag := PopulateResponseHeader(response)
	c.Assert(request, Equals, "123")
	c.Assert(etag, Equals, "123")
}

func (s *UtilsTestSuite) TestPopulateAllHeader(c *C) {
	response := &http.Response{
		Header: map[string][]string{"Etag": []string{"123"}, "X-Nos-Request-Id": []string{"123"}, "Content-Length": []string{"123"}},
	}

	objectMetadata := PopulateAllHeader(response)
	c.Assert(objectMetadata.Metadata["Etag"], Equals, "123")
	c.Assert(objectMetadata.ContentLength, Equals, int64(123))
}

func (s *UtilsTestSuite) TestProcessServerError(c *C) {

	read := strings.NewReader("1234")
	r := ioutil.NopCloser(read)

	response := &http.Response{
		Body:   r,
		Header: map[string][]string{"Content-Type": []string{"json"}, "X-Nos-Request-Id": []string{"123"}, "Content-Length": []string{"123"}},
	}
	err := ProcessServerError(response, "123", "123")
	c.Assert(err, NotNil)

	response = &http.Response{
		Body:   r,
		Header: map[string][]string{"Content-Type": []string{"xml"}, "X-Nos-Request-Id": []string{"123"}, "Content-Length": []string{"123"}},
	}
	err = ProcessServerError(response, "123", "123")
	c.Assert(err, NotNil)
}
