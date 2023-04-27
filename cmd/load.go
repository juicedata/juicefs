/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/juicedata/juicefs/pkg/object"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdLoad() *cli.Command {
	return &cli.Command{
		Name:     "load",
		Action:   load,
		Category: "ADMIN",
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
		},
		Usage:     "Load metadata from a previously dumped JSON file",
		ArgsUsage: "META-URL [FILE]",
		Description: `
Load metadata into an empty metadata engine.

WARNING: Do NOT use new engine and the old one at the same time, otherwise it will probably break
consistency of the volume.

Examples:
$ juicefs load redis://localhost/1 meta-dump.json.gz

Details: https://juicefs.com/docs/community/metadata_dump_load`,
	}
}

func load(ctx *cli.Context) error {
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
			fileBlob, err := object.CreateStorage("file", strings.TrimRight(src, filepath.Base(srcAbsPath)), "", "", "")
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
		} else {
			r = fp
		}
	}
	m := meta.NewClient(metaUri, nil)
	if format, err := m.Load(false); err == nil {
		return fmt.Errorf("Database %s is used by volume %s", utils.RemovePassword(metaUri), format.Name)
	}
	if err := m.LoadMeta(r); err != nil {
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
