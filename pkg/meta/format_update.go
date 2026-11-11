/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package meta

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/pkg/errors"
)

// ErrFormatChanged rejects an update based on an obsolete Load snapshot.
var ErrFormatChanged = errors.New("volume configuration changed since it was loaded; reload and retry")

type formatUpdate struct {
	previous *Format
	next     *Format
	data     []byte
}

func (f *Format) setRevision(data []byte) {
	f.revision = fmt.Sprintf("%x", sha256.Sum256(data))
}

// prepareUpdate must run inside the transaction that writes the setting.
func (f *Format) prepareUpdate(body []byte, force bool) (*formatUpdate, error) {
	if !force && f.revision != "" && f.revision != fmt.Sprintf("%x", sha256.Sum256(body)) {
		return nil, ErrFormatChanged
	}
	u := &formatUpdate{next: f.Clone()}
	if len(body) > 0 {
		u.previous = new(Format)
		if err := json.Unmarshal(body, u.previous); err != nil {
			return nil, fmt.Errorf("existing format is broken: %w", err)
		}
		if err := u.next.update(u.previous, force); err != nil {
			return nil, errors.Wrap(err, "update format")
		}
	}
	data, err := json.MarshalIndent(u.next, "", "")
	if err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if len(body) > 0 && !force {
		// Retain fields unknown to this client, but remove every spelling of
		// known fields so omitempty and case-insensitive JSON aliases still work.
		var stored, updated map[string]json.RawMessage
		if err = json.Unmarshal(body, &stored); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &updated); err != nil {
			return nil, err
		}
		typ := reflect.TypeOf(Format{})
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			for key := range stored {
				if strings.EqualFold(key, name) {
					delete(stored, key)
				}
			}
		}
		if len(stored) > 0 {
			for key, value := range updated {
				stored[key] = value
			}
			data, err = json.MarshalIndent(stored, "", "")
			if err != nil {
				return nil, err
			}
		}
	}
	u.data = data
	return u, nil
}

func (m *baseMeta) publishFormatUpdate(format *Format, u *formatUpdate) {
	u.next.setRevision(u.data)
	*format = *u.next
	// Neither subsequent caller edits nor a revision refresh mutate the cache.
	m.setFormat(u.next.Clone())
}
