/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/emmansun/gmsm/pkcs8"
	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/sm4"
	"golang.org/x/crypto/chacha20poly1305"
)

type Encryptor interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

func ExportRsaPrivateKeyToPem(key *rsa.PrivateKey, passphrase string) string {
	buf := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: buf,
	}
	if passphrase != "" {
		var err error
		// nolint:staticcheck
		block, _ = x509.EncryptPEMBlock(rand.Reader, block.Type, buf, []byte(passphrase), x509.PEMCipherAES256)
		if err != nil {
			panic(err)
		}
	}
	privPEM := pem.EncodeToMemory(block)
	return string(privPEM)
}

var ErrKeyNeedPasswd = errors.New("passphrase is required to private key")

func ParsePrivateKeyFromPem(enc []byte, passphrase []byte) (any, error) {
	block, _ := pem.Decode(enc)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}

	buf := block.Bytes
	if len(passphrase) == 0 {
		// nolint:staticcheck
		if strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") && x509.IsEncryptedPEMBlock(block) {
			return nil, ErrKeyNeedPasswd
		}
		if strings.Contains(block.Type, "ENCRYPTED") {
			return nil, ErrKeyNeedPasswd
		}
	} else {
		var err error
		// nolint:staticcheck
		buf, err = x509.DecryptPEMBlock(block, passphrase)
		if err != nil {
			if err == x509.IncorrectPasswordError {
				return nil, err
			}
			key, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, passphrase)
			if err == nil {
				return key, nil
			}
			key, err = pkcs8.ParsePKCS8PrivateKey(block.Bytes)
			if err == nil {
				return key, nil
			}
			if !strings.Contains(err.Error(), "ParsePKCS1PrivateKey") {
				return nil, fmt.Errorf("cannot decode encrypted private keys: %v", err)
			}
			buf = block.Bytes
		}
	}

	rsaKey, err := x509.ParsePKCS1PrivateKey(buf)
	if err == nil {
		return rsaKey, nil
	}
	key, err := pkcs8.ParsePKCS8PrivateKey(buf)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func ParseRsaPrivateKeyFromPath(path, passphrase string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePrivateKeyFromPem(b, []byte(passphrase))
}

type rsaEncryptor struct {
	privKey *rsa.PrivateKey
	label   []byte
}

func NewRSAEncryptor(privKey *rsa.PrivateKey) Encryptor {
	return &rsaEncryptor{privKey, []byte("keys")}
}

func (e *rsaEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	return rsa.EncryptOAEP(sha256.New(), rand.Reader, &e.privKey.PublicKey, plaintext, e.label)
}

func (e *rsaEncryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	return rsa.DecryptOAEP(sha256.New(), rand.Reader, e.privKey, ciphertext, e.label)
}

type sm2Encryptor struct {
	privKey *sm2.PrivateKey
}

func NewSM2Encryptor(privKey *sm2.PrivateKey) Encryptor {
	return &sm2Encryptor{privKey}
}

func (e *sm2Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	return sm2.EncryptASN1(rand.Reader, &e.privKey.PublicKey, plaintext)
}

func (e *sm2Encryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	return sm2.Decrypt(e.privKey, ciphertext)
}

func NewKeyEncryptor(privKey any) Encryptor {
	switch k := privKey.(type) {
	case *rsa.PrivateKey:
		return NewRSAEncryptor(k)
	case *sm2.PrivateKey:
		return NewSM2Encryptor(k)
	}
	panic(fmt.Sprintf("unsupported key type %T", privKey)) // should not happen
}

type dataEncryptor struct {
	keyEncryptor Encryptor
	keyLen       int
	aead         func(key []byte) (cipher.AEAD, error)
}

const (
	AES256GCM_RSA = "aes256gcm-rsa"
	CHACHA20_RSA  = "chacha20-rsa"
	SM4GCM        = "sm4gcm"
)

func NewDataEncryptor(keyEncryptor Encryptor, algo string) (*dataEncryptor, error) {
	switch algo {
	case "", AES256GCM_RSA:
		aead := func(key []byte) (cipher.AEAD, error) {
			block, err := aes.NewCipher(key)
			if err != nil {
				return nil, err
			}
			return cipher.NewGCM(block)
		}
		return &dataEncryptor{keyEncryptor, 32, aead}, nil
	case CHACHA20_RSA:
		return &dataEncryptor{keyEncryptor, chacha20poly1305.KeySize, chacha20poly1305.New}, nil
	case SM4GCM:
		// TODO: support other modes?
		// GCM not in [GB/T 17964-2021](http://c.gb688.cn/bzgk/gb/showGb?type=online&hcno=4F89D833626340B1F71068D25EAC737D)
		aead := func(key []byte) (cipher.AEAD, error) {
			block, err := sm4.NewCipher(key)
			if err != nil {
				return nil, err
			}
			return cipher.NewGCM(block)
		}
		return &dataEncryptor{keyEncryptor, 16, aead}, nil
	}
	return nil, fmt.Errorf("unsupport cipher: %s", algo)
}

func asn1TLVLen(contentLen int) int {
	return asn1HeaderLen(contentLen) + contentLen
}

func asn1HeaderLen(contentLen int) int {
	return 1 + asn1LenLen(contentLen)
}

func asn1LenLen(contentLen int) int {
	n := 1
	for v := contentLen; v > 255; v >>= 8 {
		n++
	}
	if contentLen < 128 {
		return 1
	}
	return 1 + n
}

func (e *dataEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	buf := make([]byte, e.MaxOverhead()+len(plaintext))
	n, err := e.EncryptInto(buf, plaintext)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// EncryptInto encrypts plaintext using a freshly generated data key
// and writes the envelope (wrapped-key length || nonce length ||
// wrapped key || nonce || ciphertext+AEAD tag) directly into dst.
// Returns the total number of bytes written.
//
// dst must have len >= MaxOverhead() + len(plaintext); shorter dst
// returns io.ErrShortBuffer.
//
// Exposing this in-place form lets chunked encryption avoid one
// per-chunk allocation and one memcpy by sealing straight into a
// pooled chunk buffer instead of through an intermediate slice.
func (e *dataEncryptor) EncryptInto(dst, plaintext []byte) (int, error) {
	key := make([]byte, e.keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return 0, err
	}
	cipherkey, err := e.keyEncryptor.Encrypt(key)
	if err != nil {
		return 0, err
	}
	aead, err := e.aead(key)
	if err != nil {
		return 0, err
	}
	nonceSize := aead.NonceSize()
	headerSize := 3 + len(cipherkey) + nonceSize
	need := headerSize + len(plaintext) + aead.Overhead()
	if len(dst) < need {
		return 0, io.ErrShortBuffer
	}
	dst[0] = byte(len(cipherkey) >> 8)
	dst[1] = byte(len(cipherkey) & 0xFF)
	dst[2] = byte(nonceSize)
	p := dst[3:]
	copy(p, cipherkey)
	p = p[len(cipherkey):]
	// Write the nonce straight into its final position in dst rather
	// than allocating + copying. Seal only reads nonce once (at the
	// start of the GHASH/Poly1305 initialisation) so placing it in
	// dst's earlier region cannot interfere with the ciphertext that
	// Seal appends to p[:0].
	nonce := p[:nonceSize]
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return 0, err
	}
	p = p[nonceSize:]
	ciphertext := aead.Seal(p[:0], nonce, plaintext, nil)
	return headerSize + len(ciphertext), nil
}

func (e *dataEncryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 3 {
		return nil, fmt.Errorf("received encrypted text length is less than 3, the object is corrupted")
	}
	keyLen := int(ciphertext[0])<<8 + int(ciphertext[1])
	nonceLen := int(ciphertext[2])
	if 3+keyLen+nonceLen >= len(ciphertext) {
		return nil, fmt.Errorf("malformed ciphertext: %d %d", keyLen, nonceLen)
	}
	ciphertext = ciphertext[3:]
	cipherkey := ciphertext[:keyLen]
	nonce := ciphertext[keyLen : keyLen+nonceLen]
	ciphertext = ciphertext[keyLen+nonceLen:]

	key, err := e.keyEncryptor.Decrypt(cipherkey)
	if err != nil {
		return nil, errors.New("decryt key: " + err.Error())
	}
	aead, err := e.aead(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(ciphertext[:0], nonce, ciphertext, nil)
}

// MaxOverhead returns the maximum number of extra bytes that Encrypt can add.
// Layout is:
//
//	2 bytes wrapped-key length
//	1 byte nonce length
//	wrapped encrypted data key
//	nonce
//	AEAD tag
func (e *dataEncryptor) MaxOverhead() int {
	aead, err := e.aead(make([]byte, e.keyLen))
	if err != nil {
		panic(err)
	}
	var wrappedKeyLen int
	switch ke := e.keyEncryptor.(type) {
	case *rsaEncryptor:
		wrappedKeyLen = ke.privKey.Size()
	case *sm2Encryptor:
		coordLen := (ke.privKey.Curve.Params().BitSize + 7) / 8
		intLen := asn1TLVLen(coordLen + 1)
		c3Len := asn1TLVLen(sm3.Size)
		c2Len := asn1TLVLen(e.keyLen)
		wrappedKeyLen = asn1TLVLen(intLen + intLen + c3Len + c2Len)
	default:
		panic(fmt.Sprintf("unsupported key encryptor %T", e.keyEncryptor))
	}
	return 2 + 1 + wrappedKeyLen + aead.NonceSize() + aead.Overhead()
}

// DefaultEncryptedMaxObjectSize is the upper bound NewEncrypted enforces on
// Put inputs. Because the non-chunked wrapper buffers the full plaintext
// (Put) and full ciphertext (Get) in memory, callers must keep individual
// objects well below available RAM. Override at runtime with the
// JFS_ENCRYPTED_MAX_OBJECT_SIZE environment variable (in bytes).
const DefaultEncryptedMaxObjectSize = 64 << 20 // 64 MiB

// ErrEncryptedObjectTooLarge is returned by (*encrypted).Put when the input
// stream exceeds the configured maximum object size.
var ErrEncryptedObjectTooLarge = errors.New("encrypted object exceeds configured maximum size")

func encryptedMaxObjectSize() int64 {
	if v := os.Getenv("JFS_ENCRYPTED_MAX_OBJECT_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return DefaultEncryptedMaxObjectSize
}

type encrypted struct {
	ObjectStorage
	enc           Encryptor
	maxObjectSize int64
}

// NewEncrypted returns an object storage wrapper that encrypts every object
// as a single ciphertext blob.
//
// Deprecated: this wrapper decrypts the full ciphertext into memory on every
// Get (ignoring the requested range until after decryption) and buffers the
// full plaintext on every Put. It is only safe for objects whose size is
// small relative to available memory. New callers should use
// [NewChunkedEncrypted], which streams fixed-size chunks and honours
// ranged reads. Put rejects inputs larger than
// [DefaultEncryptedMaxObjectSize] (overridable via the
// JFS_ENCRYPTED_MAX_OBJECT_SIZE environment variable) to guard against
// accidental OOMs.
func NewEncrypted(o ObjectStorage, enc Encryptor) ObjectStorage {
	return &encrypted{ObjectStorage: o, enc: enc, maxObjectSize: encryptedMaxObjectSize()}
}

func (e *encrypted) String() string {
	return fmt.Sprintf("%s(encrypted)", e.ObjectStorage)
}

func (e *encrypted) Get(ctx context.Context, key string, off, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	r, err := e.ObjectStorage.Get(ctx, key, 0, -1, getters...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	ciphertext, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	plain, err := e.enc.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("Decrypt: %s", err)
	}
	l := int64(len(plain))
	if off > l {
		off = l
	}
	if limit == -1 || off+limit > l {
		limit = l - off
	}
	data := plain[off : off+limit]
	return io.NopCloser(bytes.NewBuffer(data)), nil
}

func (e *encrypted) Put(ctx context.Context, key string, in io.Reader, getters ...AttrGetter) error {
	// Bound the read so an oversized stream cannot OOM the process. We allow
	// one extra byte past the limit so that exactly-at-limit inputs succeed
	// and over-limit inputs are detectable without scanning the rest of the
	// stream.
	limited := &io.LimitedReader{R: in, N: e.maxObjectSize + 1}
	plain, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(plain)) > e.maxObjectSize {
		return fmt.Errorf("%w: %q over %d bytes", ErrEncryptedObjectTooLarge, key, e.maxObjectSize)
	}
	ciphertext, err := e.enc.Encrypt(plain)
	if err != nil {
		return err
	}
	return e.ObjectStorage.Put(ctx, key, bytes.NewReader(ciphertext), getters...)
}

func (e *encrypted) SetTier(init Tiers) error {
	if o, ok := e.ObjectStorage.(SupportTier); ok {
		if err := o.SetTier(init); err != nil {
			return err
		}
	}
	return nil
}

func (e *encrypted) GetStorageClass(ctx context.Context) string {
	if o, ok := e.ObjectStorage.(SupportTier); ok {
		return o.GetStorageClass(ctx)
	}
	return ""
}

var _ ObjectStorage = (*encrypted)(nil)
var _ SupportTier = (*encrypted)(nil)
