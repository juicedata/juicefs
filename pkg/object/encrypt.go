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
	"strings"

	"github.com/emmansun/gmsm/pkcs8"
	"github.com/emmansun/gmsm/sm2"
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
)

func NewDataEncryptor(keyEncryptor Encryptor, algo string) (Encryptor, error) {
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
	}
	return nil, fmt.Errorf("unsupport cipher: %s", algo)
}

func (e *dataEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	key := make([]byte, e.keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	cipherkey, err := e.keyEncryptor.Encrypt(key)
	if err != nil {
		return nil, err
	}
	aead, err := e.aead(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	headerSize := 3 + len(cipherkey) + len(nonce)
	buf := make([]byte, headerSize+len(plaintext)+aead.Overhead())
	buf[0] = byte(len(cipherkey) >> 8)
	buf[1] = byte(len(cipherkey) & 0xFF)
	buf[2] = byte(len(nonce))
	p := buf[3:]
	copy(p, cipherkey)
	p = p[len(cipherkey):]
	copy(p, nonce)
	p = p[len(nonce):]
	ciphertext := aead.Seal(p[:0], nonce, plaintext, nil)
	return buf[:headerSize+len(ciphertext)], nil
}

func (e *dataEncryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 3 {
		return nil, fmt.Errorf("received encrypted text length is less than 3, the object is corrupted")
	}
	keyLen := int(ciphertext[0])<<8 + int(ciphertext[1])
	nonceLen := int(ciphertext[2])
	if 3+keyLen+nonceLen >= len(ciphertext) {
		return nil, fmt.Errorf("misformed ciphertext: %d %d", keyLen, nonceLen)
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

type encrypted struct {
	ObjectStorage
	enc Encryptor
}

// NewEncrypted returns a encrypted object storage
func NewEncrypted(o ObjectStorage, enc Encryptor) ObjectStorage {
	return &encrypted{o, enc}
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
	plain, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	ciphertext, err := e.enc.Encrypt(plain)
	if err != nil {
		return err
	}
	return e.ObjectStorage.Put(ctx, key, bytes.NewReader(ciphertext), getters...)
}

var _ ObjectStorage = (*encrypted)(nil)
