/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type putRequestContextKey struct{}

var putRequestKey = &putRequestContextKey{}

type preparedCiphertext struct {
	key        string
	ciphertext []byte
}

type putRequestState struct {
	mu       sync.Mutex
	prepared map[*encrypted]preparedCiphertext
}

type putRequestContext struct {
	context.Context
	state putRequestState
}

func (c *putRequestContext) Value(key any) any {
	if requestKey, ok := key.(*putRequestContextKey); ok && requestKey == putRequestKey {
		return &c.state
	}
	return c.Context.Value(key)
}

// WithPutRequest marks repeated Put calls as attempts for the same object payload.
// The returned context must not be reused for another logical Put request.
func WithPutRequest(ctx context.Context) context.Context {
	if ctx == nil {
		panic("cannot create context from nil parent")
	}
	return &putRequestContext{Context: ctx}
}

func (s *putRequestState) ciphertextFor(e *encrypted, key string, in io.Reader) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prepared, ok := s.prepared[e]; ok {
		if key != prepared.key {
			return nil, fmt.Errorf("put request key mismatch: %q != %q", key, prepared.key)
		}
		return prepared.ciphertext, nil
	}
	ciphertext, err := e.encrypt(in)
	if err != nil {
		return nil, err
	}
	if s.prepared == nil {
		s.prepared = make(map[*encrypted]preparedCiphertext)
	}
	s.prepared[e] = preparedCiphertext{key: key, ciphertext: ciphertext}
	return ciphertext, nil
}
