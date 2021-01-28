// +-------------------------------------------------------------------------
// | Copyright (C) 2016 Yunify, Inc.
// +-------------------------------------------------------------------------
// | Licensed under the Apache License, Version 2.0 (the "License");
// | you may not use this work except in compliance with the License.
// | You may obtain a copy of the License in the LICENSE file, or at:
// |
// | http://www.apache.org/licenses/LICENSE-2.0
// |
// | Unless required by applicable law or agreed to in writing, software
// | distributed under the License is distributed on an "AS IS" BASIS,
// | WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// | See the License for the specific language governing permissions and
// | limitations under the License.
// +-------------------------------------------------------------------------

package unpacker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/yunify/qingstor-sdk-go/request/data"
	"github.com/yunify/qingstor-sdk-go/request/errors"
)

// QingStorUnpacker is the response unpacker for QingStor service.
type QingStorUnpacker struct {
	baseUnpacker *BaseUnpacker
}

// UnpackHTTPRequest unpack the http response with an operation, http response and an output.
func (qu *QingStorUnpacker) UnpackHTTPRequest(o *data.Operation, r *http.Response, x *reflect.Value) error {
	qu.baseUnpacker = &BaseUnpacker{}
	err := qu.baseUnpacker.UnpackHTTPRequest(o, r, x)
	if err != nil {
		return err
	}

	err = qu.parseError()
	if err != nil {
		return err
	}

	// Close body for every API except GetObject and ImageProcess.
	if o.APIName != "GET Object" && o.APIName != "Image Process" && r.Body != nil {
		err = r.Body.Close()
		if err != nil {
			return err
		}

		r.Body = nil
	}

	return nil
}

func (qu *QingStorUnpacker) parseError() error {
	if !qu.baseUnpacker.isResponseRight() {
		if qu.baseUnpacker.httpResponse.Header.Get("Content-Type") == "application/json" {
			buffer := &bytes.Buffer{}
			buffer.ReadFrom(qu.baseUnpacker.httpResponse.Body)
			qu.baseUnpacker.httpResponse.Body.Close()

			qsError := &errors.QingStorError{}
			if buffer.Len() > 0 {
				err := json.Unmarshal(buffer.Bytes(), qsError)
				if err != nil {
					return err
				}
			}
			qsError.StatusCode = qu.baseUnpacker.httpResponse.StatusCode
			if qsError.RequestID == "" {
				qsError.RequestID = qu.baseUnpacker.httpResponse.Header.Get(http.CanonicalHeaderKey("X-QS-Request-ID"))
			}

			return qsError
		}
	}

	return nil
}
