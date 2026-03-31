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
	defaultPlainChunkSize = 8 << 20 // 8 MiB plaintext per chunk
	fileHeaderSize        = 8       // 4B magic + 4B plainChunkSize
	fileMagic             = "JENC"
)

// chunkedEncrypted is an ObjectStorage wrapper that encrypts data in fixed-size
// aligned chunks, supporting range reads.
//
// File format:
//
//	[4B magic][4B plainChunkSize]  ← 8-byte file header
//	[4B ciphertext_len][ciphertext]  ← chunk 0
//	[4B ciphertext_len][ciphertext]  ← chunk 1
//	...
//
// Encrypted offset of plaintext chunk N = fileHeaderSize + N * encChunkSize.
// All full chunks have identical byte size: 4 + plainChunkSize + encOverhead.
type chunkedEncrypted struct {
	ObjectStorage
	enc            Encryptor
	encOverhead    int
	plainChunkSize int
	encChunkSize   int64 // = 4 + plainChunkSize + encOverhead
}

func NewChunkedEncrypted(o ObjectStorage, enc Encryptor) ObjectStorage {
	overhead := calcEncryptOverhead(enc)
	pcs := choosePlainChunkSize(o.Limits(), overhead)
	return &chunkedEncrypted{
		ObjectStorage:  o,
		enc:            enc,
		encOverhead:    overhead,
		plainChunkSize: pcs,
		encChunkSize:   int64(4 + pcs + overhead),
	}
}

func calcEncryptOverhead(enc Encryptor) int {
	ct, err := enc.Encrypt(make([]byte, 1024))
	if err != nil {
		panic(fmt.Sprintf("encrypt_chunked: trial encryption failed: %v", err))
	}
	return len(ct) - 1024
}

func choosePlainChunkSize(limits Limits, encOverhead int) int {
	pcs := defaultPlainChunkSize
	if limits.MaxPartSize > 0 {
		// Encrypted chunk = 4 + pcs + encOverhead must fit in one part.
		maxPlain := int(limits.MaxPartSize) - 4 - encOverhead
		if maxPlain < pcs {
			pcs = maxPlain
		}
	}
	if pcs < 1<<20 {
		pcs = 1 << 20 // 1 MiB minimum
	}
	return pcs
}

func (e *chunkedEncrypted) writeFileHeader(w io.Writer) error {
	var hdr [fileHeaderSize]byte
	copy(hdr[:4], fileMagic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(e.plainChunkSize))
	_, err := w.Write(hdr[:])
	return err
}

func readFileHeader(r io.Reader, encOverhead int) (pcs int, ecs int64, err error) {
	var hdr [fileHeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, fmt.Errorf("read encrypted file header: %w", err)
	}
	if string(hdr[:4]) != fileMagic {
		return 0, 0, fmt.Errorf("invalid encrypted file magic: %q", string(hdr[:4]))
	}
	pcs = int(binary.BigEndian.Uint32(hdr[4:8]))
	return pcs, int64(4 + pcs + encOverhead), nil
}

func (e *chunkedEncrypted) String() string {
	return fmt.Sprintf("%s(encrypted-chunked)", e.ObjectStorage)
}

// calcPlainSize computes the plaintext size from the total encrypted file size.
func (e *chunkedEncrypted) calcPlainSize(encSize int64) int64 {
	if encSize <= fileHeaderSize {
		return 0
	}
	dataSize := encSize - fileHeaderSize
	fullChunks := dataSize / e.encChunkSize
	remainder := dataSize % e.encChunkSize

	plainSize := fullChunks * int64(e.plainChunkSize)
	if remainder > 0 {
		if lastPlain := remainder - 4 - int64(e.encOverhead); lastPlain > 0 {
			plainSize += lastPlain
		}
	}
	return plainSize
}

// Range read mapping:
//   - Read the 8-byte file header first to get the plainChunkSize written at upload time.
//   - startChunk = off / pcs
//   - encOff     = fileHeaderSize + startChunk * encChunkSize
//   - encLimit   = (endChunk - startChunk + 1) * encChunkSize  (whole chunks, chunk-aligned)
//   - skips the leading (off % pcs) bytes
func (e *chunkedEncrypted) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	pcs := int64(e.plainChunkSize)
	ecs := e.encChunkSize

	if off > 0 || limit > 0 {
		hdrReader, err := e.ObjectStorage.Get(ctx, key, 0, int64(fileHeaderSize), getters...)
		if err != nil {
			return nil, err
		}
		var parsedPcs int
		parsedPcs, ecs, err = readFileHeader(hdrReader, e.encOverhead)
		hdrReader.Close()
		if err != nil {
			return nil, err
		}
		pcs = int64(parsedPcs)
	}

	startChunk := off / pcs
	encOff := int64(fileHeaderSize) + startChunk*ecs

	var encLimit int64 = -1
	if limit > 0 {
		endChunk := (off + limit - 1) / pcs
		encLimit = (endChunk - startChunk + 1) * ecs
	}

	r, err := e.ObjectStorage.Get(ctx, key, encOff, encLimit, getters...)
	if err != nil {
		return nil, err
	}

	dr := &chunkDecryptReader{
		r:    r,
		enc:  e.enc,
		skip: off - startChunk*pcs,
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
	r         io.ReadCloser
	enc       Encryptor
	buf       []byte
	cipherBuf []byte
	skip      int64
}

func (r *chunkDecryptReader) Read(p []byte) (int, error) {
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}

	// Read 4-byte ciphertext length header.
	var header [4]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return 0, fmt.Errorf("Decrypt: truncated chunk header")
		}
		return 0, err // io.EOF propagates naturally
	}
	chunkLen := binary.BigEndian.Uint32(header[:])

	// Read ciphertext.
	if int(chunkLen) > cap(r.cipherBuf) {
		r.cipherBuf = make([]byte, chunkLen)
	}
	ct := r.cipherBuf[:chunkLen]
	if _, err := io.ReadFull(r.r, ct); err != nil {
		return 0, fmt.Errorf("Decrypt: read chunk: %s", err)
	}

	plain, err := r.enc.Decrypt(ct)
	if err != nil {
		return 0, fmt.Errorf("Decrypt: %s", err)
	}

	if r.skip > 0 {
		if r.skip >= int64(len(plain)) {
			r.skip -= int64(len(plain))
			return 0, nil
		}
		plain = plain[r.skip:]
		r.skip = 0
	}

	n := copy(p, plain)
	if n < len(plain) {
		r.buf = plain[n:]
	}
	return n, nil
}

func (r *chunkDecryptReader) Close() error { return r.r.Close() }

func (e *chunkedEncrypted) encryptAndWriteChunk(w io.Writer, plain []byte) error {
	ct, err := e.enc.Encrypt(plain)
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(ct)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(ct)
	return err
}

func (e *chunkedEncrypted) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	f, err := os.CreateTemp("", "jenc")
	if err != nil {
		return fmt.Errorf("Encrypt: create temp file: %s", err)
	}
	_ = os.Remove(f.Name())
	defer f.Close()

	if err := e.writeFileHeader(f); err != nil {
		return err
	}

	buf := make([]byte, e.plainChunkSize)
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

func (e *chunkedEncrypted) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	limits := e.ObjectStorage.Limits()
	// Non-first parts must satisfy MinPartSize.
	if limits.MinPartSize > 0 && e.encChunkSize < int64(limits.MinPartSize) {
		return nil, notSupported
	}
	// First part = fileHeaderSize + encChunkSize; must not exceed MaxPartSize.
	if limits.MaxPartSize > 0 && int64(fileHeaderSize)+e.encChunkSize > limits.MaxPartSize {
		return nil, notSupported
	}
	upload, err := e.ObjectStorage.CreateMultipartUpload(ctx, key)
	if err != nil {
		return nil, err
	}
	upload.MinPartSize = e.plainChunkSize
	return upload, nil
}

func (e *chunkedEncrypted) UploadPart(ctx context.Context, key string, uploadID string, num int, body []byte) (*Part, error) {
	var buf bytes.Buffer
	if num == 1 {
		if err := e.writeFileHeader(&buf); err != nil {
			return nil, err
		}
	}

	for len(body) > 0 {
		n := e.plainChunkSize
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
