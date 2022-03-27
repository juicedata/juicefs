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

package meta

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/juicedata/juicefs/pkg/version"
)

// Config for clients.
type Config struct {
	Strict      bool // update ctime
	Retries     int
	CaseInsensi bool
	ReadOnly    bool
	NoBGJob     bool // disable background jobs like clean-up, backup, etc.
	OpenCache   time.Duration
	Heartbeat   time.Duration
	MountPoint  string
	Subdir      string
}

type Format struct {
	Name             string
	UUID             string
	Storage          string
	Bucket           string
	AccessKey        string
	SecretKey        string `json:",omitempty"`
	BlockSize        int
	Compression      string
	Shards           int
	HashObjectPrefix bool
	Capacity         uint64
	Inodes           uint64
	EncryptKey       string `json:",omitempty"`
	KeyEncrypted     bool
	TrashDays        int
	MetaVersion      int
	MinClientVersion string
	MaxClientVersion string
}

func (f *Format) RemoveSecret() {
	if f.SecretKey != "" {
		f.SecretKey = "removed"
	}
	if f.EncryptKey != "" {
		f.EncryptKey = "removed"
	}
}

func (f *Format) CheckVersion() error {
	if f.MetaVersion > 1 {
		return fmt.Errorf("incompatible metadata version: %d; please upgrade the client", f.MetaVersion)
	}

	if f.MinClientVersion != "" {
		r, err := version.Compare(f.MinClientVersion)
		if err == nil && r < 0 {
			err = fmt.Errorf("allowed minimum version: %s; please upgrade the client", f.MinClientVersion)
		}
		if err != nil {
			return err
		}
	}
	if f.MaxClientVersion != "" {
		r, err := version.Compare(f.MaxClientVersion)
		if err == nil && r > 0 {
			err = fmt.Errorf("allowed maximum version: %s; please use an older client", f.MaxClientVersion)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *Format) Encrypt() error {
	if f.KeyEncrypted || f.SecretKey == "" && f.EncryptKey == "" {
		return nil
	}
	key := md5.Sum([]byte(f.UUID))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return fmt.Errorf("new cipher: %s", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("new GCM: %s", err)
	}
	nonce := make([]byte, 12)
	encrypt := func(k string) string {
		ciphertext := aesgcm.Seal(nil, nonce, []byte(k), nil)
		buf := make([]byte, 12+len(ciphertext))
		copy(buf, nonce)
		copy(buf[12:], ciphertext)
		return base64.StdEncoding.EncodeToString(buf)
	}

	if f.SecretKey != "" {
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			return fmt.Errorf("generate nonce for secret key: %s", err)
		}
		f.SecretKey = encrypt(f.SecretKey)
	}
	if f.EncryptKey != "" {
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			return fmt.Errorf("generate nonce for encrypt key: %s", err)
		}
		f.EncryptKey = encrypt(f.EncryptKey)
	}
	f.KeyEncrypted = true
	return nil
}

func (f *Format) Decrypt() error {
	if !f.KeyEncrypted || f.SecretKey == "" && f.EncryptKey == "" {
		return nil
	}
	key := md5.Sum([]byte(f.UUID))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return fmt.Errorf("new cipher: %s", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("new GCM: %s", err)
	}
	decrypt := func(k *string) error {
		buf, err := base64.StdEncoding.DecodeString(*k)
		if err != nil {
			return fmt.Errorf("decode key: %s", err)
		}
		plaintext, err := aesgcm.Open(nil, buf[:12], buf[12:], nil)
		if err != nil {
			return fmt.Errorf("open cipher: %s", err)
		}
		*k = string(plaintext)
		return nil
	}

	if f.SecretKey != "" {
		if err = decrypt(&f.SecretKey); err != nil {
			return err
		}
	}
	if f.EncryptKey != "" {
		if err = decrypt(&f.EncryptKey); err != nil {
			return err
		}
	}
	f.KeyEncrypted = false
	return nil
}
