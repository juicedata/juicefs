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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DataDog/zstd"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/olekukonko/tablewriter"

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
			&cli.BoolFlag{
				Name:  "binary",
				Usage: "load metadata from a binary file (different from original JSON format)",
			},
			&cli.BoolFlag{
				Name:  "stat",
				Usage: "show statistics of the metadata binary file",
			},
			&cli.IntFlag{
				Name:  "threads",
				Value: 10,
				Usage: "number of threads to load binary metadata, only works with --binary",
			},
		},
		Usage:     "Load metadata from a previously dumped file",
		ArgsUsage: "META-URL [FILE]",
		Description: `
Load metadata into an empty metadata engine or show statistics of the backup file.

WARNING: Do NOT use new engine and the old one at the same time, otherwise it will probably break
consistency of the volume.

Examples:
$ juicefs load redis://localhost/1 meta-dump.json.gz
$ juicefs load redis://localhost/1 meta-dump.bin --binary --threads 10
$ juicefs load meta-dump.bin --binary --stat

Details: https://juicefs.com/docs/community/metadata_dump_load`,
	}
}

func load(ctx *cli.Context) error {
	setup(ctx, 1)

	if ctx.Bool("binary") && ctx.Bool("stat") {
		return statBak(ctx)
	}

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

	if ctx.Bool("binary") {
		progress := utils.NewProgress(false)
		bars := make(map[string]*utils.Bar)
		for _, name := range meta.SegType2Name {
			bars[name] = progress.AddCountSpinner(name)
		}

		opt := &meta.LoadOption{
			Threads: ctx.Int("threads"),
			Progress: func(name string, cnt int) {
				bars[name].IncrBy(cnt)
			},
		}
		if err := m.LoadMetaV2(meta.WrapContext(ctx.Context), r, opt); err != nil {
			return err
		}
		progress.Done()
	} else {
		if err := m.LoadMeta(r); err != nil {
			return err
		}
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

func statBak(ctx *cli.Context) error {
	path := ctx.Args().Get(0)
	if path == "" {
		return errors.New("missing file path")
	}

	fp, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	bak := &meta.BakFormat{}
	footer, err := bak.ReadFooter(fp)
	if err != nil {
		return fmt.Errorf("failed to read footer: %w", err)
	}

	fmt.Printf("Backup Version: %d\n", footer.Msg.Version)
	data := make([][]string, 0, len(footer.Msg.Infos))
	for name, info := range footer.Msg.Infos {
		data = append(data, []string{name, fmt.Sprintf("%d", info.Num)})
	}
	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name", "Num"})
	for _, v := range data {
		table.Append(v)
	}
	table.Render()
	return nil
}
