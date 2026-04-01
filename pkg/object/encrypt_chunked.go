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
}

func NewChunkedEncrypted(o ObjectStorage, enc *dataEncryptor) ObjectStorage {
	overhead := enc.MaxOverhead()
	ce := &chunkedEncrypted{
		ObjectStorage: o,
		enc:           enc,
		overhead:      overhead,
		encChunkSize:  plainChunkSize + chunkHeaderSize + int64(overhead),
	}
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
		r:        r,
		enc:      e.enc,
		chunkBuf: make([]byte, e.encChunkSize),
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
	enc      *dataEncryptor
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

// encryptAndWriteChunk encrypts plain and writes [ct_len][ct][zeros].
// The ciphertext section is always padded to len(plain)+overhead.
func (e *chunkedEncrypted) encryptAndWriteChunk(w io.Writer, plain []byte) error {
	ct, err := e.enc.Encrypt(plain)
	if err != nil {
		return err
	}
	fixedCtLen := len(plain) + e.overhead
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
	pr, pw := io.Pipe()
	go func() {
		plain := make([]byte, plainChunkSize)
		for {
			n, readErr := io.ReadFull(in, plain)
			if n > 0 {
				if err := e.encryptAndWriteChunk(pw, plain[:n]); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				pw.Close()
				return
			} else if readErr != nil {
				pw.CloseWithError(readErr)
				return
			}
		}
	}()
	err := e.ObjectStorage.Put(ctx, key, pr, getters...)
	if err != nil {
		pr.CloseWithError(err)
	}
	return err
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
