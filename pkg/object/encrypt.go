/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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

func ParseRsaPrivateKeyFromPem(privPEM string, passphrase string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}

	buf := block.Bytes
	// nolint:staticcheck
	if strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") &&
		x509.IsEncryptedPEMBlock(block) {
		if passphrase == "" {
			return nil, fmt.Errorf("passphrase is required to private key")
		}
		var err error
		// nolint:staticcheck
		buf, err = x509.DecryptPEMBlock(block, []byte(passphrase))
		if err != nil {
			if err == x509.IncorrectPasswordError {
				return nil, err
			}
			return nil, fmt.Errorf("cannot decode encrypted private keys: %v", err)
		}
	} else if passphrase != "" {
		logger.Warningf("passphrase is not used, because private key is not encrypted")
	}

	priv, err := x509.ParsePKCS1PrivateKey(buf)
	if err != nil {
		return nil, err
	}

	return priv, nil
}

func ParseRsaPrivateKeyFromPath(path, passphrase string) (*rsa.PrivateKey, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRsaPrivateKeyFromPem(string(b), passphrase)
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

type aesEncryptor struct {
	keyEncryptor Encryptor
	keyLen       int
}

func NewAESEncryptor(keyEncryptor Encryptor) Encryptor {
	return &aesEncryptor{keyEncryptor, 32} //  AES-256-GCM
}

func (e *aesEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	key := make([]byte, e.keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	cipherkey, err := e.keyEncryptor.Encrypt(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	headerSize := 3 + len(cipherkey) + len(nonce)
	buf := make([]byte, headerSize+len(plaintext)+aesgcm.Overhead())
	buf[0] = byte(len(cipherkey) >> 8)
	buf[1] = byte(len(cipherkey) & 0xFF)
	buf[2] = byte(len(nonce))
	p := buf[3:]
	copy(p, cipherkey)
	p = p[len(cipherkey):]
	copy(p, nonce)
	p = p[len(nonce):]
	ciphertext := aesgcm.Seal(p[:0], nonce, plaintext, nil)
	return buf[:headerSize+len(ciphertext)], nil
}

func (e *aesEncryptor) Decrypt(ciphertext []byte) ([]byte, error) {
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
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aesgcm.Open(ciphertext[:0], nonce, ciphertext, nil)
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
		return nil, io.EOF
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
