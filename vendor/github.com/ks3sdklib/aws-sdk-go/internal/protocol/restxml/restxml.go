// Package restxml provides RESTful XML serialisation of AWS
// requests and responses.
package restxml

//go:generate go run ../../fixtures/protocol/generate.go ../../fixtures/protocol/input/rest-xml.json build_test.go
//go:generate go run ../../fixtures/protocol/generate.go ../../fixtures/protocol/output/rest-xml.json unmarshal_test.go

import (
	"bytes"
	"encoding/xml"
	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/internal/apierr"
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/query"
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/rest"
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/xml/xmlutil"
)

// Build builds a request payload for the REST XML protocol.
func Build(r *aws.Request) {
	rest.Build(r)

	if t := rest.PayloadType(r.Params); t == "structure" || t == "" {
		var buf bytes.Buffer
		err := xmlutil.BuildXML(r.Params, xml.NewEncoder(&buf))
		if err != nil {
			r.Error = apierr.New("Marshal", "failed to enode rest XML request", err)
			return
		}
		r.SetBufferBody(buf.Bytes())
	}
}

// Unmarshal unmarshals a payload response for the REST XML protocol.
func Unmarshal(r *aws.Request) {
	if t := rest.PayloadType(r.Data); t == "structure" || t == "" {
		defer r.HTTPResponse.Body.Close()
		decoder := xml.NewDecoder(r.HTTPResponse.Body)
		err := xmlutil.UnmarshalXML(r.Data, decoder, "")
		if err != nil {
			r.Error = apierr.New("Unmarshal", "failed to decode REST XML response", err)
			return
		}
	}
}

// UnmarshalMeta unmarshals response headers for the REST XML protocol.
func UnmarshalMeta(r *aws.Request) {
	rest.Unmarshal(r)
}

// UnmarshalError unmarshals a response error for the REST XML protocol.
func UnmarshalError(r *aws.Request) {
	query.UnmarshalError(r)
}
