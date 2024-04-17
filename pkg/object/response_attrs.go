/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package object

const DefaultStorageClass = "STANDARD"

type SupportStorageClass interface {
	SetStorageClass(sc string) error
}

// A generic way to get attributes from different object storage clients
type ResponseAttrs struct {
	storageClass *string
	requestID    *string
	// other interested attrs can be added here
}

func (r *ResponseAttrs) SetRequestID(id string) *ResponseAttrs {
	if r.requestID != nil { // Will be nil if caller is not interested in this attribute
		*r.requestID = id
	}
	return r
}

func (r *ResponseAttrs) SetStorageClass(sc string) *ResponseAttrs {
	if r.storageClass != nil && sc != "" { // Don't overwrite default storage class
		*r.storageClass = sc
	}
	return r
}

type AttrGetter func(attrs *ResponseAttrs)

func WithRequestID(id *string) AttrGetter {
	return func(attrs *ResponseAttrs) {
		attrs.requestID = id
	}
}

func WithStorageClass(sc *string) AttrGetter {
	return func(attrs *ResponseAttrs) {
		attrs.storageClass = sc
	}
}

func applyGetters(getters ...AttrGetter) ResponseAttrs {
	var attrs ResponseAttrs
	for _, getter := range getters {
		getter(&attrs)
	}
	return attrs
}
