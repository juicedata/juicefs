package restjson_test

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
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/restjson"
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
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice1protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

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
			HTTPMethod: "GET",
			HTTPPath:   "/2014-01-01/jobsByPipeline/{PipelineId}",
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
	PipelineId *string `location:"uri" type:"string"`

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
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice2protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

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
			Name:       "OperationName",
			HTTPMethod: "GET",
			HTTPPath:   "/2014-01-01/jobsByPipeline/{PipelineId}",
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
	Foo *string `location:"uri" locationName:"PipelineId" type:"string"`

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
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice3protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

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
			Name:       "OperationName",
			HTTPMethod: "GET",
			HTTPPath:   "/2014-01-01/jobsByPipeline/{PipelineId}",
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

type InputService3TestShapeInputService3TestCaseOperation1Output struct {
	metadataInputService3TestShapeInputService3TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService3TestShapeInputService3TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService3TestShapeInputShape struct {
	Ascending *string `location:"querystring" locationName:"Ascending" type:"string"`

	PageToken *string `location:"querystring" locationName:"PageToken" type:"string"`

	PipelineId *string `location:"uri" locationName:"PipelineId" type:"string"`

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
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice4protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

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
			HTTPPath:   "/2014-01-01/jobsByPipeline/{PipelineId}",
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
	Ascending *string `location:"querystring" locationName:"Ascending" type:"string"`

	Config *InputService4TestShapeStructType `type:"structure"`

	PageToken *string `location:"querystring" locationName:"PageToken" type:"string"`

	PipelineId *string `location:"uri" locationName:"PipelineId" type:"string"`

	metadataInputService4TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService4TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService4TestShapeStructType struct {
	A *string `type:"string"`

	B *string `type:"string"`

	metadataInputService4TestShapeStructType `json:"-" xml:"-"`
}

type metadataInputService4TestShapeStructType struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService5ProtocolTest is a client for InputService5ProtocolTest.
type InputService5ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService5ProtocolTest client.
func NewInputService5ProtocolTest(config *aws.Config) *InputService5ProtocolTest {
	service := &aws.Service{
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice5protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

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
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/2014-01-01/jobsByPipeline/{PipelineId}",
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

type InputService5TestShapeInputService5TestCaseOperation1Output struct {
	metadataInputService5TestShapeInputService5TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputService5TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeInputShape struct {
	Ascending *string `location:"querystring" locationName:"Ascending" type:"string"`

	Checksum *string `location:"header" locationName:"x-amz-checksum" type:"string"`

	Config *InputService5TestShapeStructType `type:"structure"`

	PageToken *string `location:"querystring" locationName:"PageToken" type:"string"`

	PipelineId *string `location:"uri" locationName:"PipelineId" type:"string"`

	metadataInputService5TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService5TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService5TestShapeStructType struct {
	A *string `type:"string"`

	B *string `type:"string"`

	metadataInputService5TestShapeStructType `json:"-" xml:"-"`
}

type metadataInputService5TestShapeStructType struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService6ProtocolTest is a client for InputService6ProtocolTest.
type InputService6ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService6ProtocolTest client.
func NewInputService6ProtocolTest(config *aws.Config) *InputService6ProtocolTest {
	service := &aws.Service{
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice6protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

	return &InputService6ProtocolTest{service}
}

// newRequest creates a new request for a InputService6ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService6ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService6TestCaseOperation1Request generates a request for the InputService6TestCaseOperation1 operation.
func (c *InputService6ProtocolTest) InputService6TestCaseOperation1Request(input *InputService6TestShapeInputShape) (req *aws.Request, output *InputService6TestShapeInputService6TestCaseOperation1Output) {

	if opInputService6TestCaseOperation1 == nil {
		opInputService6TestCaseOperation1 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/2014-01-01/vaults/{vaultName}/archives",
		}
	}

	if input == nil {
		input = &InputService6TestShapeInputShape{}
	}

	req = c.newRequest(opInputService6TestCaseOperation1, input, output)
	output = &InputService6TestShapeInputService6TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService6ProtocolTest) InputService6TestCaseOperation1(input *InputService6TestShapeInputShape) (*InputService6TestShapeInputService6TestCaseOperation1Output, error) {
	req, out := c.InputService6TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService6TestCaseOperation1 *aws.Operation

type InputService6TestShapeInputService6TestCaseOperation1Output struct {
	metadataInputService6TestShapeInputService6TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService6TestShapeInputService6TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService6TestShapeInputShape struct {
	Body io.ReadSeeker `locationName:"body" type:"blob"`

	Checksum *string `location:"header" locationName:"x-amz-sha256-tree-hash" type:"string"`

	VaultName *string `location:"uri" locationName:"vaultName" type:"string" required:"true"`

	metadataInputService6TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService6TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure" payload:"Body"`
}

// InputService7ProtocolTest is a client for InputService7ProtocolTest.
type InputService7ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService7ProtocolTest client.
func NewInputService7ProtocolTest(config *aws.Config) *InputService7ProtocolTest {
	service := &aws.Service{
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice7protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

	return &InputService7ProtocolTest{service}
}

// newRequest creates a new request for a InputService7ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService7ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService7TestCaseOperation1Request generates a request for the InputService7TestCaseOperation1 operation.
func (c *InputService7ProtocolTest) InputService7TestCaseOperation1Request(input *InputService7TestShapeInputShape) (req *aws.Request, output *InputService7TestShapeInputService7TestCaseOperation1Output) {

	if opInputService7TestCaseOperation1 == nil {
		opInputService7TestCaseOperation1 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService7TestShapeInputShape{}
	}

	req = c.newRequest(opInputService7TestCaseOperation1, input, output)
	output = &InputService7TestShapeInputService7TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService7ProtocolTest) InputService7TestCaseOperation1(input *InputService7TestShapeInputShape) (*InputService7TestShapeInputService7TestCaseOperation1Output, error) {
	req, out := c.InputService7TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService7TestCaseOperation1 *aws.Operation

// InputService7TestCaseOperation2Request generates a request for the InputService7TestCaseOperation2 operation.
func (c *InputService7ProtocolTest) InputService7TestCaseOperation2Request(input *InputService7TestShapeInputShape) (req *aws.Request, output *InputService7TestShapeInputService7TestCaseOperation2Output) {

	if opInputService7TestCaseOperation2 == nil {
		opInputService7TestCaseOperation2 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path?abc=mno",
		}
	}

	if input == nil {
		input = &InputService7TestShapeInputShape{}
	}

	req = c.newRequest(opInputService7TestCaseOperation2, input, output)
	output = &InputService7TestShapeInputService7TestCaseOperation2Output{}
	req.Data = output
	return
}

func (c *InputService7ProtocolTest) InputService7TestCaseOperation2(input *InputService7TestShapeInputShape) (*InputService7TestShapeInputService7TestCaseOperation2Output, error) {
	req, out := c.InputService7TestCaseOperation2Request(input)
	err := req.Send()
	return out, err
}

var opInputService7TestCaseOperation2 *aws.Operation

type InputService7TestShapeInputService7TestCaseOperation1Output struct {
	metadataInputService7TestShapeInputService7TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService7TestShapeInputService7TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService7TestShapeInputService7TestCaseOperation2Output struct {
	metadataInputService7TestShapeInputService7TestCaseOperation2Output `json:"-" xml:"-"`
}

type metadataInputService7TestShapeInputService7TestCaseOperation2Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService7TestShapeInputShape struct {
	Foo *string `location:"querystring" locationName:"param-name" type:"string"`

	metadataInputService7TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService7TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService8ProtocolTest is a client for InputService8ProtocolTest.
type InputService8ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService8ProtocolTest client.
func NewInputService8ProtocolTest(config *aws.Config) *InputService8ProtocolTest {
	service := &aws.Service{
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice8protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

	return &InputService8ProtocolTest{service}
}

// newRequest creates a new request for a InputService8ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService8ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService8TestCaseOperation1Request generates a request for the InputService8TestCaseOperation1 operation.
func (c *InputService8ProtocolTest) InputService8TestCaseOperation1Request(input *InputService8TestShapeInputShape) (req *aws.Request, output *InputService8TestShapeInputService8TestCaseOperation1Output) {

	if opInputService8TestCaseOperation1 == nil {
		opInputService8TestCaseOperation1 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService8TestShapeInputShape{}
	}

	req = c.newRequest(opInputService8TestCaseOperation1, input, output)
	output = &InputService8TestShapeInputService8TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService8ProtocolTest) InputService8TestCaseOperation1(input *InputService8TestShapeInputShape) (*InputService8TestShapeInputService8TestCaseOperation1Output, error) {
	req, out := c.InputService8TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService8TestCaseOperation1 *aws.Operation

// InputService8TestCaseOperation2Request generates a request for the InputService8TestCaseOperation2 operation.
func (c *InputService8ProtocolTest) InputService8TestCaseOperation2Request(input *InputService8TestShapeInputShape) (req *aws.Request, output *InputService8TestShapeInputService8TestCaseOperation2Output) {

	if opInputService8TestCaseOperation2 == nil {
		opInputService8TestCaseOperation2 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService8TestShapeInputShape{}
	}

	req = c.newRequest(opInputService8TestCaseOperation2, input, output)
	output = &InputService8TestShapeInputService8TestCaseOperation2Output{}
	req.Data = output
	return
}

func (c *InputService8ProtocolTest) InputService8TestCaseOperation2(input *InputService8TestShapeInputShape) (*InputService8TestShapeInputService8TestCaseOperation2Output, error) {
	req, out := c.InputService8TestCaseOperation2Request(input)
	err := req.Send()
	return out, err
}

var opInputService8TestCaseOperation2 *aws.Operation

// InputService8TestCaseOperation3Request generates a request for the InputService8TestCaseOperation3 operation.
func (c *InputService8ProtocolTest) InputService8TestCaseOperation3Request(input *InputService8TestShapeInputShape) (req *aws.Request, output *InputService8TestShapeInputService8TestCaseOperation3Output) {

	if opInputService8TestCaseOperation3 == nil {
		opInputService8TestCaseOperation3 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService8TestShapeInputShape{}
	}

	req = c.newRequest(opInputService8TestCaseOperation3, input, output)
	output = &InputService8TestShapeInputService8TestCaseOperation3Output{}
	req.Data = output
	return
}

func (c *InputService8ProtocolTest) InputService8TestCaseOperation3(input *InputService8TestShapeInputShape) (*InputService8TestShapeInputService8TestCaseOperation3Output, error) {
	req, out := c.InputService8TestCaseOperation3Request(input)
	err := req.Send()
	return out, err
}

var opInputService8TestCaseOperation3 *aws.Operation

// InputService8TestCaseOperation4Request generates a request for the InputService8TestCaseOperation4 operation.
func (c *InputService8ProtocolTest) InputService8TestCaseOperation4Request(input *InputService8TestShapeInputShape) (req *aws.Request, output *InputService8TestShapeInputService8TestCaseOperation4Output) {

	if opInputService8TestCaseOperation4 == nil {
		opInputService8TestCaseOperation4 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService8TestShapeInputShape{}
	}

	req = c.newRequest(opInputService8TestCaseOperation4, input, output)
	output = &InputService8TestShapeInputService8TestCaseOperation4Output{}
	req.Data = output
	return
}

func (c *InputService8ProtocolTest) InputService8TestCaseOperation4(input *InputService8TestShapeInputShape) (*InputService8TestShapeInputService8TestCaseOperation4Output, error) {
	req, out := c.InputService8TestCaseOperation4Request(input)
	err := req.Send()
	return out, err
}

var opInputService8TestCaseOperation4 *aws.Operation

// InputService8TestCaseOperation5Request generates a request for the InputService8TestCaseOperation5 operation.
func (c *InputService8ProtocolTest) InputService8TestCaseOperation5Request(input *InputService8TestShapeInputShape) (req *aws.Request, output *InputService8TestShapeInputService8TestCaseOperation5Output) {

	if opInputService8TestCaseOperation5 == nil {
		opInputService8TestCaseOperation5 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService8TestShapeInputShape{}
	}

	req = c.newRequest(opInputService8TestCaseOperation5, input, output)
	output = &InputService8TestShapeInputService8TestCaseOperation5Output{}
	req.Data = output
	return
}

func (c *InputService8ProtocolTest) InputService8TestCaseOperation5(input *InputService8TestShapeInputShape) (*InputService8TestShapeInputService8TestCaseOperation5Output, error) {
	req, out := c.InputService8TestCaseOperation5Request(input)
	err := req.Send()
	return out, err
}

var opInputService8TestCaseOperation5 *aws.Operation

// InputService8TestCaseOperation6Request generates a request for the InputService8TestCaseOperation6 operation.
func (c *InputService8ProtocolTest) InputService8TestCaseOperation6Request(input *InputService8TestShapeInputShape) (req *aws.Request, output *InputService8TestShapeInputService8TestCaseOperation6Output) {

	if opInputService8TestCaseOperation6 == nil {
		opInputService8TestCaseOperation6 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService8TestShapeInputShape{}
	}

	req = c.newRequest(opInputService8TestCaseOperation6, input, output)
	output = &InputService8TestShapeInputService8TestCaseOperation6Output{}
	req.Data = output
	return
}

func (c *InputService8ProtocolTest) InputService8TestCaseOperation6(input *InputService8TestShapeInputShape) (*InputService8TestShapeInputService8TestCaseOperation6Output, error) {
	req, out := c.InputService8TestCaseOperation6Request(input)
	err := req.Send()
	return out, err
}

var opInputService8TestCaseOperation6 *aws.Operation

type InputService8TestShapeInputService8TestCaseOperation1Output struct {
	metadataInputService8TestShapeInputService8TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputService8TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeInputService8TestCaseOperation2Output struct {
	metadataInputService8TestShapeInputService8TestCaseOperation2Output `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputService8TestCaseOperation2Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeInputService8TestCaseOperation3Output struct {
	metadataInputService8TestShapeInputService8TestCaseOperation3Output `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputService8TestCaseOperation3Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeInputService8TestCaseOperation4Output struct {
	metadataInputService8TestShapeInputService8TestCaseOperation4Output `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputService8TestCaseOperation4Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeInputService8TestCaseOperation5Output struct {
	metadataInputService8TestShapeInputService8TestCaseOperation5Output `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputService8TestCaseOperation5Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeInputService8TestCaseOperation6Output struct {
	metadataInputService8TestShapeInputService8TestCaseOperation6Output `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputService8TestCaseOperation6Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeInputShape struct {
	RecursiveStruct *InputService8TestShapeRecursiveStructType `type:"structure"`

	metadataInputService8TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService8TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService8TestShapeRecursiveStructType struct {
	NoRecurse *string `type:"string"`

	RecursiveList []*InputService8TestShapeRecursiveStructType `type:"list"`

	RecursiveMap map[string]*InputService8TestShapeRecursiveStructType `type:"map"`

	RecursiveStruct *InputService8TestShapeRecursiveStructType `type:"structure"`

	metadataInputService8TestShapeRecursiveStructType `json:"-" xml:"-"`
}

type metadataInputService8TestShapeRecursiveStructType struct {
	SDKShapeTraits bool `type:"structure"`
}

// InputService9ProtocolTest is a client for InputService9ProtocolTest.
type InputService9ProtocolTest struct {
	*aws.Service
}

// New returns a new InputService9ProtocolTest client.
func NewInputService9ProtocolTest(config *aws.Config) *InputService9ProtocolTest {
	service := &aws.Service{
		Config:      aws.DefaultConfig.Merge(config),
		ServiceName: "inputservice9protocoltest",
		APIVersion:  "2014-01-01",
	}
	service.Initialize()

	// Handlers
	service.Handlers.Sign.PushBack(v4.Sign)
	service.Handlers.Build.PushBack(restjson.Build)
	service.Handlers.Unmarshal.PushBack(restjson.Unmarshal)
	service.Handlers.UnmarshalMeta.PushBack(restjson.UnmarshalMeta)
	service.Handlers.UnmarshalError.PushBack(restjson.UnmarshalError)

	return &InputService9ProtocolTest{service}
}

// newRequest creates a new request for a InputService9ProtocolTest operation and runs any
// custom request initialization.
func (c *InputService9ProtocolTest) newRequest(op *aws.Operation, params, data interface{}) *aws.Request {
	req := aws.NewRequest(c.Service, op, params, data)

	return req
}

// InputService9TestCaseOperation1Request generates a request for the InputService9TestCaseOperation1 operation.
func (c *InputService9ProtocolTest) InputService9TestCaseOperation1Request(input *InputService9TestShapeInputShape) (req *aws.Request, output *InputService9TestShapeInputService9TestCaseOperation1Output) {

	if opInputService9TestCaseOperation1 == nil {
		opInputService9TestCaseOperation1 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService9TestShapeInputShape{}
	}

	req = c.newRequest(opInputService9TestCaseOperation1, input, output)
	output = &InputService9TestShapeInputService9TestCaseOperation1Output{}
	req.Data = output
	return
}

func (c *InputService9ProtocolTest) InputService9TestCaseOperation1(input *InputService9TestShapeInputShape) (*InputService9TestShapeInputService9TestCaseOperation1Output, error) {
	req, out := c.InputService9TestCaseOperation1Request(input)
	err := req.Send()
	return out, err
}

var opInputService9TestCaseOperation1 *aws.Operation

// InputService9TestCaseOperation2Request generates a request for the InputService9TestCaseOperation2 operation.
func (c *InputService9ProtocolTest) InputService9TestCaseOperation2Request(input *InputService9TestShapeInputShape) (req *aws.Request, output *InputService9TestShapeInputService9TestCaseOperation2Output) {

	if opInputService9TestCaseOperation2 == nil {
		opInputService9TestCaseOperation2 = &aws.Operation{
			Name:       "OperationName",
			HTTPMethod: "POST",
			HTTPPath:   "/path",
		}
	}

	if input == nil {
		input = &InputService9TestShapeInputShape{}
	}

	req = c.newRequest(opInputService9TestCaseOperation2, input, output)
	output = &InputService9TestShapeInputService9TestCaseOperation2Output{}
	req.Data = output
	return
}

func (c *InputService9ProtocolTest) InputService9TestCaseOperation2(input *InputService9TestShapeInputShape) (*InputService9TestShapeInputService9TestCaseOperation2Output, error) {
	req, out := c.InputService9TestCaseOperation2Request(input)
	err := req.Send()
	return out, err
}

var opInputService9TestCaseOperation2 *aws.Operation

type InputService9TestShapeInputService9TestCaseOperation1Output struct {
	metadataInputService9TestShapeInputService9TestCaseOperation1Output `json:"-" xml:"-"`
}

type metadataInputService9TestShapeInputService9TestCaseOperation1Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService9TestShapeInputService9TestCaseOperation2Output struct {
	metadataInputService9TestShapeInputService9TestCaseOperation2Output `json:"-" xml:"-"`
}

type metadataInputService9TestShapeInputService9TestCaseOperation2Output struct {
	SDKShapeTraits bool `type:"structure"`
}

type InputService9TestShapeInputShape struct {
	TimeArg *time.Time `type:"timestamp" timestampFormat:"unix"`

	TimeArgInHeader *time.Time `location:"header" locationName:"x-amz-timearg" type:"timestamp" timestampFormat:"rfc822"`

	metadataInputService9TestShapeInputShape `json:"-" xml:"-"`
}

type metadataInputService9TestShapeInputShape struct {
	SDKShapeTraits bool `type:"structure"`
}

//
// Tests begin here
//

func TestInputService1ProtocolTestURIParameterOnlyWithNoLocationNameCase1(t *testing.T) {
	svc := NewInputService1ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService1TestShapeInputShape{
		PipelineId: aws.String("foo"),
	}
	req, _ := svc.InputService1TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert URL
	assert.Equal(t, "https://test/2014-01-01/jobsByPipeline/foo", r.URL.String())

	// assert headers

}

func TestInputService2ProtocolTestURIParameterOnlyWithLocationNameCase1(t *testing.T) {
	svc := NewInputService2ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService2TestShapeInputShape{
		Foo: aws.String("bar"),
	}
	req, _ := svc.InputService2TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert URL
	assert.Equal(t, "https://test/2014-01-01/jobsByPipeline/bar", r.URL.String())

	// assert headers

}

func TestInputService3ProtocolTestURIParameterAndQuerystringParamsCase1(t *testing.T) {
	svc := NewInputService3ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService3TestShapeInputShape{
		Ascending:  aws.String("true"),
		PageToken:  aws.String("bar"),
		PipelineId: aws.String("foo"),
	}
	req, _ := svc.InputService3TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert URL
	assert.Equal(t, "https://test/2014-01-01/jobsByPipeline/foo?Ascending=true&PageToken=bar", r.URL.String())

	// assert headers

}

func TestInputService4ProtocolTestURIParameterQuerystringParamsAndJSONBodyCase1(t *testing.T) {
	svc := NewInputService4ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService4TestShapeInputShape{
		Ascending: aws.String("true"),
		Config: &InputService4TestShapeStructType{
			A: aws.String("one"),
			B: aws.String("two"),
		},
		PageToken:  aws.String("bar"),
		PipelineId: aws.String("foo"),
	}
	req, _ := svc.InputService4TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"Config":{"A":"one","B":"two"}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/2014-01-01/jobsByPipeline/foo?Ascending=true&PageToken=bar", r.URL.String())

	// assert headers

}

func TestInputService5ProtocolTestURIParameterQuerystringParamsHeadersAndJSONBodyCase1(t *testing.T) {
	svc := NewInputService5ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService5TestShapeInputShape{
		Ascending: aws.String("true"),
		Checksum:  aws.String("12345"),
		Config: &InputService5TestShapeStructType{
			A: aws.String("one"),
			B: aws.String("two"),
		},
		PageToken:  aws.String("bar"),
		PipelineId: aws.String("foo"),
	}
	req, _ := svc.InputService5TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"Config":{"A":"one","B":"two"}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/2014-01-01/jobsByPipeline/foo?Ascending=true&PageToken=bar", r.URL.String())

	// assert headers
	assert.Equal(t, "12345", r.Header.Get("x-amz-checksum"))

}

func TestInputService6ProtocolTestStreamingPayloadCase1(t *testing.T) {
	svc := NewInputService6ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService6TestShapeInputShape{
		Body:      aws.ReadSeekCloser(bytes.NewBufferString("contents")),
		Checksum:  aws.String("foo"),
		VaultName: aws.String("name"),
	}
	req, _ := svc.InputService6TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`contents`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/2014-01-01/vaults/name/archives", r.URL.String())

	// assert headers
	assert.Equal(t, "foo", r.Header.Get("x-amz-sha256-tree-hash"))

}

func TestInputService7ProtocolTestOmitsNullQueryParamsButSerializesEmptyStringsCase1(t *testing.T) {
	svc := NewInputService7ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService7TestShapeInputShape{}
	req, _ := svc.InputService7TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService7ProtocolTestOmitsNullQueryParamsButSerializesEmptyStringsCase2(t *testing.T) {
	svc := NewInputService7ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService7TestShapeInputShape{
		Foo: aws.String(""),
	}
	req, _ := svc.InputService7TestCaseOperation2Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert URL
	assert.Equal(t, "https://test/path?abc=mno&param-name=", r.URL.String())

	// assert headers

}

func TestInputService8ProtocolTestRecursiveShapesCase1(t *testing.T) {
	svc := NewInputService8ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService8TestShapeInputShape{
		RecursiveStruct: &InputService8TestShapeRecursiveStructType{
			NoRecurse: aws.String("foo"),
		},
	}
	req, _ := svc.InputService8TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"NoRecurse":"foo"}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService8ProtocolTestRecursiveShapesCase2(t *testing.T) {
	svc := NewInputService8ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService8TestShapeInputShape{
		RecursiveStruct: &InputService8TestShapeRecursiveStructType{
			RecursiveStruct: &InputService8TestShapeRecursiveStructType{
				NoRecurse: aws.String("foo"),
			},
		},
	}
	req, _ := svc.InputService8TestCaseOperation2Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveStruct":{"NoRecurse":"foo"}}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService8ProtocolTestRecursiveShapesCase3(t *testing.T) {
	svc := NewInputService8ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService8TestShapeInputShape{
		RecursiveStruct: &InputService8TestShapeRecursiveStructType{
			RecursiveStruct: &InputService8TestShapeRecursiveStructType{
				RecursiveStruct: &InputService8TestShapeRecursiveStructType{
					RecursiveStruct: &InputService8TestShapeRecursiveStructType{
						NoRecurse: aws.String("foo"),
					},
				},
			},
		},
	}
	req, _ := svc.InputService8TestCaseOperation3Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveStruct":{"RecursiveStruct":{"RecursiveStruct":{"NoRecurse":"foo"}}}}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService8ProtocolTestRecursiveShapesCase4(t *testing.T) {
	svc := NewInputService8ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService8TestShapeInputShape{
		RecursiveStruct: &InputService8TestShapeRecursiveStructType{
			RecursiveList: []*InputService8TestShapeRecursiveStructType{
				{
					NoRecurse: aws.String("foo"),
				},
				{
					NoRecurse: aws.String("bar"),
				},
			},
		},
	}
	req, _ := svc.InputService8TestCaseOperation4Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveList":[{"NoRecurse":"foo"},{"NoRecurse":"bar"}]}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService8ProtocolTestRecursiveShapesCase5(t *testing.T) {
	svc := NewInputService8ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService8TestShapeInputShape{
		RecursiveStruct: &InputService8TestShapeRecursiveStructType{
			RecursiveList: []*InputService8TestShapeRecursiveStructType{
				{
					NoRecurse: aws.String("foo"),
				},
				{
					RecursiveStruct: &InputService8TestShapeRecursiveStructType{
						NoRecurse: aws.String("bar"),
					},
				},
			},
		},
	}
	req, _ := svc.InputService8TestCaseOperation5Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveList":[{"NoRecurse":"foo"},{"RecursiveStruct":{"NoRecurse":"bar"}}]}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService8ProtocolTestRecursiveShapesCase6(t *testing.T) {
	svc := NewInputService8ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService8TestShapeInputShape{
		RecursiveStruct: &InputService8TestShapeRecursiveStructType{
			RecursiveMap: map[string]*InputService8TestShapeRecursiveStructType{
				"bar": {
					NoRecurse: aws.String("bar"),
				},
				"foo": {
					NoRecurse: aws.String("foo"),
				},
			},
		},
	}
	req, _ := svc.InputService8TestCaseOperation6Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"RecursiveStruct":{"RecursiveMap":{"bar":{"NoRecurse":"bar"},"foo":{"NoRecurse":"foo"}}}}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService9ProtocolTestTimestampValuesCase1(t *testing.T) {
	svc := NewInputService9ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService9TestShapeInputShape{
		TimeArg: aws.Time(time.Unix(1422172800, 0)),
	}
	req, _ := svc.InputService9TestCaseOperation1Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert body
	assert.NotNil(t, r.Body)
	body, _ := ioutil.ReadAll(r.Body)
	assert.Equal(t, util.Trim(`{"TimeArg":1422172800}`), util.Trim(string(body)))

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers

}

func TestInputService9ProtocolTestTimestampValuesCase2(t *testing.T) {
	svc := NewInputService9ProtocolTest(nil)
	svc.Endpoint = "https://test"

	input := &InputService9TestShapeInputShape{
		TimeArgInHeader: aws.Time(time.Unix(1422172800, 0)),
	}
	req, _ := svc.InputService9TestCaseOperation2Request(input)
	r := req.HTTPRequest

	// build request
	restjson.Build(req)
	assert.NoError(t, req.Error)

	// assert URL
	assert.Equal(t, "https://test/path", r.URL.String())

	// assert headers
	assert.Equal(t, "Sun, 25 Jan 2015 08:00:00 GMT", r.Header.Get("x-amz-timearg"))

}
