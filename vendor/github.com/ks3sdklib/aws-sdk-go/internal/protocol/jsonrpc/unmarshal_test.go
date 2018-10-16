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

// OutputService1ProtocolTest is a client for OutputService1ProtocolTest.
type OutputService1ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService1ProtocolTest client.
func NewOutputService1ProtocolTest(config *aws.Config) *OutputService1ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice1protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService1ProtocolTest{service}
}

// newRequest creates a new request for a OutputService1ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService1ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService1TestCaseOperation1Request generates a request for the OutputService1TestCaseOperation1 operation.
func (c *OutputService1ProtocolTest) OutputService1TestCaseOperation1Request(input *OutputService1TestShapeOutputService1TestCaseOperation1Input) (req *aws.Request, output *OutputService1TestShapeOutputShape) {

	if opOutputService1TestCaseOperation1 == nil {
		opOutputService1TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService1TestShapeOutputService1TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService1TestCaseOperation1, input, output)
	output = &OutputService1TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService1ProtocolTest) OutputService1TestCaseOperation1(input *OutputService1TestShapeOutputService1TestCaseOperation1Input) (*OutputService1TestShapeOutputShape, error) {
	req, out := c.OutputService1TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService1TestCaseOperation1 *aws.Operation

type OutputService1TestShapeOutputService1TestCaseOperation1Input struct {
	metadataOutputService1TestShapeOutputService1TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService1TestShapeOutputService1TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService1TestShapeOutputShape struct {
	metadataOutputService1TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService1TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// OutputService2ProtocolTest is a client for OutputService2ProtocolTest.
type OutputService2ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService2ProtocolTest client.
func NewOutputService2ProtocolTest(config *aws.Config) *OutputService2ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice2protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService2ProtocolTest{service}
}

// newRequest creates a new request for a OutputService2ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService2ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService2TestCaseOperation1Request generates a request for the OutputService2TestCaseOperation1 operation.
func (c *OutputService2ProtocolTest) OutputService2TestCaseOperation1Request(input *OutputService2TestShapeOutputService2TestCaseOperation1Input) (req *aws.Request, output *OutputService2TestShapeOutputShape) {

	if opOutputService2TestCaseOperation1 == nil {
		opOutputService2TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService2TestShapeOutputService2TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService2TestCaseOperation1, input, output)
	output = &OutputService2TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService2ProtocolTest) OutputService2TestCaseOperation1(input *OutputService2TestShapeOutputService2TestCaseOperation1Input) (*OutputService2TestShapeOutputShape, error) {
	req, out := c.OutputService2TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService2TestCaseOperation1 *aws.Operation

type OutputService2TestShapeOutputService2TestCaseOperation1Input struct {
	metadataOutputService2TestShapeOutputService2TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService2TestShapeOutputService2TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService2TestShapeOutputShape struct {
	Char *string `type:"character"`

	Double *float64 `type:"double"`

	FalseBool *bool `type:"boolean"`

	Float *float64 `type:"float"`

	Long *int64 `type:"long"`

	Num *int64 `type:"integer"`

	Str *string `type:"string"`

	TrueBool *bool `type:"boolean"`

	metadataOutputService2TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService2TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// OutputService3ProtocolTest is a client for OutputService3ProtocolTest.
type OutputService3ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService3ProtocolTest client.
func NewOutputService3ProtocolTest(config *aws.Config) *OutputService3ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice3protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService3ProtocolTest{service}
}

// newRequest creates a new request for a OutputService3ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService3ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService3TestCaseOperation1Request generates a request for the OutputService3TestCaseOperation1 operation.
func (c *OutputService3ProtocolTest) OutputService3TestCaseOperation1Request(input *OutputService3TestShapeOutputService3TestCaseOperation1Input) (req *aws.Request, output *OutputService3TestShapeOutputShape) {

	if opOutputService3TestCaseOperation1 == nil {
		opOutputService3TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService3TestShapeOutputService3TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService3TestCaseOperation1, input, output)
	output = &OutputService3TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService3ProtocolTest) OutputService3TestCaseOperation1(input *OutputService3TestShapeOutputService3TestCaseOperation1Input) (*OutputService3TestShapeOutputShape, error) {
	req, out := c.OutputService3TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService3TestCaseOperation1 *aws.Operation

type OutputService3TestShapeBlobContainer struct {
	Foo []byte `locationName:"foo" type:"blob"`

	metadataOutputService3TestShapeBlobContainer `json:"-" xml:"-"`
}

type metadataOutputService3TestShapeBlobContainer struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService3TestShapeOutputService3TestCaseOperation1Input struct {
	metadataOutputService3TestShapeOutputService3TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService3TestShapeOutputService3TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService3TestShapeOutputShape struct {
	BlobMember []byte `type:"blob"`

	StructMember *OutputService3TestShapeBlobContainer `type:"structure"`

	metadataOutputService3TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService3TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// OutputService4ProtocolTest is a client for OutputService4ProtocolTest.
type OutputService4ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService4ProtocolTest client.
func NewOutputService4ProtocolTest(config *aws.Config) *OutputService4ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice4protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService4ProtocolTest{service}
}

// newRequest creates a new request for a OutputService4ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService4ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService4TestCaseOperation1Request generates a request for the OutputService4TestCaseOperation1 operation.
func (c *OutputService4ProtocolTest) OutputService4TestCaseOperation1Request(input *OutputService4TestShapeOutputService4TestCaseOperation1Input) (req *aws.Request, output *OutputService4TestShapeOutputShape) {

	if opOutputService4TestCaseOperation1 == nil {
		opOutputService4TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService4TestShapeOutputService4TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService4TestCaseOperation1, input, output)
	output = &OutputService4TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService4ProtocolTest) OutputService4TestCaseOperation1(input *OutputService4TestShapeOutputService4TestCaseOperation1Input) (*OutputService4TestShapeOutputShape, error) {
	req, out := c.OutputService4TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService4TestCaseOperation1 *aws.Operation

type OutputService4TestShapeOutputService4TestCaseOperation1Input struct {
	metadataOutputService4TestShapeOutputService4TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService4TestShapeOutputService4TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService4TestShapeOutputShape struct {
	StructMember *OutputService4TestShapeTimeContainer `type:"structure"`

	TimeMember *time.Time `type:"timestamp" timestampFormat:"unix"`

	metadataOutputService4TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService4TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService4TestShapeTimeContainer struct {
	Foo *time.Time `locationName:"foo" type:"timestamp" timestampFormat:"unix"`

	metadataOutputService4TestShapeTimeContainer `json:"-" xml:"-"`
}

type metadataOutputService4TestShapeTimeContainer struct {
	SDKShapeTraits bool `type:"structure"`
}

// OutputService5ProtocolTest is a client for OutputService5ProtocolTest.
type OutputService5ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService5ProtocolTest client.
func NewOutputService5ProtocolTest(config *aws.Config) *OutputService5ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice5protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService5ProtocolTest{service}
}

// newRequest creates a new request for a OutputService5ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService5ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService5TestCaseOperation1Request generates a request for the OutputService5TestCaseOperation1 operation.
func (c *OutputService5ProtocolTest) OutputService5TestCaseOperation1Request(input *OutputService5TestShapeOutputService5TestCaseOperation1Input) (req *aws.Request, output *OutputService5TestShapeOutputShape) {

	if opOutputService5TestCaseOperation1 == nil {
		opOutputService5TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService5TestShapeOutputService5TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService5TestCaseOperation1, input, output)
	output = &OutputService5TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService5ProtocolTest) OutputService5TestCaseOperation1(input *OutputService5TestShapeOutputService5TestCaseOperation1Input) (*OutputService5TestShapeOutputShape, error) {
	req, out := c.OutputService5TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService5TestCaseOperation1 *aws.Operation

type OutputService5TestShapeOutputService5TestCaseOperation1Input struct {
	metadataOutputService5TestShapeOutputService5TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService5TestShapeOutputService5TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService5TestShapeOutputShape struct {
	ListMember []*string `type:"list"`

	metadataOutputService5TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService5TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// OutputService6ProtocolTest is a client for OutputService6ProtocolTest.
type OutputService6ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService6ProtocolTest client.
func NewOutputService6ProtocolTest(config *aws.Config) *OutputService6ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice6protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService6ProtocolTest{service}
}

// newRequest creates a new request for a OutputService6ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService6ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService6TestCaseOperation1Request generates a request for the OutputService6TestCaseOperation1 operation.
func (c *OutputService6ProtocolTest) OutputService6TestCaseOperation1Request(input *OutputService6TestShapeOutputService6TestCaseOperation1Input) (req *aws.Request, output *OutputService6TestShapeOutputShape) {

	if opOutputService6TestCaseOperation1 == nil {
		opOutputService6TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService6TestShapeOutputService6TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService6TestCaseOperation1, input, output)
	output = &OutputService6TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService6ProtocolTest) OutputService6TestCaseOperation1(input *OutputService6TestShapeOutputService6TestCaseOperation1Input) (*OutputService6TestShapeOutputShape, error) {
	req, out := c.OutputService6TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService6TestCaseOperation1 *aws.Operation

type OutputService6TestShapeOutputService6TestCaseOperation1Input struct {
	metadataOutputService6TestShapeOutputService6TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService6TestShapeOutputService6TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService6TestShapeOutputShape struct {
	MapMember map[string][]*int64 `type:"map"`

	metadataOutputService6TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService6TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// OutputService7ProtocolTest is a client for OutputService7ProtocolTest.
type OutputService7ProtocolTest struct {
	*aws.Service
}

// New returns a new OutputService7ProtocolTest client.
func NewOutputService7ProtocolTest(config *aws.Config) *OutputService7ProtocolTest {
	service := &aws.Service{
		Config:       aws.DefaultConfig.Merge(config),
		ServiceName:  "outputservice7protocoltest",
		APIVersion:   "",
		JSONVersion:  "",
		TargetPrefix: "",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(jsonrpc.Build)
	service.Handlers.Unmarshal.PushBack(jsonrpc.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(jsonrpc.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(jsonrpc.UnmarshalError)

	return &OutputService7ProtocolTest{service}
}

// newRequest creates a new request for a OutputService7ProtocolTest operation and runs any
// custom request initialization.
func (c *OutputService7ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// OutputService7TestCaseOperation1Request generates a request for the OutputService7TestCaseOperation1 operation.
func (c *OutputService7ProtocolTest) OutputService7TestCaseOperation1Request(input *OutputService7TestShapeOutputService7TestCaseOperation1Input) (req *aws.Request, output *OutputService7TestShapeOutputShape) {

	if opOutputService7TestCaseOperation1 == nil {
		opOutputService7TestCaseOperation1 = &aws.Operation{
			Name: "OperationName",
		}
	}

	if input == nil {
		input = &OutputService7TestShapeOutputService7TestCaseOperation1Input{}
	}

	req = c.newRequest(opOutputService7TestCaseOperation1, input, output)
	output = &OutputService7TestShapeOutputShape{}
	req.Data = output
	return
}

func (c *OutputService7ProtocolTest) OutputService7TestCaseOperation1(input *OutputService7TestShapeOutputService7TestCaseOperation1Input) (*OutputService7TestShapeOutputShape, error) {
	req, out := c.OutputService7TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opOutputService7TestCaseOperation1 *aws.Operation

type OutputService7TestShapeOutputService7TestCaseOperation1Input struct {
	metadataOutputService7TestShapeOutputService7TestCaseOperation1Input `json:"-" xml:"-"`
}

type metadataOutputService7TestShapeOutputService7TestCaseOperation1Input struct {
	SDKShapeTraits bool `type:"structure"`
}

type OutputService7TestShapeOutputShape struct {
	StrType *string `type:"string"`

	metadataOutputService7TestShapeOutputShape `json:"-" xml:"-"`
}

type metadataOutputService7TestShapeOutputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

//
// Tests begin here
//

func TestOutputService1ProtocolTestEmptyBodyCase1(t *testing.T) {
	svc := NewOutputService1ProtocolTest(nil)

	buf := bytes.NewReader([]byte(""))
	req, out := svc.OutputService1TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used

}

func TestOutputService2ProtocolTestScalarMembersCase1(t *testing.T) {
	svc := NewOutputService2ProtocolTest(nil)

	buf := bytes.NewReader([]byte("{\"Str\": \"myname\", \"Num\": 123, \"FalseBool\": false, \"TrueBool\": true, \"Float\": 1.2, \"Double\": 1.3, \"Long\": 200, \"Char\": \"a\"}"))
	req, out := svc.OutputService2TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used
	assert.Equal(t, "a", *out.Char)
	assert.Equal(t, 1.3, *out.Double)
	assert.Equal(t, false, *out.FalseBool)
	assert.Equal(t, 1.2, *out.Float)
	assert.Equal(t, int64(200), *out.Long)
	assert.Equal(t, int64(123), *out.Num)
	assert.Equal(t, "myname", *out.Str)
	assert.Equal(t, true, *out.TrueBool)

}

func TestOutputService3ProtocolTestBlobMembersCase1(t *testing.T) {
	svc := NewOutputService3ProtocolTest(nil)

	buf := bytes.NewReader([]byte("{\"BlobMember\": \"aGkh\", \"StructMember\": {\"foo\": \"dGhlcmUh\"}}"))
	req, out := svc.OutputService3TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used
	assert.Equal(t, "hi!", string(out.BlobMember))
	assert.Equal(t, "there!", string(out.StructMember.Foo))

}

func TestOutputService4ProtocolTestTimestampMembersCase1(t *testing.T) {
	svc := NewOutputService4ProtocolTest(nil)

	buf := bytes.NewReader([]byte("{\"TimeMember\": 1398796238, \"StructMember\": {\"foo\": 1398796238}}"))
	req, out := svc.OutputService4TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used
	assert.Equal(t, time.Unix(1.398796238e+09, 0).UTC().String(), out.StructMember.Foo.String())
	assert.Equal(t, time.Unix(1.398796238e+09, 0).UTC().String(), out.TimeMember.String())

}

func TestOutputService5ProtocolTestListsCase1(t *testing.T) {
	svc := NewOutputService5ProtocolTest(nil)

	buf := bytes.NewReader([]byte("{\"ListMember\": [\"a\", \"b\"]}"))
	req, out := svc.OutputService5TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used
	assert.Equal(t, "a", *out.ListMember[0])
	assert.Equal(t, "b", *out.ListMember[1])

}

func TestOutputService6ProtocolTestMapsCase1(t *testing.T) {
	svc := NewOutputService6ProtocolTest(nil)

	buf := bytes.NewReader([]byte("{\"MapMember\": {\"a\": [1, 2], \"b\": [3, 4]}}"))
	req, out := svc.OutputService6TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used
	assert.Equal(t, int64(1), *out.MapMember["a"][0])
	assert.Equal(t, int64(2), *out.MapMember["a"][1])
	assert.Equal(t, int64(3), *out.MapMember["b"][0])
	assert.Equal(t, int64(4), *out.MapMember["b"][1])

}

func TestOutputService7ProtocolTestIgnoresExtraDataCase1(t *testing.T) {
	svc := NewOutputService7ProtocolTest(nil)

	buf := bytes.NewReader([]byte("{\"foo\": \"bar\"}"))
	req, out := svc.OutputService7TestCaseOperation1Request(nil)
	req.HTTPResponse = &http.Response{StatusCode: 200, Body: ioutil.NopCloser(buf), Header: http.Header{}}

	// set headers

	// unmarshal response
	jsonrpc.UnmarshalMeta(req)
	jsonrpc.Unmarshal(req)
	assert.NoError(t, req.Error)

	// assert response
	assert.NotNil(t, out) // ensure out variable is used

}
