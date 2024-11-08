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

package cmd

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
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

	"github.com/dustin/go-humanize"
	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/compress"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	osync "github.com/juicedata/juicefs/pkg/sync"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/urfave/cli/v2"
)

func cmdFormat() *cli.Command {
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
$ juicefs format sqlite3://myjfs.db myjfs --inodes 1000000 --capacity 102400

# Create a volume with "trash" disabled
$ juicefs format sqlite3://myjfs.db myjfs --trash-days 0

Details: https://juicefs.com/docs/community/quick_start_guide`,
		Flags: expandFlags(
			formatStorageFlags(),
			formatFlags(),
			formatManagementFlags(),
			[]cli.Flag{
				&cli.BoolFlag{
					Name:  "force",
					Usage: "overwrite existing format",
				},
				&cli.BoolFlag{
					Name:  "no-update",
					Usage: "don't update existing volume",
				},
			}),
	}
}

func formatStorageFlags() []cli.Flag {
	var defaultBucket = "/var/jfs"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			break
		}
		fallthrough
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
		}
		defaultBucket = path.Join(homeDir, ".juicefs", "local")
	case "windows":
		defaultBucket = path.Join("C:/jfs/local")
	}
	return addCategories("DATA STORAGE", []cli.Flag{
		&cli.StringFlag{
			Name:  "storage",
			Value: "file",
			Usage: "object storage type (e.g. s3, gs, oss, cos)",
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
			Name:  "session-token",
			Usage: "session token for object storage",
		},
		&cli.StringFlag{
			Name:  "storage-class",
			Usage: "the default storage class",
		},
	})
}

func formatFlags() []cli.Flag {
	return addCategories("DATA FORMAT", []cli.Flag{
		&cli.StringFlag{
			Name:  "block-size",
			Value: "4M",
			Usage: "size of block in KiB",
		},
		&cli.StringFlag{
			Name:  "compress",
			Value: "none",
			Usage: "compression algorithm (lz4, zstd, none)",
		},
		&cli.StringFlag{
			Name:  "encrypt-rsa-key",
			Usage: "a path to RSA private key (PEM)",
		},
		&cli.StringFlag{
			Name:  "encrypt-algo",
			Usage: "encrypt algorithm (aes256gcm-rsa, chacha20-rsa)",
			Value: object.AES256GCM_RSA,
		},
		&cli.BoolFlag{
			Name:  "hash-prefix",
			Usage: "add a hash prefix to name of objects",
		},
		&cli.IntFlag{
			Name:  "shards",
			Usage: "store the blocks into N buckets by hash of key",
		},
	})
}

func formatManagementFlags() []cli.Flag {
	return addCategories("MANAGEMENT", []cli.Flag{
		&cli.StringFlag{
			Name:  "capacity",
			Usage: "hard quota of the volume limiting its usage of space in GiB",
		},
		&cli.Uint64Flag{
			Name:  "inodes",
			Usage: "hard quota of the volume limiting its number of inodes",
		},
		&cli.IntFlag{
			Name:  "trash-days",
			Value: 1,
			Usage: "number of days after which removed files will be permanently deleted",
		},
		&cli.BoolFlag{
			Name:  "enable-acl",
			Usage: "enable POSIX ACL (this flag is irreversible once enabled)",
		},
	})
}

func fixObjectSize(s uint64) uint64 {
	const min, max = 64 << 10, 16 << 20
	var bits uint
	for s > 1 {
		bits++
		s >>= 1
	}
	s = s << bits
	if s < min {
		logger.Warnf("block size is too small: %s, use %s instead", humanize.IBytes(s), humanize.IBytes(min))
		s = min
	} else if s > max {
		logger.Warnf("block size is too large: %s, use %s instead", humanize.IBytes(s), humanize.IBytes(max))
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
	if u, err := url.Parse(format.Bucket); err == nil {
		values := u.Query()
		if values.Get("tls-insecure-skip-verify") != "" {
			var tlsSkipVerify bool
			if tlsSkipVerify, err = strconv.ParseBool(values.Get("tls-insecure-skip-verify")); err != nil {
				return nil, err
			}
			object.GetHttpClient().Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = tlsSkipVerify
			values.Del("tls-insecure-skip-verify")
			u.RawQuery = values.Encode()
			format.Bucket = u.String()
		}

		// Configure client TLS when params are provided
		if values.Get("ca-certs") != "" && values.Get("ssl-cert") != "" && values.Get("ssl-key") != "" {

			clientTLSCert, err := tls.LoadX509KeyPair(values.Get("ssl-cert"), values.Get("ssl-key"))
			if err != nil {
				return nil, fmt.Errorf("error loading certificate and key file: %s", err.Error())
			}

			certPool := x509.NewCertPool()
			caCertPEM, err := os.ReadFile(values.Get("ca-certs"))
			if err != nil {
				return nil, fmt.Errorf("error loading CA cert file: %s", err.Error())
			}

			if certAdded := certPool.AppendCertsFromPEM(caCertPEM); !certAdded {
				return nil, fmt.Errorf("error appending CA cert to pool")
			}

			object.GetHttpClient().Transport.(*http.Transport).TLSClientConfig.RootCAs = certPool
			object.GetHttpClient().Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{clientTLSCert}
		}
	}

	if format.Shards > 1 {
		blob, err = object.NewSharded(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey, format.SessionToken, format.Shards)
	} else {
		blob, err = object.CreateStorage(strings.ToLower(format.Storage), format.Bucket, format.AccessKey, format.SecretKey, format.SessionToken)
	}
	if err != nil {
		return nil, err
	}
	blob = object.WithPrefix(blob, format.Name+"/")
	if format.StorageClass != "" {
		if os, ok := blob.(object.SupportStorageClass); ok {
			err := os.SetStorageClass(format.StorageClass)
			if err != nil {
				logger.Warnf("set storage class %q: %v", format.StorageClass, err)
			}
		} else {
			logger.Warnf("Storage class is not supported by %q, will ignore", format.Storage)
		}
	}
	if format.EncryptKey != "" {
		passphrase := os.Getenv("JFS_RSA_PASSPHRASE")
		if passphrase == "" {
			block, _ := pem.Decode([]byte(format.EncryptKey))
			// nolint:staticcheck
			if block != nil && strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") && x509.IsEncryptedPEMBlock(block) {
				return nil, fmt.Errorf("passphrase is required to private key, please try again after setting the 'JFS_RSA_PASSPHRASE' environment variable")
			}
		}

		privKey, err := object.ParseRsaPrivateKeyFromPem([]byte(format.EncryptKey), []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse rsa: %s", err)
		}
		encryptor, err := object.NewDataEncryptor(object.NewRSAEncryptor(privKey), format.EncryptAlgo)
		if err != nil {
			return nil, err
		}
		blob = object.NewEncrypted(blob, encryptor)
	}
	return blob, nil
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randSeq(n int) string {
	b := make([]rune, n)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
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
		return fmt.Errorf("read wrong data: expected %x, got %x", data, data2)
	}
	err = store.Delete(key)
	if err != nil {
		// it's OK to don't have delete permission, but we should warn user explicitly
		logger.Warnf("Failed to delete, err: %s", err)
	}
	return nil
}

func test(store object.ObjectStorage) error {
	key := "testing/" + randSeq(10)
	data := make([]byte, 100)
	utils.RandRead(data)
	nRetry := 3
	var err error
	for i := 0; i < nRetry; i++ {
		err = doTesting(store, key, data)
		if err == nil {
			break
		}
		logger.Warnf("Test storage %s failed: %s, tries: #%d", store, err, i+1)
		time.Sleep(time.Second * time.Duration(i*3+1))
	}
	if err == nil {
		_ = store.Delete("testing/")
	}
	return err
}

func loadEncrypt(keyPath string) string {
	if keyPath == "" {
		return ""
	}
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		logger.Fatalf("load RSA key from %s: %s", keyPath, err)
	}
	return string(pem)
}

func format(c *cli.Context) error {
	setup(c, 2)
	removePassword(c.Args().Get(0))
	m := meta.NewClient(c.Args().Get(0), nil)
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
	if v := c.Int("shards"); v > 256 {
		logger.Fatalf("too many shards: %d", v)
	}

	var create, encrypted bool
	format, err := m.Load(false)
	if err == nil {
		if c.Bool("no-update") {
			return nil
		}
		format.Name = name
		for _, flag := range c.LocalFlagNames() {
			switch flag {
			case "capacity":
				format.Capacity = utils.ParseBytes(c, flag, 'G')
			case "inodes":
				format.Inodes = c.Uint64(flag)
			case "bucket":
				format.Bucket = c.String(flag)
			case "access-key":
				format.AccessKey = c.String(flag)
			case "secret-key":
				encrypted = format.KeyEncrypted
				if err := format.Decrypt(); err != nil && strings.Contains(err.Error(), "secret was removed") {
					logger.Warnf("decrypt secrets: %s", err)
				}
				format.SecretKey = c.String(flag)
			case "session-token":
				encrypted = format.KeyEncrypted
				if err := format.Decrypt(); err != nil && strings.Contains(err.Error(), "secret was removed") {
					logger.Warnf("decrypt secrets: %s", err)
				}
				format.SessionToken = c.String(flag)
			case "trash-days":
				format.TrashDays = c.Int(flag)
			case "block-size":
				format.BlockSize = int(fixObjectSize(utils.ParseBytes(c, flag, 'K')) >> 10)
			case "compress":
				format.Compression = c.String(flag)
			case "shards":
				format.Shards = c.Int(flag)
			case "hash-prefix":
				format.HashPrefix = c.Bool(flag)
			case "storage":
				format.Storage = c.String(flag)
			case "encrypt-rsa-key", "encrypt-algo":
				logger.Warnf("Flag %s is ignored since it cannot be updated", flag)
			}
		}
	} else if strings.HasPrefix(err.Error(), "database is not formatted") {
		create = true
		format = &meta.Format{
			Name:             name,
			UUID:             uuid.New().String(),
			Storage:          c.String("storage"),
			StorageClass:     c.String("storage-class"),
			Bucket:           c.String("bucket"),
			AccessKey:        c.String("access-key"),
			SecretKey:        c.String("secret-key"),
			SessionToken:     c.String("session-token"),
			EncryptKey:       loadEncrypt(c.String("encrypt-rsa-key")),
			EncryptAlgo:      c.String("encrypt-algo"),
			Shards:           c.Int("shards"),
			HashPrefix:       c.Bool("hash-prefix"),
			Capacity:         utils.ParseBytes(c, "capacity", 'G'),
			Inodes:           c.Uint64("inodes"),
			BlockSize:        int(fixObjectSize(utils.ParseBytes(c, "block-size", 'K')) >> 10),
			Compression:      c.String("compress"),
			TrashDays:        c.Int("trash-days"),
			DirStats:         true,
			MetaVersion:      meta.MaxVersion,
			MinClientVersion: "1.1.0-A",
			EnableACL:        c.Bool("enable-acl"),
		}
		if format.EnableACL {
			format.MinClientVersion = "1.2.0-A"
		}

		if format.AccessKey == "" && os.Getenv("ACCESS_KEY") != "" {
			format.AccessKey = os.Getenv("ACCESS_KEY")
			_ = os.Unsetenv("ACCESS_KEY")
		}
		if format.SecretKey == "" && os.Getenv("SECRET_KEY") != "" {
			format.SecretKey = os.Getenv("SECRET_KEY")
			_ = os.Unsetenv("SECRET_KEY")
		}
		if format.SessionToken == "" && os.Getenv("SESSION_TOKEN") != "" {
			format.SessionToken = os.Getenv("SESSION_TOKEN")
			_ = os.Unsetenv("SESSION_TOKEN")
		}
	} else {
		logger.Fatalf("Load metadata: %s", err)
	}
	if format.Storage == "file" || format.Storage == "sqlite3" {
		p, err := filepath.Abs(format.Bucket)
		if err == nil {
			format.Bucket = p
		} else {
			logger.Fatalf("Failed to get absolute path of %s: %s", format.Bucket, err)
		}
		if format.Storage == "file" {
			format.Bucket += "/"
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
			if objs, err := osync.ListAll(blob, "", "", "", true); err == nil {
				for o := range objs {
					if o == nil {
						logger.Warnf("List storage %s failed", blob)
						break
					} else if o.IsDir() || o.Size() == 0 {
						continue
					} else if o.Key() != "testing" && !strings.HasPrefix(o.Key(), "testing/") {
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

	if create || encrypted {
		if err = format.Encrypt(); err != nil {
			logger.Fatalf("Format encrypt: %s", err)
		}
	}
	if err = m.Init(format, c.Bool("force")); err != nil {
		if create {
			_ = blob.Delete("juicefs_uuid")
		}
		logger.Fatalf("format: %s", err)
	}
	logger.Infof("Volume is formatted as %s", format)
	return nil
}
