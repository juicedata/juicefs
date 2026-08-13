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
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

type countingEncryptor struct {
	Encryptor
	calls atomic.Int32
}

func (e *countingEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	e.calls.Add(1)
	return e.Encryptor.Encrypt(plaintext)
}

type failOnceEncryptor struct {
	Encryptor
	calls atomic.Int32
}

func (e *failOnceEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	if e.calls.Add(1) == 1 {
		return nil, errors.New("temporary encrypt failure")
	}
	return e.Encryptor.Encrypt(plaintext)
}

type retryPutStorage struct {
	ObjectStorage
	failures int
	puts     atomic.Int32

	mu      sync.Mutex
	bodies  [][]byte
	started chan struct{}
	release chan struct{}
}

func (s *retryPutStorage) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	body, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	put := s.puts.Add(1)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()
	if put == 1 && s.started != nil {
		close(s.started)
		<-s.release
	}
	if int(put) <= s.failures {
		return errors.New("temporary put failure")
	}
	return s.ObjectStorage.Put(ctx, key, bytes.NewReader(body), getters...)
}

func TestEncryptedPutRetry(t *testing.T) {
	ctx := WithPutRequest(context.Background())
	base, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	storage := &retryPutStorage{ObjectStorage: base, failures: 2}
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	enc := &countingEncryptor{Encryptor: dc}
	store := WithPrefix(NewEncrypted(storage, enc), "prefix/")
	want := []byte("same plaintext for every attempt")

	for i := 0; i < 3; i++ {
		err = store.Put(ctx, "key", bytes.NewReader(want))
		if i < 2 {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}

	require.Equal(t, int32(1), enc.calls.Load())
	storage.mu.Lock()
	require.Len(t, storage.bodies, 3)
	require.Equal(t, storage.bodies[0], storage.bodies[1])
	require.Equal(t, storage.bodies[0], storage.bodies[2])
	ciphertext := storage.bodies[2]
	storage.mu.Unlock()
	plain, err := dc.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, want, plain)
}

func TestPutRequestContext(t *testing.T) {
	type contextKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "value"))
	ctx := WithPutRequest(parent)

	require.Equal(t, "value", ctx.Value(contextKey{}))
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Panics(t, func() { WithPutRequest(nil) })
}

func TestEncryptedPut(t *testing.T) {
	base, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	enc := &countingEncryptor{Encryptor: dc}
	store := NewEncrypted(base, enc)

	for i := 0; i < 2; i++ {
		require.NoError(t, store.Put(context.Background(), "key", bytes.NewReader([]byte("payload"))))
	}
	require.Equal(t, int32(2), enc.calls.Load())

	readErr := errors.New("read failure")
	require.ErrorIs(t, store.Put(context.Background(), "key", iotest.ErrReader(readErr)), readErr)
	require.Equal(t, int32(2), enc.calls.Load())
}

func TestEncryptedPutPrepareRetry(t *testing.T) {
	ctx := WithPutRequest(context.Background())
	base, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	enc := &failOnceEncryptor{Encryptor: dc}
	store := NewEncrypted(base, enc)

	require.Error(t, store.Put(ctx, "key", bytes.NewReader([]byte("payload"))))
	require.NoError(t, store.Put(ctx, "key", bytes.NewReader([]byte("payload"))))
	require.Equal(t, int32(2), enc.calls.Load())
}

func TestPutRequestKey(t *testing.T) {
	ctx := WithPutRequest(context.Background())
	base, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	store := NewEncrypted(base, dc)

	require.NoError(t, store.Put(ctx, "key", bytes.NewReader([]byte("payload"))))
	err = store.Put(ctx, "other", bytes.NewReader([]byte("payload")))
	require.ErrorContains(t, err, "put request key mismatch")
	_, err = base.Head(context.Background(), "other")
	require.Error(t, err)
}

func TestEncryptedPutConcurrent(t *testing.T) {
	ctx := WithPutRequest(context.Background())
	base, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	storage := &retryPutStorage{
		ObjectStorage: base,
		started:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	enc := &countingEncryptor{Encryptor: dc}
	store := NewEncrypted(storage, enc)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.Put(ctx, "key", bytes.NewReader([]byte("payload")))
	}()
	<-storage.started
	require.NoError(t, store.Put(ctx, "key", bytes.NewReader([]byte("payload"))))
	close(storage.release)
	require.NoError(t, <-firstDone)

	require.Equal(t, int32(1), enc.calls.Load())
	storage.mu.Lock()
	defer storage.mu.Unlock()
	require.Len(t, storage.bodies, 2)
	require.Equal(t, storage.bodies[0], storage.bodies[1])
}
