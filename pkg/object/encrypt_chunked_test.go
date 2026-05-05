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
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunkedEncryptedConcurrentGet(t *testing.T) {
	ctx := context.Background()
	s, err := CreateStorage("mem", "", "", "", "")
	require.NoError(t, err)
	dc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)
	store := NewChunkedEncrypted(s, dc)

	const dataSize = 1024
	want := make([]byte, dataSize)
	for i := range want {
		want[i] = byte(i % 251)
	}
	require.NoError(t, store.Put(ctx, "key", bytes.NewReader(want)))

	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.Get(ctx, "key", 0, -1)
			require.NoError(t, err)
			defer r.Close()

			// Read one byte at a time to maximise the r.buf aliasing window.
			buf := make([]byte, 1)
			var got []byte
			for {
				n, readErr := r.Read(buf)
				if n > 0 {
					got = append(got, buf[:n]...)
				}
				if readErr == io.EOF {
					break
				}
				require.NoError(t, readErr)
			}
			require.Equal(t, want, got)
		}()
	}
	wg.Wait()
}
