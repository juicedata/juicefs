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

package fs

import (
	"errors"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type reloadTestMeta struct {
	meta.Meta
	reload func(*meta.Format) error
}

func (m *reloadTestMeta) OnReload(fn func(*meta.Format) error) {
	m.reload = fn
}

func TestReloadableStorage(t *testing.T) {
	const storage = "reload-test"
	var created int
	var createErr error
	object.Register(storage, func(bucket, accessKey, secretKey, token string) (object.ObjectStorage, error) {
		if secretKey != "test-secret" {
			t.Errorf("storage constructor did not receive the decrypted secret key")
		}
		created++
		if createErr != nil {
			return nil, createErr
		}
		return object.CreateStorage("mem", bucket, accessKey, secretKey, token)
	})
	plain := meta.Format{Name: "vol", Storage: storage, Bucket: "original", SecretKey: "test-secret"}
	format := plain
	if err := format.Encrypt(); err != nil {
		t.Fatal(err)
	}
	bucket := "first"
	m := &reloadTestMeta{}
	blob, err := NewReloadableStorage(&format, m, func(f *meta.Format) {
		f.Bucket = bucket
		f.UploadLimit = 42
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { object.Shutdown(blob) })
	if blob.String() != "mem://first/vol/" || format.Bucket != "original" {
		t.Fatalf("initial storage %s, format bucket %q", blob, format.Bucket)
	}

	updated := plain
	if err := updated.Encrypt(); err != nil {
		t.Fatal(err)
	}
	if err := m.reload(updated.Clone()); err != nil || created != 1 {
		t.Fatalf("reloading the same credentials: %v, %d create calls", err, created)
	}

	bucket = "second"
	createErr = errors.New("temporary storage error")
	if err := m.reload(updated.Clone()); !errors.Is(err, createErr) || blob.String() != "mem://first/vol/" {
		t.Fatalf("failed reload: %v, storage %s", err, blob)
	}
	createErr = nil
	if err := m.reload(&updated); err != nil || blob.String() != "mem://second/vol/" {
		t.Fatalf("retried reload: %v, storage %s", err, blob)
	}
	if updated.UploadLimit != 42 || !updated.KeyEncrypted {
		t.Fatalf("reload callback did not preserve patched limits and encrypted credentials")
	}
}
