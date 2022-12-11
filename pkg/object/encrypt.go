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
	"io/ioutil"
	"strings"

	"github.com/youmark/pkcs8"
	"golang.org/x/crypto/chacha20poly1305"
)

type Encryptor interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

type rsaEncryptor struct {
	privKey *rsa.PrivateKey
	label   []byte
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

func ParseRsaPrivateKeyFromPem(enc []byte, passphrase []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(enc)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}
	buf := block.Bytes
	if passphrase != nil {
		var err error
		// nolint:staticcheck
		buf, err = x509.DecryptPEMBlock(block, []byte(passphrase))
		if err != nil {
			if err == x509.IncorrectPasswordError {
				return nil, err
			}
			privKey, err := pkcs8.ParsePKCS8PrivateKeyRSA(block.Bytes, []byte(passphrase))
			if err == nil {
				return privKey, nil
			}
			privKey, err = pkcs8.ParsePKCS8PrivateKeyRSA(block.Bytes, nil)
			if err == nil {
				return privKey, nil
			}
			if !strings.Contains(err.Error(), "ParsePKCS1PrivateKey") {
				return nil, fmt.Errorf("cannot decode encrypted private keys: %v", err)
			}
			buf = block.Bytes
		}
	}

	priv, err := x509.ParsePKCS1PrivateKey(buf)
	if err == nil {
		return priv, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(buf)
	if err != nil {
		return nil, err
	}
	if priv, ok := key.(*rsa.PrivateKey); ok {
		return priv, nil
	}
	return nil, fmt.Errorf("is not RSA private key")
}

func ParseRsaPrivateKeyFromPath(path, passphrase string) (*rsa.PrivateKey, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRsaPrivateKeyFromPem(b, []byte(passphrase))
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

func (e *encrypted) Get(key string, off, limit int64) (io.ReadCloser, error) {
	r, err := e.ObjectStorage.Get(key, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	ciphertext, err := ioutil.ReadAll(r)
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
	return ioutil.NopCloser(bytes.NewBuffer(data)), nil
}

func (e *encrypted) Put(key string, in io.Reader) error {
	plain, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	ciphertext, err := e.enc.Encrypt(plain)
	if err != nil {
		return err
	}
	return e.ObjectStorage.Put(key, bytes.NewReader(ciphertext))
}

var _ ObjectStorage = &encrypted{}
