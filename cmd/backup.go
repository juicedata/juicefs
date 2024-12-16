/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"compress/gzip"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/zstd"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdBackup() *cli.Command {
	return &cli.Command{
		Name: "backup",
		Subcommands: []*cli.Command{
			cmdDumpV2(),
			cmdLoadV2(),
		},
		Usage:    "Backup and restore metadata",
		Category: "ADMIN",
	}
}

func cmdDumpV2() *cli.Command {
	return &cli.Command{
		Name:      "dump",
		Action:    dumpV2,
		Category:  "ADMIN",
		Usage:     "Dump metadata into a binary file",
		ArgsUsage: "META-URL FILE",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "keep-secret-key",
				Usage: "keep secret keys intact (WARNING: Be careful as they may be leaked)",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number of threads to dump metadata",
			},
		},
	}
}

func cmdLoadV2() *cli.Command {
	return &cli.Command{
		Name:      "load",
		Action:    loadV2,
		Category:  "ADMIN",
		Usage:     "Load metadata from a binary file",
		ArgsUsage: "META-URL FILE",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "encrypt-rsa-key",
				Usage: "a path to RSA private key (PEM)",
			},
			&cli.StringFlag{
				Name:  "encrypt-algo",
				Usage: "encrypt algorithm (aes256gcm-rsa, chacha20-rsa)",
				Value: object.AES256GCM_RSA,
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number of threads to load metadata",
			},
		},
	}
}

func loadV2(ctx *cli.Context) error {
	setup(ctx, 1)
	metaUri := ctx.Args().Get(0)
	src := ctx.Args().Get(1)
	removePassword(metaUri)
	var r io.ReadCloser
	if ctx.Args().Len() == 1 {
		r = os.Stdin
		src = "STDIN"
	} else {
		var ioErr error
		var fp io.ReadCloser
		if ctx.String("encrypt-rsa-key") != "" {
			passphrase := os.Getenv("JFS_RSA_PASSPHRASE")
			encryptKey := loadEncrypt(ctx.String("encrypt-rsa-key"))
			if passphrase == "" {
				block, _ := pem.Decode([]byte(encryptKey))
				// nolint:staticcheck
				if block != nil && strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") && x509.IsEncryptedPEMBlock(block) {
					return fmt.Errorf("passphrase is required to private key, please try again after setting the 'JFS_RSA_PASSPHRASE' environment variable")
				}
			}
			privKey, err := object.ParseRsaPrivateKeyFromPem([]byte(encryptKey), []byte(passphrase))
			if err != nil {
				return fmt.Errorf("parse rsa: %s", err)
			}
			encryptor, err := object.NewDataEncryptor(object.NewRSAEncryptor(privKey), ctx.String("encrypt-algo"))
			if err != nil {
				return err
			}
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("failed to stat %s: %s", src, err)
			}
			var srcAbsPath string
			srcAbsPath, err = filepath.Abs(src)
			if err != nil {
				return fmt.Errorf("failed to get absolute path of %s: %s", src, err)
			}
			fileBlob, err := object.CreateStorage("file", strings.TrimSuffix(src, filepath.Base(srcAbsPath)), "", "", "")
			if err != nil {
				return err
			}
			blob := object.NewEncrypted(fileBlob, encryptor)
			fp, ioErr = blob.Get(filepath.Base(srcAbsPath), 0, -1)
		} else {
			fp, ioErr = os.Open(src)
		}
		if ioErr != nil {
			return ioErr
		}
		defer fp.Close()
		if strings.HasSuffix(src, ".gz") {
			var err error
			r, err = gzip.NewReader(fp)
			if err != nil {
				return err
			}
			defer r.Close()
		} else if strings.HasSuffix(src, ".zstd") {
			r = zstd.NewReader(fp)
			defer r.Close()
		} else {
			r = fp
		}
	}
	m := meta.NewClient(metaUri, nil)
	if format, err := m.Load(false); err == nil {
		return fmt.Errorf("database %s is used by volume %s", utils.RemovePassword(metaUri), format.Name)
	}

	threads := ctx.Int("threads")
	if threads <= 0 {
		logger.Warnf("Invalid threads number %d, reset to 1", threads)
		threads = 1
	}
	opt := &meta.LoadOption{
		Threads: threads,
	}

	if err := m.LoadMetaV2(meta.WrapContext(ctx.Context), r, opt); err != nil {
		return err
	}
	if format, err := m.Load(true); err == nil {
		if format.SecretKey == "removed" {
			logger.Warnf("Secret key was removed; please correct it with `config` command")
		}
	} else {
		return err
	}
	logger.Infof("Load metadata from %s succeed", src)
	return nil
}

func dumpV2(ctx *cli.Context) error {
	setup(ctx, 1)
	metaUri := ctx.Args().Get(0)
	removePassword(metaUri)

	var dst string
	if ctx.Args().Len() > 1 {
		dst = ctx.Args().Get(1)
	}

	metaConf := meta.DefaultConf()
	m := meta.NewClient(metaUri, metaConf)
	if _, err := m.Load(true); err != nil {
		return err
	}

	threads := ctx.Int("threads")
	if threads <= 0 {
		logger.Warnf("Invalid threads number %d, reset to 1", threads)
		threads = 1
	}

	opt := &meta.DumpOption{
		KeepSecret: ctx.Bool("keep-secret"),
		Threads:    threads,
	}

	var w io.WriteCloser
	var err error
	if dst == "" {
		w = os.Stdout
	} else {
		tmp := dst + ".tmp"
		fp, e := os.Create(tmp)
		if e != nil {
			return e
		}
		defer func() {
			err = errors.Join(err, fp.Close())
			if err == nil {
				err = os.Rename(tmp, dst)
			} else {
				_ = os.Remove(tmp)
			}
		}()

		if strings.HasSuffix(dst, ".gz") {
			w, _ = gzip.NewWriterLevel(fp, gzip.BestSpeed)
			defer func() {
				err = errors.Join(err, w.Close())
			}()
		} else if strings.HasSuffix(dst, ".zstd") {
			w = zstd.NewWriterLevel(fp, zstd.BestSpeed)
			defer func() {
				err = errors.Join(err, w.Close())
			}()
		} else {
			w = fp
		}
	}

	if err = m.DumpMetaV2(meta.WrapContext(ctx.Context), w, opt); err != nil {
		return err
	}

	logger.Infof("Dump metadata into %s succeed", dst)
	return nil
}
