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
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/sm4"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/pkg/errors"
)

// Config for clients.
type Config struct {
	Retries            int
	MaxDeletes         int
	SkipDirNlink       int
	CaseInsensi        bool
	ReadOnly           bool
	NoBGJob            bool // disable background jobs like clean-up, backup, etc.
	OpenCache          time.Duration
	OpenCacheLimit     uint64 // max number of files to cache (soft limit)
	Heartbeat          time.Duration
	MountPoint         string
	Subdir             string
	AtimeMode          string
	DirStatFlushPeriod time.Duration
	SkipDirMtime       time.Duration
	Sid                uint64
	SortDir            bool
	FastStatfs         bool
	NetworkInterfaces  []string // list of network interfaces to use for IP discovery (empty means all)
}

func DefaultConf() *Config {
	return &Config{Retries: 10, MaxDeletes: 2, Heartbeat: 12 * time.Second, AtimeMode: NoAtime, DirStatFlushPeriod: 1 * time.Second}
}

func (c *Config) SelfCheck() {
	if c.MaxDeletes == 0 {
		logger.Warnf("Deleting object will be disabled since max-deletes is 0")
	}
	if c.Heartbeat != 0 && c.Heartbeat < time.Second {
		logger.Warnf("heartbeat should not be less than 1 second")
		c.Heartbeat = time.Second
	}
	if c.Heartbeat > time.Minute*10 {
		logger.Warnf("heartbeat should not be greater than 10 minutes")
		c.Heartbeat = time.Minute * 10
	}
}

type Format struct {
	Name             string
	UUID             string
	Storage          string
	StorageClass     string `json:",omitempty"`
	Bucket           string
	AccessKey        string `json:",omitempty"`
	SecretKey        string `json:",omitempty"`
	SessionToken     string `json:",omitempty"`
	BlockSize        int
	Compression      string `json:",omitempty"`
	Shards           int    `json:",omitempty"`
	HashPrefix       bool   `json:",omitempty"`
	Capacity         uint64 `json:",omitempty"`
	Inodes           uint64 `json:",omitempty"`
	EncryptKey       string `json:",omitempty"`
	EncryptAlgo      string `json:",omitempty"`
	KeyEncrypted     bool   `json:",omitempty"`
	UploadLimit      int64  `json:",omitempty"` // Mbps
	DownloadLimit    int64  `json:",omitempty"` // Mbps
	TrashDays        int
	MetaVersion      int    `json:",omitempty"`
	MinClientVersion string `json:",omitempty"`
	MaxClientVersion string `json:",omitempty"`
	DirStats         bool   `json:",omitempty"`
	UserGroupQuota   bool   `json:",omitempty"`
	EnableACL        bool
	RangerRestUrl    string `json:",omitempty"`
	RangerService    string `json:",omitempty"`
}

func (f *Format) update(old *Format, force bool) error {
	if force {
		logger.Warnf("Existing volume will be overwrited: %s", old)
	} else {
		var args []interface{}
		switch {
		case f.Name != old.Name:
			args = []interface{}{"name", old.Name, f.Name}
		case f.BlockSize != old.BlockSize:
			args = []interface{}{"block size", old.BlockSize, f.BlockSize}
		case f.Compression != old.Compression:
			args = []interface{}{"compression", old.Compression, f.Compression}
		case f.Shards != old.Shards:
			args = []interface{}{"shards", old.Shards, f.Shards}
		case f.HashPrefix != old.HashPrefix:
			args = []interface{}{"hash prefix", old.HashPrefix, f.HashPrefix}
		case f.MetaVersion != old.MetaVersion:
			args = []interface{}{"meta version", old.MetaVersion, f.MetaVersion}
		}
		if args == nil {
			if f.UUID != old.UUID {
				if err := f.Decrypt(); err != nil {
					return fmt.Errorf("decrypt format: %s", err)
				}
				f.UUID = old.UUID // UUID cannot be changed alone
				if err := f.Encrypt(); err != nil {
					return fmt.Errorf("encrypt format: %s", err)
				}
			}
		} else {
			return fmt.Errorf("cannot update volume %s from %v to %v", args...)
		}
	}
	return nil
}

func (f *Format) RemoveSecret() {
	if f.SecretKey != "" {
		f.SecretKey = "removed"
	}
	if f.SessionToken != "" {
		f.SessionToken = "removed"
	}
	if f.EncryptKey != "" {
		f.EncryptKey = "removed"
	}
}

func (f *Format) String() string {
	t := *f
	t.RemoveSecret()
	s, _ := json.MarshalIndent(t, "", "  ")
	return string(s)
}

func (f *Format) CheckVersion() error {
	if f.MetaVersion > MaxVersion {
		return fmt.Errorf("incompatible metadata version: %d; please upgrade the client", f.MetaVersion)
	}

	ver := version.GetVersion()
	return f.CheckCliVersion(&ver)
}

func (f *Format) CheckCliVersion(ver *version.Semver) error {
	if ver == nil {
		return errors.New("version is nil")
	}

	if f.MinClientVersion != "" {
		minClientVer := version.Parse(f.MinClientVersion)
		r, err := version.CompareVersions(ver, minClientVer)
		if err == nil && r < 0 {
			err = fmt.Errorf("allowed minimum version: %s; please upgrade the client", f.MinClientVersion)
		}
		if err != nil {
			return err
		}
	}
	if f.MaxClientVersion != "" {
		maxClientVer := version.Parse(f.MaxClientVersion)
		r, err := version.CompareVersions(ver, maxClientVer)
		if err == nil && r > 0 {
			err = fmt.Errorf("allowed maximum version: %s; please use an older client", f.MaxClientVersion)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func newCipher(algo string, key string) (cipher.AEAD, error) {
	switch algo {
	case object.SM4GCM:
		block, err := sm4.NewCipher(sm3.Kdf([]byte(key), 16))
		if err != nil {
			return nil, fmt.Errorf("new sm4 cipher: %s", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("new sm4 GCM: %s", err)
		}
		return aead, nil
	default:
		hashKey := md5.Sum([]byte(key))
		block, err := aes.NewCipher(hashKey[:])
		if err != nil {
			return nil, fmt.Errorf("new cipher: %s", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("new GCM: %s", err)
		}
		return aead, nil
	}
}

func (f *Format) Encrypt() error {
	if f.KeyEncrypted || f.SecretKey == "" && f.EncryptKey == "" && f.SessionToken == "" {
		return nil
	}
	ci, err := newCipher(f.EncryptAlgo, f.UUID)
	if err != nil {
		return err
	}
	encrypt := func(k *string) {
		if *k == "" {
			return
		}
		nonce := make([]byte, ci.NonceSize())
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			logger.Fatalf("generate nonce for secret key: %s", err)
		}
		ciphertext := ci.Seal(nil, nonce, []byte(*k), nil)
		buf := make([]byte, ci.NonceSize()+len(ciphertext))
		copy(buf, nonce)
		copy(buf[ci.NonceSize():], ciphertext)
		*k = base64.StdEncoding.EncodeToString(buf)
	}

	encrypt(&f.SecretKey)
	encrypt(&f.SessionToken)
	encrypt(&f.EncryptKey)
	f.KeyEncrypted = true
	return nil
}

func (f *Format) Decrypt() error {
	if !f.KeyEncrypted {
		return nil
	}

	ci, err := newCipher(f.EncryptAlgo, f.UUID)
	if err != nil {
		return err
	}
	decrypt := func(k *string) {
		if *k == "" {
			return
		}
		if *k == "removed" {
			err = fmt.Errorf("secret was removed; please correct it with `config` command")
			return
		}
		buf, e := base64.StdEncoding.DecodeString(*k)
		if e != nil {
			err = fmt.Errorf("decode key: %s", e)
			return
		}
		plaintext, e := ci.Open(nil, buf[:ci.NonceSize()], buf[ci.NonceSize():], nil)
		if e != nil {
			err = fmt.Errorf("open cipher: %s", e)
			return
		}
		*k = string(plaintext)
	}

	decrypt(&f.EncryptKey)
	decrypt(&f.SecretKey)
	decrypt(&f.SessionToken)
	f.KeyEncrypted = false
	return err
}
