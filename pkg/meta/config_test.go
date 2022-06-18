/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"strings"
	"testing"
)

func TestRemoveSecret(t *testing.T) {
	format := Format{Name: "test", SecretKey: "testSecret", EncryptKey: "testEncrypt", SessionToken: "token"}
	if err := format.Encrypt(); err != nil {
		t.Fatal(err)
	}

	format.RemoveSecret()
	if format.SecretKey != "removed" || format.EncryptKey != "removed" || format.SessionToken != "removed" {
		t.Fatalf("invalid format: %+v", format)
	}

	if err := format.Decrypt(); err != nil && !strings.Contains(err.Error(), "secret was removed") {
		t.Fatal(err)
	}
}

func TestEncrypt(t *testing.T) {
	format := Format{Name: "test", SecretKey: "testSecret", SessionToken: "token", EncryptKey: "testEncrypt"}
	if err := format.Encrypt(); err != nil {
		t.Fatalf("Format encrypt: %s", err)
	}
	if format.SecretKey == "testSecret" || format.SessionToken == "token" || format.EncryptKey == "testEncrypt" {
		t.Fatalf("invalid format: %+v", format)
	}
	if err := format.Decrypt(); err != nil {
		t.Fatalf("Format decrypt: %s", err)
	}
	if format.SecretKey != "testSecret" || format.SessionToken != "token" || format.EncryptKey != "testEncrypt" {
		t.Fatalf("invalid format: %+v", format)
	}
}
