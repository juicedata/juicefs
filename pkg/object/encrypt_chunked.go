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
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	plainChunkSize    = 1 << 20                                              // 1 MiB plaintext per chunk
	chunkSafetyMargin = 300                                                  // reserved headroom for ciphertext expansion
	chunkHeaderSize   = 4                                                    // bytes used to store ct_len per chunk
	encChunkSize      = plainChunkSize + chunkHeaderSize + chunkSafetyMargin // on-disk bytes per chunk
)

// chunkedEncrypted is an ObjectStorage wrapper that encrypts data in fixed-size chunks.
//
//	[chunkHeaderSize bytes: ct_len][ciphertext][zeros] <- each chunk is padded to len(plain)+chunkSafetyMargin bytes
type chunkedEncrypted struct {
	ObjectStorage
	enc Encryptor
}

func NewChunkedEncrypted(o ObjectStorage, enc Encryptor) ObjectStorage {
	return &chunkedEncrypted{ObjectStorage: o, enc: enc}
}

func (e *chunkedEncrypted) String() string {
	return fmt.Sprintf("%s(encrypted-chunked)", e.ObjectStorage)
}

// calcPlainSize computes the exact plaintext size from the total encrypted file size.
func (e *chunkedEncrypted) calcPlainSize(encSize int64) int64 {
	if encSize <= 0 {
		return 0
	}
	fullChunks := encSize / encChunkSize
	remainder := encSize % encChunkSize
	plainSize := fullChunks * plainChunkSize
	if remainder > chunkHeaderSize+chunkSafetyMargin {
		plainSize += remainder - (chunkHeaderSize + chunkSafetyMargin)
	}
	return plainSize
}

func (e *chunkedEncrypted) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	startChunk := off / plainChunkSize
	encOff := startChunk * encChunkSize

	var encLimit int64 = -1
	if limit > 0 {
		endChunk := (off + limit - 1) / plainChunkSize
		encLimit = (endChunk - startChunk + 1) * encChunkSize
	}

	r, err := e.ObjectStorage.Get(ctx, key, encOff, encLimit, getters...)
	if err != nil {
		return nil, err
	}

	dr := &chunkDecryptReader{
		r:        r,
		enc:      e.enc,
		chunkBuf: make([]byte, encChunkSize),
		skip:     off - startChunk*plainChunkSize,
	}
	if limit > 0 {
		return &limitedReadCloser{io.LimitReader(dr, limit), dr}, nil
	}
	return dr, nil
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

type chunkDecryptReader struct {
	r        io.ReadCloser
	enc      Encryptor
	buf      []byte
	chunkBuf []byte
	skip     int64
}

func (r *chunkDecryptReader) Read(p []byte) (int, error) {
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}

	n, err := io.ReadFull(r.r, r.chunkBuf)
	chunk := r.chunkBuf[:n]
	if err == io.ErrUnexpectedEOF {
		err = nil
	} else if err != nil {
		return 0, err
	}

	if len(chunk) < chunkHeaderSize {
		return 0, fmt.Errorf("Decrypt: truncated chunk header")
	}
	ctLen := int(binary.BigEndian.Uint32(chunk[:chunkHeaderSize]))
	if chunkHeaderSize+ctLen > len(chunk) {
		return 0, fmt.Errorf("Decrypt: chunk data truncated: need %d, have %d", chunkHeaderSize+ctLen, len(chunk))
	}

	plain, decErr := r.enc.Decrypt(chunk[chunkHeaderSize : chunkHeaderSize+ctLen])
	if decErr != nil {
		return 0, fmt.Errorf("Decrypt: %s", decErr)
	}

	if r.skip > 0 {
		plain = plain[r.skip:]
		r.skip = 0
	}

	n = copy(p, plain)
	if n < len(plain) {
		r.buf = plain[n:]
	}
	return n, nil
}

func (r *chunkDecryptReader) Close() error { return r.r.Close() }

// encryptAndWriteChunk encrypts plain and writes [ct_len][ct][zeros].
// The ciphertext section is always padded to len(plain)+chunkSafetyMargin
func (e *chunkedEncrypted) encryptAndWriteChunk(w io.Writer, plain []byte) error {
	ct, err := e.enc.Encrypt(plain)
	if err != nil {
		return err
	}
	fixedCtLen := len(plain) + chunkSafetyMargin
	if len(ct) > fixedCtLen {
		return fmt.Errorf("encrypt_chunked: ciphertext %d exceeds capacity %d", len(ct), fixedCtLen)
	}
	var header [chunkHeaderSize]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(ct)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if _, err := w.Write(ct); err != nil {
		return err
	}
	if pad := fixedCtLen - len(ct); pad > 0 {
		if _, err := w.Write(make([]byte, pad)); err != nil {
			return err
		}
	}
	return nil
}

func (e *chunkedEncrypted) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	f, err := os.CreateTemp("", "jenc")
	if err != nil {
		return fmt.Errorf("Encrypt: create temp file: %s", err)
	}
	_ = os.Remove(f.Name())
	defer f.Close()

	buf := make([]byte, plainChunkSize)
	for {
		n, readErr := io.ReadFull(in, buf)
		if n > 0 {
			if err := e.encryptAndWriteChunk(f, buf[:n]); err != nil {
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

type sizedObj struct {
	Object
	size int64
}

func (o *sizedObj) Size() int64 { return o.size }

func (e *chunkedEncrypted) Head(ctx context.Context, key string) (Object, error) {
	o, err := e.ObjectStorage.Head(ctx, key)
	if err != nil {
		return nil, err
	}
	return &sizedObj{o, e.calcPlainSize(o.Size())}, nil
}

func (e *chunkedEncrypted) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	objs, hasMore, nextToken, err := e.ObjectStorage.List(ctx, prefix, startAfter, token, delimiter, limit, followLink)
	if err != nil {
		return nil, hasMore, nextToken, err
	}
	for i, o := range objs {
		if !o.IsDir() {
			objs[i] = &sizedObj{o, e.calcPlainSize(o.Size())}
		}
	}
	return objs, hasMore, nextToken, nil
}

func (e *chunkedEncrypted) ListAll(ctx context.Context, prefix, marker string, followLink bool) (<-chan Object, error) {
	ch, err := e.ObjectStorage.ListAll(ctx, prefix, marker, followLink)
	if err != nil {
		return nil, err
	}
	out := make(chan Object, 1000)
	go func() {
		defer close(out)
		for o := range ch {
			if o != nil && !o.IsDir() {
				o = &sizedObj{o, e.calcPlainSize(o.Size())}
			}
			out <- o
		}
	}()
	return out, nil
}

func (e *chunkedEncrypted) Limits() Limits {
	l := e.ObjectStorage.Limits()
	l.IsSupportUploadPartCopy = false
	return l
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

func (e *chunkedEncrypted) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	var buf bytes.Buffer
	for len(body) > 0 {
		n := plainChunkSize
		if n > len(body) {
			n = len(body)
		}
		if err := e.encryptAndWriteChunk(&buf, body[:n]); err != nil {
			return nil, err
		}
		body = body[n:]
	}
	return e.ObjectStorage.UploadPart(ctx, key, uploadID, num, buf.Bytes())
}

func (e *chunkedEncrypted) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, notSupported
}

var _ ObjectStorage = (*chunkedEncrypted)(nil)
var _ FileSystem = (*chunkedEncrypted)(nil)
