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

package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/urfave/cli/v2"
)

func cmdFormat() *cli.Command {
	var defaultBucket string
	switch runtime.GOOS {
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
		}
		defaultBucket = path.Join(homeDir, ".juicefs", "local")
	case "windows":
		defaultBucket = path.Join("C:/jfs/local")
	default:
		defaultBucket = "/var/jfs"
	}
	return &cli.Command{
		Name:      "format",
		Action:    format,
		Category:  "ADMIN",
		Usage:     "Format a volume",
		ArgsUsage: "META-URL NAME",
		Description: `
Create a new JuiceFS volume. Here META-URL is used to set up the metadata engine (Redis, TiKV, MySQL, etc.),
and NAME is the prefix of all objects in data storage.

DEPRECATED: It was also used to change configuration of an existing volume, but now this function is
deprecated, instead please use the "config" command.

Examples:
# Create a simple test volume (data will be stored in a local directory)
$ juicefs format sqlite3://myjfs.db myjfs

# Create a volume with Redis and S3
$ juicefs format redis://localhost myjfs --storage s3 --bucket https://mybucket.s3.us-east-2.amazonaws.com

# Create a volume with password protected MySQL
$ juicefs format mysql://jfs:mypassword@(127.0.0.1:3306)/juicefs myjfs
# A safer alternative
$ META_PASSWORD=mypassword juicefs format mysql://jfs:@(127.0.0.1:3306)/juicefs myjfs

# Create a volume with "quota" enabled
$ juicefs format sqlite3://myjfs.db myjfs --inode 1000000 --capacity 102400

# Create a volume with "trash" disabled
$ juicefs format sqlite3://myjfs.db myjfs --trash-days 0

Details: https://juicefs.com/docs/community/quick_start_guide`,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "block-size",
				Value: 4096,
				Usage: "size of block in KiB",
			},
			&cli.Uint64Flag{
				Name:  "capacity",
				Value: 0,
				Usage: "hard quota of the volume limiting its usage of space in GiB",
			},
			&cli.Uint64Flag{
				Name:  "inodes",
				Value: 0,
				Usage: "hard quota of the volume limiting its number of inodes",
			},
			&cli.StringFlag{
				Name:  "compress",
				Value: "none",
				Usage: "compression algorithm (lz4, zstd, none)",
			},
			&cli.IntFlag{
				Name:  "shards",
				Value: 0,
				Usage: "store the blocks into N buckets by hash of key",
			},
			&cli.StringFlag{
				Name:  "storage",
				Value: "file",
				Usage: "object storage type (e.g. s3, gcs, oss, cos)",
			},
			&cli.StringFlag{
				Name:  "bucket",
				Value: defaultBucket,
				Usage: "the bucket URL of object storage to store data",
			},
			&cli.StringFlag{
				Name:  "access-key",
				Usage: "access key for object storage (env ACCESS_KEY)",
			},
			&cli.StringFlag{
				Name:  "secret-key",
				Usage: "secret key for object storage (env SECRET_KEY)",
			},
			&cli.StringFlag{
				Name:  "encrypt-rsa-key",
				Usage: "a path to RSA private key (PEM)",
			},
			&cli.IntFlag{
				Name:  "trash-days",
				Value: 1,
				Usage: "number of days after which removed files will be permanently deleted",
			},
			&cli.BoolFlag{
				Name:  "hash-object-prefix",
				Usage: "give each object a hashed prefix",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "overwrite existing format",
			},
			&cli.BoolFlag{
				Name:  "no-update",
				Usage: "don't update existing volume",
			},
		},
	}
}

func fixObjectSize(s int) int {
	const min, max = 64, 16 << 10
	var bits uint
	for s > 1 {
		bits++
		s >>= 1
	}
	s = s << bits
	if s < min {
		s = min
	} else if s > max {
		s = max
	}
	return s
}

func createStorage(format meta.Format) (object.ObjectStorage, error) {
	if err := format.Decrypt(); err != nil {
		return nil, fmt.Errorf("format decrypt: %s", err)
	}
	object.UserAgent = "JuiceFS-" + version.Version()
	var blob object.ObjectStorage
	var err error
	var query string
	if p := strings.Index(format.Bucket, "?"); p > 0 && p+1 < len(format.Bucket) {
		query = format.Bucket[p+1:]
		format.Bucket = format.Bucket[:p]
		logger.Debugf("query string: %s", query)
	}
	if query != "" {
		values, err := url.ParseQuery(query)
		if err != nil {
			return nil, err
		}
		var tlsSkipVerify bool
		if tlsSkipVerify, err = strconv.ParseBool(values.Get("tls-insecure-skip-verify")); err != nil {
			return nil, err
		}
		object.GetHttpClient().Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: tlsSkipVerify}
	}
	if format.Shards > 1 {
		blob, err = object.NewSharded(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey, format.Shards)
	} else {
		blob, err = object.CreateStorage(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey)
	}
	if err != nil {
		return nil, err
	}
	blob = object.WithPrefix(blob, format.Name+"/")

	if format.EncryptKey != "" {
		passphrase := os.Getenv("JFS_RSA_PASSPHRASE")
		privKey, err := object.ParseRsaPrivateKeyFromPem(format.EncryptKey, passphrase)
		if err != nil {
			return nil, fmt.Errorf("load private key: %s", err)
		}
		encryptor := object.NewAESEncryptor(object.NewRSAEncryptor(privKey))
		blob = object.NewEncrypted(blob, encryptor)
	}
	return blob, nil
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func doTesting(store object.ObjectStorage, key string, data []byte) error {
	if err := store.Put(key, bytes.NewReader(data)); err != nil {
		if strings.Contains(err.Error(), "Access Denied") {
			return fmt.Errorf("Failed to put: %s", err)
		}
		if err2 := store.Create(); err2 != nil {
			if strings.Contains(err.Error(), "NoSuchBucket") {
				return fmt.Errorf("Failed to create bucket %s: %s, previous error: %s\nPlease create bucket %s manually, then format again.",
					store, err2, err, store)
			} else {
				return fmt.Errorf("Failed to create bucket %s: %s, previous error: %s",
					store, err2, err)
			}
		}
		if err := store.Put(key, bytes.NewReader(data)); err != nil {
			return fmt.Errorf("Failed to put: %s", err)
		}
	}
	p, err := store.Get(key, 0, -1)
	if err != nil {
		return fmt.Errorf("Failed to get: %s", err)
	}
	data2, err := io.ReadAll(p)
	_ = p.Close()
	if err != nil {
		return err
	}
	if !bytes.Equal(data, data2) {
		return fmt.Errorf("Read wrong data")
	}
	err = store.Delete(key)
	if err != nil {
		// it's OK to don't have delete permission
		fmt.Printf("Failed to delete: %s", err)
	}
	return nil
}

func test(store object.ObjectStorage) error {
	rand.Seed(time.Now().UnixNano())
	key := "testing/" + randSeq(10)
	data := make([]byte, 100)
	_, _ = rand.Read(data)
	nRetry := 3
	var err error
	for i := 0; i < nRetry; i++ {
		err = doTesting(store, key, data)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second * time.Duration(i*3+1))
	}
	return err
}

func format(c *cli.Context) error {
	setup(c, 2)
	removePassword(c.Args().Get(0))
	m := meta.NewClient(c.Args().Get(0), &meta.Config{Retries: 2})
	name := c.Args().Get(1)
	validName := regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,61}[a-z0-9]$`)
	if !validName.MatchString(name) {
		logger.Fatalf("invalid name: %s, only alphabet, number and - are allowed, and the length should be 3 to 63 characters.", name)
	}
	if v := c.String("compress"); compress.NewCompressor(v) == nil {
		logger.Fatalf("Unsupported compress algorithm: %s", v)
	}
	if v := c.Int("trash-days"); v < 0 {
		logger.Fatalf("Invalid trash days: %d", v)
	}

	loadEncrypt := func(keyPath string) string {
		if keyPath == "" {
			return ""
		}
		pem, err := os.ReadFile(keyPath)
		if err != nil {
			logger.Fatalf("load RSA key from %s: %s", keyPath, err)
		}
		return string(pem)
	}
	var format *meta.Format
	var create bool
	if format, _ = m.Load(false); format == nil {
		create = true
		format = &meta.Format{
			Name:             name,
			UUID:             uuid.New().String(),
			Storage:          c.String("storage"),
			Bucket:           c.String("bucket"),
			AccessKey:        c.String("access-key"),
			SecretKey:        c.String("secret-key"),
			EncryptKey:       loadEncrypt(c.String("encrypt-rsa-key")),
			Shards:           c.Int("shards"),
			HashObjectPrefix: c.Bool("hash-object-prefix"),
			Capacity:         c.Uint64("capacity") << 30,
			Inodes:           c.Uint64("inodes"),
			BlockSize:        fixObjectSize(c.Int("block-size")),
			Compression:      c.String("compress"),
			TrashDays:        c.Int("trash-days"),
			MetaVersion:      1,
		}
		if format.AccessKey == "" && os.Getenv("ACCESS_KEY") != "" {
			format.AccessKey = os.Getenv("ACCESS_KEY")
			_ = os.Unsetenv("ACCESS_KEY")
		}
		if format.SecretKey == "" && os.Getenv("SECRET_KEY") != "" {
			format.SecretKey = os.Getenv("SECRET_KEY")
			_ = os.Unsetenv("SECRET_KEY")
		}
	} else {
		if c.Bool("no-update") {
			return nil
		}
		format.Name = name
		for _, flag := range c.LocalFlagNames() {
			switch flag {
			case "capacity":
				format.Capacity = c.Uint64(flag)
			case "inodes":
				format.Capacity = c.Uint64(flag)
			case "bucket":
				format.Bucket = c.String(flag)
			case "access-key":
				format.AccessKey = c.String(flag)
			case "secret-key":
				format.SecretKey = c.String(flag)
				format.KeyEncrypted = false
			case "trash-days":
				format.TrashDays = c.Int(flag)
			case "block-size":
				format.BlockSize = fixObjectSize(c.Int(flag))
			case "compress":
				format.Compression = c.String(flag)
			case "shards":
				format.Shards = c.Int(flag)
			case "hash-object-prefix":
				format.HashObjectPrefix = c.Bool(flag)
			case "storage":
				format.Storage = c.String(flag)
			case "encrypt-rsa-key":
				logger.Warnf("Flag %s is ignored since it cannot be updated", flag)
			}
		}
	}
	if format.Storage == "file" {
		if p, err := filepath.Abs(format.Bucket); err == nil {
			format.Bucket = p + "/"
		} else {
			logger.Fatalf("Failed to get absolute path of %s: %s", format.Bucket, err)
		}
	}

	blob, err := createStorage(*format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)
	if os.Getenv("JFS_NO_CHECK_OBJECT_STORAGE") == "" {
		if err := test(blob); err != nil {
			logger.Fatalf("Storage %s is not configured correctly: %s", blob, err)
		}
		if create {
			if objs, err := osync.ListAll(blob, "", ""); err == nil {
				for o := range objs {
					if o == nil {
						logger.Warnf("List storage %s failed", blob)
						break
					} else if o.IsDir() {
						continue
					} else if !strings.HasPrefix(o.Key(), "testing/") {
						logger.Fatalf("Storage %s is not empty; please clean it up or pick another volume name", blob)
					}
				}
			} else {
				logger.Warnf("List storage %s failed: %s", blob, err)
			}
			if err = blob.Put("juicefs_uuid", strings.NewReader(format.UUID)); err != nil {
				logger.Warnf("Put uuid object: %s", err)
			}
		}
	}

	if err = format.Encrypt(); err != nil {
		logger.Fatalf("Format encrypt: %s", err)
	}
	if err = m.Init(*format, c.Bool("force")); err != nil {
		logger.Fatalf("format: %s", err)
	}
	format.RemoveSecret()
	logger.Infof("Volume is formatted as %+v", *format)
	return nil
}
