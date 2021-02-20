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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"io/ioutil"
	"os/exec"
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
	key2, _ := ParseRsaPrivateKeyFromPem(privPEM, "abc")
	c2 := NewRSAEncryptor(key2)
	plaintext, _ := c2.Decrypt(ciphertext)
	if string(plaintext) != "hello" {
		t.Fail()
	}

	_, err := ParseRsaPrivateKeyFromPem(privPEM, "")
	if err == nil {
		t.Errorf("parse without passphrase should fail")
		t.Fail()
	}
	_, err = ParseRsaPrivateKeyFromPem(privPEM, "ab")
	if err != x509.IncorrectPasswordError {
		t.Errorf("parse without passphrase should return IncorrectPasswordError")
		t.Fail()
	}

	_ = exec.Command("openssl", "genrsa", "-out", "/tmp/private.pem", "2048").Run()
	if _, err = ParseRsaPrivateKeyFromPath("/tmp/private.pem", ""); err != nil {
		t.Error(err)
		t.Fail()
	}
	_ = exec.Command("openssl", "genrsa", "-out", "/tmp/private.pem", "-aes256", "-passout", "pass:abcd", "2048").Run()
	if _, err = ParseRsaPrivateKeyFromPath("/tmp/private.pem", "abcd"); err != nil {
		t.Error(err)
		t.Fail()
	}
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

func TestAESGCM(t *testing.T) {
	kc := NewRSAEncryptor(testkey)
	dc := NewAESEncryptor(kc)
	data := []byte("hello")
	ciphertext, _ := dc.Encrypt(data)
	plaintext, _ := dc.Decrypt(ciphertext)
	if !bytes.Equal(data, plaintext) {
		t.Errorf("decrypt fail")
		t.Fail()
	}
}

func TestEncryptedStore(t *testing.T) {
	s, _ := CreateStorage("mem", "", "", "")
	kc := NewRSAEncryptor(testkey)
	dc := NewAESEncryptor(kc)
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
