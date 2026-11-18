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
	"testing"

	jfscred "github.com/juicedata/juicefs/pkg/object/credentials"
)

func TestCreateStorageWithProvider(t *testing.T) {
	const name = "provider-test"
	RegisterProvider(name, func(bucket string, p jfscred.Provider) (ObjectStorage, error) {
		if bucket != "bucket" {
			t.Fatalf("unexpected bucket: %s", bucket)
		}
		cred, err := p.Get(context.Background())
		if err != nil {
			return nil, err
		}
		if cred.AccessKeyID != "ak" {
			t.Fatalf("unexpected credentials: %+v", cred)
		}
		return newMem(bucket, "", "", "")
	})

	p := jfscred.Static{Credentials: jfscred.Credentials{AccessKeyID: "ak"}}
	s, err := CreateStorageWithProvider(name, "bucket", p)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("storage was not created")
	}
}
