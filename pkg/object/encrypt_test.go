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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io/ioutil"
	"path/filepath"
	"testing"
)

var testkey = GenerateRsaKeyPair()

func GenerateRsaKeyPair() *rsa.PrivateKey {
	privkey, _ := rsa.GenerateKey(rand.Reader, 2048)
	return privkey
}

func TestRSA(t *testing.T) {
	c1 := NewRSAEncryptor(testkey)
	ciphertext, _ := c1.Encrypt([]byte("hello"))

	privPEM := ExportRsaPrivateKeyToPem(testkey, "abc")
	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		t.Fatalf("failed to parse PEM block containing the key")
	}

	key2, _ := ParseRsaPrivateKeyFromPem(block, "abc")
	c2 := NewRSAEncryptor(key2)
	plaintext, _ := c2.Decrypt(ciphertext)
	if string(plaintext) != "hello" {
		t.Fail()
	}

	_, err := ParseRsaPrivateKeyFromPem(block, "")
	if err == nil {
		t.Errorf("parse without passphrase should fail")
		t.Fail()
	}
	_, err = ParseRsaPrivateKeyFromPem(block, "ab")
	if err == nil {
		t.Errorf("parse with incorrect passphrase should return fail")
		t.Fail()
	}

	dir := t.TempDir()

	if err := genrsa(filepath.Join(dir, "private.pem"), ""); err != nil {
		t.Error(err)
		t.Fail()
	}
	if _, err = ParseRsaPrivateKeyFromPath(filepath.Join(dir, "private.pem"), ""); err != nil {
		t.Error(err)
		t.Fail()
	}

	if err := genrsa(filepath.Join(dir, "private.pem"), "abcd"); err != nil {
		t.Error(err)
		t.Fail()
	}
	if _, err = ParseRsaPrivateKeyFromPath(filepath.Join(dir, "private.pem"), "abcd"); err != nil {
		t.Error(err)
		t.Fail()
	}
}

func genrsa(path string, password string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	if password != "" {
		// nolint:staticcheck
		block, err = x509.EncryptPEMBlock(rand.Reader, block.Type, block.Bytes, []byte(password), x509.PEMCipherAES256)
		if err != nil {
			return err
		}
	}
	if err := ioutil.WriteFile(path, pem.EncodeToMemory(block), 0755); err != nil {
		return err
	}
	return nil
}

func BenchmarkRSA4096Encrypt(b *testing.B) {
	secret := make([]byte, 32)
	kc := NewRSAEncryptor(testkey)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = kc.Encrypt(secret)
	}
}

func BenchmarkRSA4096Decrypt(b *testing.B) {
	secret := make([]byte, 32)
	kc := NewRSAEncryptor(testkey)
	ciphertext, _ := kc.Encrypt(secret)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = kc.Decrypt(ciphertext)
	}
}

func TestChaCha20(t *testing.T) {
	kc := NewRSAEncryptor(testkey)
	dc, _ := NewDataEncryptor(kc, CHACHA20_RSA)
	data := []byte("hello")
	ciphertext, _ := dc.Encrypt(data)
	plaintext, _ := dc.Decrypt(ciphertext)
	if !bytes.Equal(data, plaintext) {
		t.Errorf("decrypt fail")
		t.Fail()
	}
}

func TestAESGCM(t *testing.T) {
	kc := NewRSAEncryptor(testkey)
	dc, _ := NewDataEncryptor(kc, AES256GCM_RSA)
	data := []byte("hello")
	ciphertext, _ := dc.Encrypt(data)
	plaintext, _ := dc.Decrypt(ciphertext)
	if !bytes.Equal(data, plaintext) {
		t.Errorf("decrypt fail")
		t.Fail()
	}
}

func TestEncryptedStore(t *testing.T) {
	s, _ := CreateStorage("mem", "", "", "", "")
	kc := NewRSAEncryptor(testkey)
	dc, _ := NewDataEncryptor(kc, AES256GCM_RSA)
	es := NewEncrypted(s, dc)
	_ = es.Put("a", bytes.NewReader([]byte("hello")))
	r, err := es.Get("a", 1, 2)
	if err != nil {
		t.Errorf("Get a: %s", err)
		t.Fail()
	}
	d, _ := ioutil.ReadAll(r)
	if string(d) != "el" {
		t.Fail()
	}

	r, _ = es.Get("a", 0, -1)
	d, _ = ioutil.ReadAll(r)
	if string(d) != "hello" {
		t.Fail()
	}
}
