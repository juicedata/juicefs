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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmansun/gmsm/sm2"
	"github.com/stretchr/testify/require"
)

var rsaInPKCS8 = `-----BEGIN ENCRYPTED PRIVATE KEY-----
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

var rsaInPKCS1 = `-----BEGIN RSA PRIVATE KEY-----
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

var sm2InPKCS8Plain = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqBHM9VAYItBG0wawIBAQQgUWhfo4lpH0j/Toc7
ESiTd+1FsWJgIR9MlBVeQ0lYi62hRANCAASAuZzZAg6zj+ZXclqBx0UfZVNeN9+R
L5MzLV1dmrLZQqbt+j8oDAN3QU3VPXziKzGttdTvgItUrLavxaCMXOL+
-----END PRIVATE KEY-----`

var sm2InPKCS8WithSM4 = `-----BEGIN ENCRYPTED PRIVATE KEY-----
MIHzMF4GCSqGSIb3DQEFDTBRMDEGCSqGSIb3DQEFDDAkBBAm2QGHlzGOPNqAyOCZ
zWOrAgIIADAMBggqhkiG9w0CCQUAMBwGCCqBHM9VAWgCBBC0yGrxfFu2t51rF8RX
N/4+BIGQkIa24K2nOv+fkmohJHaya9b+LJUs6VR50K+2n3QuJokRvxlGB9TknxDs
e3ZJfNKRoksL7V4Ttd82pgF6a68jBB0//iOSysc6d/ovx5oKrJ8kx+t/U5NbxWRV
8UrHPN50rzxS4l6niklnwUM2q36Lf6R+xYduTVmTfWDAAPFSRIlKUDmhgPlT8MHB
jxqPfZVO
-----END ENCRYPTED PRIVATE KEY-----`

var sm2InPKCS8WithAES = `-----BEGIN ENCRYPTED PRIVATE KEY-----
MIHzMF4GCSqGSIb3DQEFDTBRMDAGCSqGSIb3DQEFDDAjBBA19eEcvLDwQqrQx0Yo
4vKAAgFkMAwGCCqGSIb3DQIJBQAwHQYJYIZIAWUDBAEqBBCniW2M8JL78D06Hqxk
hQtcBIGQd7zfctW4ry2MqfNpnsx5L2kT6Sv11ecehBJt8e9C/d33YLjBuAA9GTLO
Aoz7Z9lb9ivf/TZL0EXBI7llNQitxV+NEx32jCpwO3rEoFUqoGZZh2jcRmLsufS2
pwq8iHhypwUx6EDLJXTXOFlMsqgHYC1ZV9LqnmdLAKyqXQeHtGN9QZgDQwy221yi
xI3CLucj
-----END ENCRYPTED PRIVATE KEY-----`

func TestParsePrivateKey(t *testing.T) {
	var cases = []struct {
		name   string
		key    string
		pass   []byte
		expect bool
	}{
		{"rsa key in pkcs#1, parse without passphrase", rsaInPKCS1, nil, true},
		{"rsa key in pkcs#1, parse with passphrase", rsaInPKCS1, []byte("123"), true},
		{"rsa key in pkcs#8, parse with correct passphrase", rsaInPKCS8, []byte("12345678"), true},
		{"rsa key in pkcs#8, parse with incorrect passphrase", rsaInPKCS8, []byte("1234567"), false},
		{"rsa key in pkcs#8, parse without passphrase", rsaInPKCS8, nil, false},
		{"sm2 key in pkcs#8 plain, parse without passphrase", sm2InPKCS8Plain, nil, true},
		{"sm2 key in pkcs#8 plain, parse with passphrase", sm2InPKCS8Plain, []byte("any"), true},
		{"sm2 key in pkcs#8 with sm4, parse with correct passphrase", sm2InPKCS8WithSM4, []byte("12345678"), true},
		{"sm2 key in pkcs#8 with sm4, parse with incorrect passphrase", sm2InPKCS8WithSM4, []byte("1234567"), false},
		{"sm2 key in pkcs#8 with sm4, parse without passphrase", sm2InPKCS8WithSM4, nil, false},
		{"sm2 key in pkcs#8 with aes, parse with correct passphrase", sm2InPKCS8WithAES, []byte("12345678"), true},
		{"sm2 key in pkcs#8 with aes, parse with incorrect passphrase", sm2InPKCS8WithAES, []byte("1234567"), false},
		{"sm2 key in pkcs#8 with aes, parse without passphrase", sm2InPKCS8WithAES, nil, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParsePrivateKeyFromPem([]byte(c.key), c.pass)
			require.Equal(t, c.expect, err == nil, "unexpected result: %v", err)
		})
	}
}

func genPrivateKey(typ string) any {
	switch typ {
	case "rsa":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return key
	case "sm2":
		key, err := sm2.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		return key
	default:
		panic(fmt.Errorf("unknown key type: %s", typ))
	}
}

var rsaKey = genPrivateKey("rsa").(*rsa.PrivateKey)

func TestSM2(t *testing.T) {
	sm2Key := genPrivateKey("sm2").(*sm2.PrivateKey)
	sm2 := NewSM2Encryptor(sm2Key)
	cipherText, err := sm2.Encrypt([]byte("hello"))
	require.NoError(t, err)
	plainText, err := sm2.Decrypt(cipherText)
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), plainText)
}

func TestRSA(t *testing.T) {
	c1 := NewRSAEncryptor(rsaKey)
	ciphertext, _ := c1.Encrypt([]byte("hello"))

	privPEM := ExportRsaPrivateKeyToPem(rsaKey, "abc")

	key2, _ := ParsePrivateKeyFromPem([]byte(privPEM), []byte("abc"))
	c2 := NewKeyEncryptor(key2)
	plaintext, _ := c2.Decrypt(ciphertext)
	if string(plaintext) != "hello" {
		t.Fail()
	}

	_, err := ParsePrivateKeyFromPem([]byte(privPEM), nil)
	if err == nil {
		t.Errorf("parse without passphrase should fail")
		t.Fail()
	}
	_, err = ParsePrivateKeyFromPem([]byte(privPEM), []byte("ab"))
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
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0755); err != nil {
		return err
	}
	return nil
}

func BenchmarkKeyEncryptionKey(b *testing.B) {
	secret := make([]byte, 32)
	keyTypes := []string{"rsa", "sm2"}

	for _, typ := range keyTypes {
		ke := NewKeyEncryptor(genPrivateKey(typ))
		cipherText, _ := ke.Encrypt(secret)
		b.ResetTimer()
		b.Run(typ+"_encrypt", func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				_, _ = ke.Encrypt(secret)
			}
		})
		b.Run(typ+"_decrypt", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = ke.Decrypt(cipherText)
			}
		})
	}
}

func TestDataEncryptor(t *testing.T) {
	cases := []struct {
		name string
		kek  string
		algo string
	}{
		{"rsa_aesgcm", "rsa", AES256GCM_RSA},
		{"rsa_chacha20", "rsa", CHACHA20_RSA},
		{"sm2_sm4gcm", "sm2", SM4GCM},
	}
	data := []byte("hello")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ke := NewKeyEncryptor(genPrivateKey(c.kek))
			de, err := NewDataEncryptor(ke, c.algo)
			require.NoError(t, err, "failed to create data encryptor")
			cipherText, err := de.Encrypt(data)
			require.NoError(t, err, "failed to encrypt data")
			plainText, err := de.Decrypt(cipherText)
			require.NoError(t, err, "failed to decrypt data")
			require.Equal(t, data, plainText, "decrypted data not equal to original")
		})
	}
}

func BenchmarkDataEncryptor(b *testing.B) {
	cases := []struct {
		name string
		kek  string
		algo string
	}{
		{"rsa_aesgcm", "rsa", AES256GCM_RSA},
		{"rsa_chacha20", "rsa", CHACHA20_RSA},
		{"sm2_sm4gcm", "sm2", SM4GCM},
	}
	data := make([]byte, 4<<20)
	if _, err := rand.Read(data); err != nil {
		b.Fatalf("failed to generate random data: %v", err)
	}
	for _, c := range cases {
		ke := NewKeyEncryptor(genPrivateKey(c.kek))
		de, err := NewDataEncryptor(ke, c.algo)
		if err != nil {
			b.Fatalf("failed to create data encryptor: %v", err)
		}
		cipherText, err := de.Encrypt(data)
		if err != nil {
			b.Fatalf("failed to encrypt data: %v", err)
		}
		b.Run(c.name+"_encrypt", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = de.Encrypt(data)
			}
		})
		b.Run(c.name+"_decrypt", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = de.Decrypt(cipherText)
			}
		})
	}
}

func TestEncryptedStore(t *testing.T) {
	ctx := context.Background()
	s, _ := CreateStorage("mem", "", "", "", "")
	kc := NewRSAEncryptor(rsaKey)
	dc, _ := NewDataEncryptor(kc, AES256GCM_RSA)
	es := NewEncrypted(s, dc)
	_ = es.Put(ctx, "a", bytes.NewReader([]byte("hello")))
	r, err := es.Get(ctx, "a", 1, 2)
	if err != nil {
		t.Errorf("Get a: %s", err)
		t.Fail()
	}
	d, _ := io.ReadAll(r)
	if string(d) != "el" {
		t.Fail()
	}

	r, _ = es.Get(ctx, "a", 0, -1)
	d, _ = io.ReadAll(r)
	if string(d) != "hello" {
		t.Fail()
	}
	_ = s.Put(ctx, "emptyfile", bytes.NewReader([]byte("")))
	_, err = es.Get(ctx, "emptyfile", 0, -1)
	if err == nil || !strings.Contains(err.Error(), "the object is corrupted") {
		t.Fail()
	}
}
