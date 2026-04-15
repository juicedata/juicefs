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
	"sync"
)

const (
	plainChunkSize  = 1 << 20 // 1 MiB plaintext per chunk
	chunkHeaderSize = 4       // bytes used to store ct_len per chunk
)

// chunkedEncrypted is an ObjectStorage wrapper that encrypts data in fixed-size chunks.
//
//	[chunkHeaderSize bytes: ct_len][ciphertext][zeros] <- each chunk is padded to len(plain)+overhead bytes
type chunkedEncrypted struct {
	ObjectStorage
	enc *dataEncryptor

	overhead     int
	encChunkSize int64

	plainPool, encChunkPool sync.Pool // plainChunkSize/encChunkSize buffers
}

func NewChunkedEncrypted(o ObjectStorage, enc *dataEncryptor) ObjectStorage {
	overhead := enc.MaxOverhead()
	encChunkSize := plainChunkSize + chunkHeaderSize + int64(overhead)
	ce := &chunkedEncrypted{
		ObjectStorage: o,
		enc:           enc,
		overhead:      overhead,
		encChunkSize:  encChunkSize,
	}
	ce.plainPool = sync.Pool{New: func() any { buf := make([]byte, plainChunkSize); return &buf }}
	ce.encChunkPool = sync.Pool{New: func() any { buf := make([]byte, encChunkSize); return &buf }}
	if fs, ok := o.(FileSystem); ok {
		cefs := &chunkedEncryptedFS{chunkedEncrypted: ce, FileSystem: fs}
		if symlink, ok := o.(SupportSymlink); ok {
			return &chunkedEncryptedFSSymlink{chunkedEncryptedFS: cefs, SupportSymlink: symlink}
		}
		return cefs
	}
	return ce
}

func (e *chunkedEncrypted) String() string {
	return fmt.Sprintf("%s(encrypted-chunked)", e.ObjectStorage)
}

// calcPlainSize computes the exact plaintext size from the total encrypted file size.
func (e *chunkedEncrypted) calcPlainSize(encSize int64) int64 {
	if encSize <= 0 {
		return 0
	}
	fullChunks := encSize / e.encChunkSize
	remainder := encSize % e.encChunkSize
	plainSize := fullChunks * plainChunkSize
	if remainder > chunkHeaderSize+int64(e.overhead) {
		plainSize += remainder - (chunkHeaderSize + int64(e.overhead))
	}
	return plainSize
}

func (e *chunkedEncrypted) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	startChunk := off / plainChunkSize
	encOff := startChunk * e.encChunkSize

	var encLimit int64 = -1
	if limit > 0 {
		endChunk := (off + limit - 1) / plainChunkSize
		encLimit = (endChunk - startChunk + 1) * e.encChunkSize
	}

	r, err := e.ObjectStorage.Get(ctx, key, encOff, encLimit, getters...)
	if err != nil {
		return nil, err
	}

	dr := &chunkDecryptReader{
		r:    r,
		enc:  e.enc,
		pool: &e.encChunkPool,
		skip: off - startChunk*plainChunkSize,
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
	r    io.ReadCloser
	enc  *dataEncryptor
	buf  []byte
	pool *sync.Pool
	skip int64
}

func (r *chunkDecryptReader) Read(p []byte) (int, error) {
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}

	chunkBuf := r.pool.Get().(*[]byte)
	defer r.pool.Put(chunkBuf)

	n, err := io.ReadFull(r.r, *chunkBuf)
	chunk := (*chunkBuf)[:n]
	if err != io.ErrUnexpectedEOF && err != nil {
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
		skip := r.skip
		r.skip = 0
		if skip >= int64(len(plain)) {
			return 0, io.EOF
		}
		plain = plain[skip:]
	}

	n = copy(p, plain)
	if n < len(plain) {
		r.buf = plain[n:]
	}
	return n, nil
}

func (r *chunkDecryptReader) Close() error { return r.r.Close() }

type chunkEncryptReader struct {
	r        io.Reader
	enc      *dataEncryptor
	overhead int
	pool     *sync.Pool
	buf      []byte
	done     bool
}

func (e *chunkedEncrypted) newChunkEncryptReader(r io.Reader) *chunkEncryptReader {
	return &chunkEncryptReader{
		r:        r,
		enc:      e.enc,
		overhead: e.overhead,
		pool:     &e.plainPool,
	}
}

func (cr *chunkEncryptReader) Read(p []byte) (int, error) {
	if len(cr.buf) > 0 {
		n := copy(p, cr.buf)
		cr.buf = cr.buf[n:]
		return n, nil
	}
	if cr.done {
		return 0, io.EOF
	}

	plain := cr.pool.Get().(*[]byte)
	defer cr.pool.Put(plain)

	n, readErr := io.ReadFull(cr.r, *plain)
	if n == 0 {
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			cr.done = true
			return 0, io.EOF
		}
		return 0, readErr
	}

	ct, err := cr.enc.Encrypt((*plain)[:n])
	if err != nil {
		return 0, err
	}
	fixedCtLen := n + cr.overhead
	if len(ct) > fixedCtLen {
		return 0, fmt.Errorf("encrypt_chunked: ciphertext %d exceeds capacity %d", len(ct), fixedCtLen)
	}

	chunk := make([]byte, chunkHeaderSize+fixedCtLen)
	binary.BigEndian.PutUint32(chunk[:chunkHeaderSize], uint32(len(ct)))
	copy(chunk[chunkHeaderSize:], ct)

	copied := copy(p, chunk)
	if copied < len(chunk) {
		cr.buf = chunk[copied:]
	}

	if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
		cr.done = true
	} else if readErr != nil {
		return copied, readErr
	}
	return copied, nil
}

func (e *chunkedEncrypted) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	return e.ObjectStorage.Put(ctx, key, e.newChunkEncryptReader(in), getters...)
}

type sizedObj struct {
	Object
	size int64
}

func (o *sizedObj) Size() int64 { return o.size }

type sizedFile struct {
	File
	size int64
}

func (o *sizedFile) Size() int64 { return o.size }

func withSize(o Object, size int64) Object {
	if f, ok := o.(File); ok {
		return &sizedFile{File: f, size: size}
	}
	return &sizedObj{Object: o, size: size}
}

func (e *chunkedEncrypted) Head(ctx context.Context, key string) (Object, error) {
	o, err := e.ObjectStorage.Head(ctx, key)
	if err != nil {
		return nil, err
	}
	return withSize(o, e.calcPlainSize(o.Size())), nil
}

func (e *chunkedEncrypted) List(ctx context.Context, prefix, startAfter, token, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	objs, hasMore, nextToken, err := e.ObjectStorage.List(ctx, prefix, startAfter, token, delimiter, limit, followLink)
	if err != nil {
		return nil, hasMore, nextToken, err
	}
	for i, o := range objs {
		if !o.IsDir() {
			objs[i] = withSize(o, e.calcPlainSize(o.Size()))
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
				o = withSize(o, e.calcPlainSize(o.Size()))
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

func (e *chunkedEncrypted) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	data, err := io.ReadAll(e.newChunkEncryptReader(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}
	return e.ObjectStorage.UploadPart(ctx, key, uploadID, num, data)
}

func (e *chunkedEncrypted) UploadPartCopy(ctx context.Context, key string, uploadID string, num int, srcKey string, off, size int64) (*Part, error) {
	return nil, notSupported
}

func (e *chunkedEncrypted) SetTier(init Tiers) {
	if o, ok := e.ObjectStorage.(SupportTier); ok {
		o.SetTier(init)
	}
}

func (e *chunkedEncrypted) GetStorageClass(ctx context.Context) string {
	if o, ok := e.ObjectStorage.(SupportTier); ok {
		return o.GetStorageClass(ctx)
	}
	return ""
}

var _ ObjectStorage = (*chunkedEncrypted)(nil)

type chunkedEncryptedFS struct {
	*chunkedEncrypted
	FileSystem
}

var _ FileSystem = (*chunkedEncryptedFS)(nil)

type chunkedEncryptedFSSymlink struct {
	*chunkedEncryptedFS
	SupportSymlink
}

var _ SupportSymlink = (*chunkedEncryptedFSSymlink)(nil)
