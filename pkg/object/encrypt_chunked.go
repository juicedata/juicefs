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
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	defaultChunkSize = 8 << 20 // 8 MiB
)

// chunkedEncrypted is an ObjectStorage wrapper that encrypts data in streaming chunked.
// Chunked encryption format:
// [4 bytes: chunk0_len][chunk0_ciphertext]
// [4 bytes: chunk1_len][chunk1_ciphertext]
// ...
type chunkedEncrypted struct {
	ObjectStorage
	enc Encryptor
}

func NewChunkedEncrypted(o ObjectStorage, enc Encryptor) ObjectStorage {
	return &chunkedEncrypted{o, enc}
}

func (e *chunkedEncrypted) String() string {
	return fmt.Sprintf("%s(encrypted-chunked)", e.ObjectStorage)
}

func (e *chunkedEncrypted) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	r, err := e.ObjectStorage.Get(ctx, key, 0, -1, getters...)
	if err != nil {
		return nil, err
	}
	dr := &chunkDecryptReader{r: r, enc: e.enc}
	if off > 0 {
		return nil, notSupported
	}
	if limit >= 0 {
		return &limitedReadCloser{
			Reader: io.LimitReader(dr, limit),
			Closer: dr,
		}, nil
	}
	return dr, nil
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

type chunkDecryptReader struct {
	r         io.ReadCloser
	enc       Encryptor
	buf       []byte
	cipherBuf []byte
}

func (r *chunkDecryptReader) Read(p []byte) (int, error) {
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r.r, lenBuf[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return 0, fmt.Errorf("Decrypt: truncated chunk length")
		}
		return 0, err
	}
	chunkLen := int(binary.BigEndian.Uint32(lenBuf[:]))
	if cap(r.cipherBuf) < chunkLen {
		r.cipherBuf = make([]byte, chunkLen)
	}
	ciphertext := r.cipherBuf[:chunkLen]
	if _, err := io.ReadFull(r.r, ciphertext); err != nil {
		return 0, fmt.Errorf("Decrypt: read chunk: %s", err)
	}
	plain, err := r.enc.Decrypt(ciphertext)
	if err != nil {
		return 0, fmt.Errorf("Decrypt: %s", err)
	}
	n := copy(p, plain)
	if n < len(plain) {
		r.buf = plain[n:]
	}
	return n, nil
}

func (r *chunkDecryptReader) Close() error { return r.r.Close() }

func (e *chunkedEncrypted) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	f, err := os.CreateTemp("", "jenc")
	if err != nil {
		return fmt.Errorf("Encrypt: create temp file: %s", err)
	}
	_ = os.Remove(f.Name())
	defer f.Close()

	plain := make([]byte, defaultChunkSize)
	lenBuf := make([]byte, 4)
	for {
		n, readErr := io.ReadFull(in, plain)
		if n > 0 {
			ciphertext, encErr := e.enc.Encrypt(plain[:n])
			if encErr != nil {
				return encErr
			}
			binary.BigEndian.PutUint32(lenBuf, uint32(len(ciphertext)))
			if _, err := f.Write(lenBuf); err != nil {
				return err
			}
			if _, err := f.Write(ciphertext); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break
			}
			return readErr
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return e.ObjectStorage.Put(ctx, key, f, getters...)
}

func (e *chunkedEncrypted) Chmod(path string, mode os.FileMode) error {
	if fs, ok := e.ObjectStorage.(FileSystem); ok {
		return fs.Chmod(path, mode)
	}
	return notSupported
}

func (e *chunkedEncrypted) Chown(path, owner, group string) error {
	if fs, ok := e.ObjectStorage.(FileSystem); ok {
		return fs.Chown(path, owner, group)
	}
	return notSupported
}

func (e *chunkedEncrypted) Chtimes(path string, mtime time.Time) error {
	if fs, ok := e.ObjectStorage.(FileSystem); ok {
		return fs.Chtimes(path, mtime)
	}
	return notSupported
}

func encryptChunk(enc Encryptor, plaintext []byte) ([]byte, error) {
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 4+len(ciphertext))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(ciphertext)))
	copy(buf[4:], ciphertext)
	return buf, nil
}

func (e *chunkedEncrypted) Limits() Limits {
	l := e.ObjectStorage.Limits()
	l.IsSupportUploadPartCopy = false
	l.IsNotSupportRangeRead = true
	return l
}

func (e *chunkedEncrypted) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	return e.ObjectStorage.CreateMultipartUpload(ctx, key)
}

func (e *chunkedEncrypted) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	chunk, err := encryptChunk(e.enc, body)
	if err != nil {
		return nil, err
	}
	return e.ObjectStorage.UploadPart(ctx, key, uploadID, num, chunk)
}

func (e *chunkedEncrypted) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, notSupported
}

func (e *chunkedEncrypted) AbortUpload(ctx context.Context, key string, uploadID string) {
	e.ObjectStorage.AbortUpload(ctx, key, uploadID)
}

func (e *chunkedEncrypted) CompleteUpload(ctx context.Context, key string, uploadID string, parts []*Part) error {
	return e.ObjectStorage.CompleteUpload(ctx, key, uploadID, parts)
}

func (e *chunkedEncrypted) ListUploads(ctx context.Context, marker string) ([]*PendingPart, string, error) {
	return e.ObjectStorage.ListUploads(ctx, marker)
}

var _ ObjectStorage = (*chunkedEncrypted)(nil)
var _ FileSystem = (*chunkedEncrypted)(nil)
