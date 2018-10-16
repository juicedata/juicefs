package jsonrpc_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/jsonrpc"
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/xml/xmlutil"
	"github.com/ks3sdklib/aws-sdk-go/internal/signer/v4"
	"github.com/ks3sdklib/aws-sdk-go/internal/util"
	"github.com/stretchr/testify/assert"
)

var _ bytes.Buffer // always import bytes
var _ http.Request
var _ json.Marshaler
var _ time.Time
var _ xmlutil.XMLNode
var _ xml.Attr
var _ = ioutil.Discard
var _ = util.Trim("")
var _ = url.Values{}
var _ = io.EOF

// InputService1ProtocolTest is a client for InputService1ProtocolTest.
type InputService1ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService1ProtocolTest client.
func NewInputService1ProtocolTest(config *aws.Config) *InputService1ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "inputservice1protocoltest",
		APIVersion:   "",
		JSONVersion:  "1.1",
		TargetPrefix: "com.amazonaws.foo",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &InputService1ProtocolTest{service}
}

// newRequest creates a new request for a InputService1ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService1ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService1TestCaseOperation1Request generates a request for the InputService1TestCaseOperation1 operation.
func (c *InputService1ProtocolTest) InputService1TestCaseOperation1Request(input *InputService1TestShapeInputShape) (req *aws.Request, output *InputService1TestShapeInputService1TestCaseOperation1Output) {

	if opInputService1TestCaseOperation1 == nil {
		opInputService1TestCaseOperation1 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
		}
	}

	if input == nil {
		input = &InputService1TestShapeInputShape{}
	}

	req = c.newRequest(opInputService1TestCaseOperation1, input, output)
	output = &InputService1TestShapeInputService1TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService1ProtocolTest) InputService1TestCaseOperation1(input *InputService1TestShapeInputShape) (*InputService1TestShapeInputService1TestCaseOperation1Output, error) {
	req, out := c.InputService1TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService1TestCaseOperation1 *aws.Operation

type InputService1TestShapeInputService1TestCaseOperation1Output struct {
	metadataInputService1TestShapeInputService1TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService1TestShapeInputService1TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService1TestShapeInputShape struct {
	Name *string `type:"string"`

	metadataInputService1TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService1TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService2ProtocolTest is a client for InputService2ProtocolTest.
type InputService2ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService2ProtocolTest client.
func NewInputService2ProtocolTest(config *aws.Config) *InputService2ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "inputservice2protocoltest",
		APIVersion:   "",
		JSONVersion:  "1.1",
		TargetPrefix: "com.amazonaws.foo",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &InputService2ProtocolTest{service}
}

// newRequest creates a new request for a InputService2ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService2ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService2TestCaseOperation1Request generates a request for the InputService2TestCaseOperation1 operation.
func (c *InputService2ProtocolTest) InputService2TestCaseOperation1Request(input *InputService2TestShapeInputShape) (req *aws.Request, output *InputService2TestShapeInputService2TestCaseOperation1Output) {

	if opInputService2TestCaseOperation1 == nil {
		opInputService2TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService2TestShapeInputShape{}
	}

	req = c.newRequest(opInputService2TestCaseOperation1, input, output)
	output = &InputService2TestShapeInputService2TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService2ProtocolTest) InputService2TestCaseOperation1(input *InputService2TestShapeInputShape) (*InputService2TestShapeInputService2TestCaseOperation1Output, error) {
	req, out := c.InputService2TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService2TestCaseOperation1 *aws.Operation

type InputService2TestShapeInputService2TestCaseOperation1Output struct {
	metadataInputService2TestShapeInputService2TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService2TestShapeInputService2TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService2TestShapeInputShape struct {
	TimeArg *time.Time `type:"timestamp" timestampFormat:"unix"`

	metadataInputService2TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService2TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService3ProtocolTest is a client for InputService3ProtocolTest.
type InputService3ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService3ProtocolTest client.
func NewInputService3ProtocolTest(config *aws.Config) *InputService3ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "inputservice3protocoltest",
		APIVersion:   "",
		JSONVersion:  "1.1",
		TargetPrefix: "com.amazonaws.foo",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &InputService3ProtocolTest{service}
}

// newRequest creates a new request for a InputService3ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService3ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService3TestCaseOperation1Request generates a request for the InputService3TestCaseOperation1 operation.
func (c *InputService3ProtocolTest) InputService3TestCaseOperation1Request(input *InputService3TestShapeInputShape) (req *aws.Request, output *InputService3TestShapeInputService3TestCaseOperation1Output) {

	if opInputService3TestCaseOperation1 == nil {
		opInputService3TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService3TestShapeInputShape{}
	}

	req = c.newRequest(opInputService3TestCaseOperation1, input, output)
	output = &InputService3TestShapeInputService3TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService3ProtocolTest) InputService3TestCaseOperation1(input *InputService3TestShapeInputShape) (*InputService3TestShapeInputService3TestCaseOperation1Output, error) {
	req, out := c.InputService3TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService3TestCaseOperation1 *aws.Operation

// InputService3TestCaseOperation2Request generates a request for the InputService3TestCaseOperation2 operation.
func (c *InputService3ProtocolTest) InputService3TestCaseOperation2Request(input *InputService3TestShapeInputShape) (req *aws.Request, output *InputService3TestShapeInputService3TestCaseOperation2Output) {

	if opInputService3TestCaseOperation2 == nil {
		opInputService3TestCaseOperation2 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService3TestShapeInputShape{}
	}

	req = c.newRequest(opInputService3TestCaseOperation2, input, output)
	output = &InputService3TestShapeInputService3TestCaseOperation2Output{}
	req.Data = output
	return
}

func (c *InputService3ProtocolTest) InputService3TestCaseOperation2(input *InputService3TestShapeInputShape) (*InputService3TestShapeInputService3TestCaseOperation2Output, error) {
	req, out := c.InputService3TestCaseOperation2Request(input)
	err := req.Send()
	return out, err
}

var opInputService3TestCaseOperation2 *aws.Operation

type InputService3TestShapeInputService3TestCaseOperation1Output struct {
	metadataInputService3TestShapeInputService3TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService3TestShapeInputService3TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService3TestShapeInputService3TestCaseOperation2Output struct {
	metadataInputService3TestShapeInputService3TestCaseOperation2Output `json:"-" xml:"-"`
}

type metadataInputService3TestShapeInputService3TestCaseOperation2Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService3TestShapeInputShape struct {
	BlobArg []byte `type:"blob"`

	BlobMap map[string][]byte `type:"map"`

	metadataInputService3TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService3TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService4ProtocolTest is a client for InputService4ProtocolTest.
type InputService4ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService4ProtocolTest client.
func NewInputService4ProtocolTest(config *aws.Config) *InputService4ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "inputservice4protocoltest",
		APIVersion:   "",
		JSONVersion:  "1.1",
		TargetPrefix: "com.amazonaws.foo",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &InputService4ProtocolTest{service}
}

// newRequest creates a new request for a InputService4ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService4ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService4TestCaseOperation1Request generates a request for the InputService4TestCaseOperation1 operation.
func (c *InputService4ProtocolTest) InputService4TestCaseOperation1Request(input *InputService4TestShapeInputShape) (req *aws.Request, output *InputService4TestShapeInputService4TestCaseOperation1Output) {

	if opInputService4TestCaseOperation1 == nil {
		opInputService4TestCaseOperation1 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
		}
	}

	if input == nil {
		input = &InputService4TestShapeInputShape{}
	}

	req = c.newRequest(opInputService4TestCaseOperation1, input, output)
	output = &InputService4TestShapeInputService4TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService4ProtocolTest) InputService4TestCaseOperation1(input *InputService4TestShapeInputShape) (*InputService4TestShapeInputService4TestCaseOperation1Output, error) {
	req, out := c.InputService4TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService4TestCaseOperation1 *aws.Operation

type InputService4TestShapeInputService4TestCaseOperation1Output struct {
	metadataInputService4TestShapeInputService4TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService4TestShapeInputService4TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService4TestShapeInputShape struct {
	ListParam [][]byte `type:"list"`

	metadataInputService4TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService4TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService5ProtocolTest is a client for InputService5ProtocolTest.
type InputService5ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService5ProtocolTest client.
func NewInputService5ProtocolTest(config *aws.Config) *InputService5ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "inputservice5protocoltest",
		APIVersion:   "",
		JSONVersion:  "1.1",
		TargetPrefix: "com.amazonaws.foo",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &InputService5ProtocolTest{service}
}

// newRequest creates a new request for a InputService5ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService5ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService5TestCaseOperation1Request generates a request for the InputService5TestCaseOperation1 operation.
func (c *InputService5ProtocolTest) InputService5TestCaseOperation1Request(input *InputService5TestShapeInputShape) (req *aws.Request, output *InputService5TestShapeInputService5TestCaseOperation1Output) {

	if opInputService5TestCaseOperation1 == nil {
		opInputService5TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService5TestShapeInputShape{}
	}

	req = c.newRequest(opInputService5TestCaseOperation1, input, output)
	output = &InputService5TestShapeInputService5TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService5ProtocolTest) InputService5TestCaseOperation1(input *InputService5TestShapeInputShape) (*InputService5TestShapeInputService5TestCaseOperation1Output, error) {
	req, out := c.InputService5TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService5TestCaseOperation1 *aws.Operation

// InputService5TestCaseOperation2Request generates a request for the InputService5TestCaseOperation2 operation.
func (c *InputService5ProtocolTest) InputService5TestCaseOperation2Request(input *InputService5TestShapeInputShape) (req *aws.Request, output *InputService5TestShapeInputService5TestCaseOperation2Output) {

	if opInputService5TestCaseOperation2 == nil {
		opInputService5TestCaseOperation2 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService5TestShapeInputShape{}
	}

	req = c.newRequest(opInputService5TestCaseOperation2, input, output)
	output = &InputService5TestShapeInputService5TestCaseOperation2Output{}
	req.Data = output
	return
}

func (c *InputService5ProtocolTest) InputService5TestCaseOperation2(input *InputService5TestShapeInputShape) (*InputService5TestShapeInputService5TestCaseOperation2Output, error) {
	req, out := c.InputService5TestCaseOperation2Request(input)
	err := req.Send()
	return out, err
}

var opInputService5TestCaseOperation2 *aws.Operation

// InputService5TestCaseOperation3Request generates a request for the InputService5TestCaseOperation3 operation.
func (c *InputService5ProtocolTest) InputService5TestCaseOperation3Request(input *InputService5TestShapeInputShape) (req *aws.Request, output *InputService5TestShapeInputService5TestCaseOperation3Output) {

	if opInputService5TestCaseOperation3 == nil {
		opInputService5TestCaseOperation3 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService5TestShapeInputShape{}
	}

	req = c.newRequest(opInputService5TestCaseOperation3, input, output)
	output = &InputService5TestShapeInputService5TestCaseOperation3Output{}
	req.Data = output
	return
}

func (c *InputService5ProtocolTest) InputService5TestCaseOperation3(input *InputService5TestShapeInputShape) (*InputService5TestShapeInputService5TestCaseOperation3Output, error) {
	req, out := c.InputService5TestCaseOperation3Request(input)
	err := req.Send()
	return out, err
}

var opInputService5TestCaseOperation3 *aws.Operation

// InputService5TestCaseOperation4Request generates a request for the InputService5TestCaseOperation4 operation.
func (c *InputService5ProtocolTest) InputService5TestCaseOperation4Request(input *InputService5TestShapeInputShape) (req *aws.Request, output *InputService5TestShapeInputService5TestCaseOperation4Output) {

	if opInputService5TestCaseOperation4 == nil {
		opInputService5TestCaseOperation4 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService5TestShapeInputShape{}
	}

	req = c.newRequest(opInputService5TestCaseOperation4, input, output)
	output = &InputService5TestShapeInputService5TestCaseOperation4Output{}
	req.Data = output
	return
}

func (c *InputService5ProtocolTest) InputService5TestCaseOperation4(input *InputService5TestShapeInputShape) (*InputService5TestShapeInputService5TestCaseOperation4Output, error) {
	req, out := c.InputService5TestCaseOperation4Request(input)
	err := req.Send()
	return out, err
}

var opInputService5TestCaseOperation4 *aws.Operation

// InputService5TestCaseOperation5Request generates a request for the InputService5TestCaseOperation5 operation.
func (c *InputService5ProtocolTest) InputService5TestCaseOperation5Request(input *InputService5TestShapeInputShape) (req *aws.Request, output *InputService5TestShapeInputService5TestCaseOperation5Output) {

	if opInputService5TestCaseOperation5 == nil {
		opInputService5TestCaseOperation5 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService5TestShapeInputShape{}
	}

	req = c.newRequest(opInputService5TestCaseOperation5, input, output)
	output = &InputService5TestShapeInputService5TestCaseOperation5Output{}
	req.Data = output
	return
}

func (c *InputService5ProtocolTest) InputService5TestCaseOperation5(input *InputService5TestShapeInputShape) (*InputService5TestShapeInputService5TestCaseOperation5Output, error) {
	req, out := c.InputService5TestCaseOperation5Request(input)
	err := req.Send()
	return out, err
}

var opInputService5TestCaseOperation5 *aws.Operation

// InputService5TestCaseOperation6Request generates a request for the InputService5TestCaseOperation6 operation.
func (c *InputService5ProtocolTest) InputService5TestCaseOperation6Request(input *InputService5TestShapeInputShape) (req *aws.Request, output *InputService5TestShapeInputService5TestCaseOperation6Output) {

	if opInputService5TestCaseOperation6 == nil {
		opInputService5TestCaseOperation6 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &InputService5TestShapeInputShape{}
	}

	req = c.newRequest(opInputService5TestCaseOperation6, input, output)
	output = &InputService5TestShapeInputService5TestCaseOperation6Output{}
	req.Data = output
	return
}

func (c *InputService5ProtocolTest) InputService5TestCaseOperation6(input *InputService5TestShapeInputShape) (*InputService5TestShapeInputService5TestCaseOperation6Output, error) {
	req, out := c.InputService5TestCaseOperation6Request(input)
	err := req.Send()
	return out, err
}

var opInputService5TestCaseOperation6 *aws.Operation

type InputService5TestShapeInputService5TestCaseOperation1Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputService5TestCaseOperation2Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation2Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation2Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputService5TestCaseOperation3Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation3Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation3Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputService5TestCaseOperation4Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation4Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation4Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputService5TestCaseOperation5Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation5Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation5Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputService5TestCaseOperation6Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation6Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation6Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputShape struct {
	RecursiveStruct *InputService5TestShapeRecursiveStructType `type:"structure"`

	metadataInputService5TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeRecursiveStructType struct {
	NoRecurse *string `type:"string"`

	RecursiveList []*InputService5TestShapeRecursiveStructType `type:"list"`

	RecursiveMap map[string]*InputService5TestShapeRecursiveStructType `type:"map"`

	RecursiveStruct *InputService5TestShapeRecursiveStructType `type:"structure"`

	metadataInputService5TestShapeRecursiveStructType `json:"-" xml:"-"`
}

type metadataInputService5TestShapeRecursiveStructType struct {
	SDKShapeTraits bool `type:"structure"`
}

//
// Tests begin here
//

func TestInputService1ProtocolTestScalarMembersCase1(t *testing.T) {
	svc := NewInputService1ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService1TestShapeInputShape{
		Name: aws.String("myname"),
	}
	req, _ := svc.InputService1TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"Name":"myname"}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService2ProtocolTestTimestampValuesCase1(t *testing.T) {
	svc := NewInputService2ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService2TestShapeInputShape{
		TimeArg: aws.Time(time.Unix(1422172800, 0)),
	}
	req, _ := svc.InputService2TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"TimeArg":1422172800}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService3ProtocolTestBase64EncodedBlobsCase1(t *testing.T) {
	svc := NewInputService3ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService3TestShapeInputShape{
		BlobArg: []byte("foo"),
	}
	req, _ := svc.InputService3TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"BlobArg":"Zm9v"}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService3ProtocolTestBase64EncodedBlobsCase2(t *testing.T) {
	svc := NewInputService3ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService3TestShapeInputShape{
		BlobMap: map[string][]byte{
			"key1": []byte("foo"),
			"key2": []byte("bar"),
		},
	}
	req, _ := svc.InputService3TestCaseOperation2Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"BlobMap":{"key1":"Zm9v","key2":"YmFy"}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService4ProtocolTestNestedBlobsCase1(t *testing.T) {
	svc := NewInputService4ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService4TestShapeInputShape{
		ListParam: [][]byte{
			[]byte("foo"),
			[]byte("bar"),
		},
	}
	req, _ := svc.InputService4TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"ListParam":["Zm9v","YmFy"]}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService5ProtocolTestRecursiveShapesCase1(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		RecursiveStruct: &InputService5TestShapeRecursiveStructType{
			NoRecurse: aws.String("foo"),
		},
	}
	req, _ := svc.InputService5TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"NoRecurse":"foo"}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService5ProtocolTestRecursiveShapesCase2(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		RecursiveStruct: &InputService5TestShapeRecursiveStructType{
			RecursiveStruct: &InputService5TestShapeRecursiveStructType{
				NoRecurse: aws.String("foo"),
			},
		},
	}
	req, _ := svc.InputService5TestCaseOperation2Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveStruct":{"NoRecurse":"foo"}}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService5ProtocolTestRecursiveShapesCase3(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		RecursiveStruct: &InputService5TestShapeRecursiveStructType{
			RecursiveStruct: &InputService5TestShapeRecursiveStructType{
				RecursiveStruct: &InputService5TestShapeRecursiveStructType{
					RecursiveStruct: &InputService5TestShapeRecursiveStructType{
						NoRecurse: aws.String("foo"),
					},
				},
			},
		},
	}
	req, _ := svc.InputService5TestCaseOperation3Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveStruct":{"RecursiveStruct":{"RecursiveStruct":{"NoRecurse":"foo"}}}}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService5ProtocolTestRecursiveShapesCase4(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		RecursiveStruct: &InputService5TestShapeRecursiveStructType{
			RecursiveList: []*InputService5TestShapeRecursiveStructType{
				{
					NoRecurse: aws.String("foo"),
				},
				{
					NoRecurse: aws.String("bar"),
				},
			},
		},
	}
	req, _ := svc.InputService5TestCaseOperation4Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveList":[{"NoRecurse":"foo"},{"NoRecurse":"bar"}]}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService5ProtocolTestRecursiveShapesCase5(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		RecursiveStruct: &InputService5TestShapeRecursiveStructType{
			RecursiveList: []*InputService5TestShapeRecursiveStructType{
				{
					NoRecurse: aws.String("foo"),
				},
				{
					RecursiveStruct: &InputService5TestShapeRecursiveStructType{
						NoRecurse: aws.String("bar"),
					},
				},
			},
		},
	}
	req, _ := svc.InputService5TestCaseOperation5Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveList":[{"NoRecurse":"foo"},{"RecursiveStruct":{"NoRecurse":"bar"}}]}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}

func TestInputService5ProtocolTestRecursiveShapesCase6(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		RecursiveStruct: &InputService5TestShapeRecursiveStructType{
			RecursiveMap: map[string]*InputService5TestShapeRecursiveStructType{
				"bar": {
					NoRecurse: aws.String("bar"),
				},
				"foo": {
					NoRecurse: aws.String("foo"),
				},
			},
		},
	}
	req, _ := svc.InputService5TestCaseOperation6Request(input)
	r := req.HTTPRequest

	// build request
	jsonrpc.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveMap":{"bar":{"NoRecurse":"bar"},"foo":{"NoRecurse":"foo"}}}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/", r.URL.String())

	// assert headers
	assert.Equal(t, "application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	assert.Equal(t, "com.amazonaws.foo.OperationName", r.Header.Get("X-Amz-Target"))

}
