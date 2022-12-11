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

var keyInPKCS8 = `-----BEGIN ENCRYPTED PRIVATE KEY-----
MIICzzBJBgkqhkiG9w0BBQ0wPDAbBgkqhkiG9w0BBQwwDgQIEEEvSFbVLkUCAggA
MB0GCWCGSAFlAwQBKgQQhuaBA77wcAHA7bl4dkbFsQSCAoBi5hgqWhK2ic3HBSUX
JBFFh7omdhU4uK7mQzVVx/RvnUCbw5T7ghfboJhP5bHkj+UnnFiKD6vFZfSgH/Q3
5KUjPIveLa0urly1bC1SMequXggjEgSPUe8XBjWJJcwkbELFiQzD76GSnveCMokZ
X7WvoZeY0AaSAnQwe5r1evAdilWXdb2fUmRA23gco8pgSrkdVPyz9lb+FbDjrd8j
7qiMDcoZ4qFrQ4v8IQJv+ED5f7fLen7UGpG5uOZT9Ez153f7Zw+eEAmp5qwE5SCP
JbVLsR++HXkntJg1q2Yw4rIOi7qing408jwroed/W6AzS8A49RvrI2/Ac5dHfEnB
LkC23Ep47/e9B8cZQCmIZXEnUpcjSwWKe5U4nCXyeskuhRhTtA3EpYFXx+/P3yNE
YISywz6brtAxDwfk8LNAGkZRQ5c+nIFh43M+m5LLHAOSug/TbIvVvgottdc0VRHl
Q72zeXu8X7PF8dhnoxVSBdKfRYCHQWg+PBw8IYn1KA1SfvwakeVnYcU8P4BMOXo5
36Q4CVDIW9zWCrW49Cq/dxi0yqYyoA5hw8lIqMzmewdiUH0BwlsaOBz0utz0GhOi
mBsK7O5819orKnuGmWzuvEETiRJ+HZTgkWAC0Wu1r7gjbMKow8grkygQz0iqMrSE
kY7gfcnT2mpR7ow0DbWqiidb4PrxYsk0X1hOswsAek62xL/sdqlA9C3eZuqPNfqa
yatWjKjQY4ukKUm7QplPOgOuP01GN0XF7zMEqXtl1GxPp9uDnKFzDopQau+3OrID
ljSQG+zYqxPFeLZ06zh3bYqS5E5RjlguF6055m5NaudQ9b+/7NjPDHdpWth6cQFx
OIGw
-----END ENCRYPTED PRIVATE KEY-----
`

var pemWithoutPass = `-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQCaPxhJMEfX0CaYIziQxwjlVlh75xw1xWlF14GGdpZYaM70BzMu
XdB22X7PnkK38PHk4saXKz0blhaf/qllJP7mcdqFEcs4sWsVhU1KoLdRNH/1AJQK
0/Oehr3vov1CjsR+51RRuDFcVOBd7lpglK5s0+TGRYyImFc+JhZ23RVFNwIDAQAB
AoGAGtobDzqxdxeMcHXJNiMAIHScqM098vpv7jGrIc5pM/Di/kZ2mX7JeLc6RUiG
0uDGK5NzAQQM+k1xmN7LfIkpOo2pSlL0gC/M6q0TAJqRLXBKjMVqlHLUytqKTtEg
4PeF93GnxJZt9NSqo5HH87OwkjXeG1brqhZTfKtL/tbRpRECQQDLJAIFri3pGzni
Xq4s2NogxUnC8cg9I7jEv4gTH/KuFQTsh/5i2+1tsGyFdXKzFd0A8DcZx+MzBm7q
qwF46vw5AkEAwmIN0s9EcUVeVyorgdphl81QV+x9TR5wZbksigPQcNqU2NJVZKtd
1f0o2H3E2XHV2DLjeLWctlmx+i0k3Sos7wJAJR/Sgsk/OK+yF22oNSf4TS7g+RCI
wKurk8FRE/WtuyS6PqPn2JdKv9YTLxy0tofTWN2NpFeEbQnK8XYJEdkX+QJAR/GC
rEOKUWIbSKeS8ryg4k5bLi+ZMLHTZ9LhaTOAMkS0UouGj3vdfxXzyCzEbrZzL1Gm
X0bYeaU4+h87RaAWgQJBALhNFDDGXnEd0Lj2pUhBcdaRXGqrg8PZWekr0GLDPEvO
s+yhHoqRlGKUwQtwwB3HCIEWxe7siOa0YTy9MJ5QySY=
-----END RSA PRIVATE KEY-----`

func TestParsePKCS8(t *testing.T) {
	_, err := ParseRsaPrivateKeyFromPem([]byte(keyInPKCS8), []byte("12345678"))
	if err != nil {
		t.Fatalf("parse rsa: %s", err)
	}
	_, err = ParseRsaPrivateKeyFromPem([]byte(keyInPKCS8), []byte("1234567"))
	if err == nil {
		t.Fatalf("parse rsa: %s", err)
	}
}

func TestParsePemWithoutPassword(t *testing.T) {
	_, err := ParseRsaPrivateKeyFromPem([]byte(pemWithoutPass), nil)
	if err != nil {
		t.Fatalf("parse rsa: %s", err)
	}
	_, err = ParseRsaPrivateKeyFromPem([]byte(pemWithoutPass), []byte("123"))
	if err != nil {
		t.Fatalf("parse rsa: %s", err)
	}
}

func GenerateRsaKeyPair() *rsa.PrivateKey {
	privkey, _ := rsa.GenerateKey(rand.Reader, 2048)
	return privkey
}

func TestRSA(t *testing.T) {
	c1 := NewRSAEncryptor(testkey)
	ciphertext, _ := c1.Encrypt([]byte("hello"))

	privPEM := ExportRsaPrivateKeyToPem(testkey, "abc")

	key2, _ := ParseRsaPrivateKeyFromPem([]byte(privPEM), []byte("abc"))
	c2 := NewRSAEncryptor(key2)
	plaintext, _ := c2.Decrypt(ciphertext)
	if string(plaintext) != "hello" {
		t.Fail()
	}

	_, err := ParseRsaPrivateKeyFromPem([]byte(privPEM), nil)
	if err == nil {
		t.Errorf("parse without passphrase should fail")
		t.Fail()
	}
	_, err = ParseRsaPrivateKeyFromPem([]byte(privPEM), []byte("ab"))
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
