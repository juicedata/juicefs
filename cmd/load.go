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
			&cli.Int64Flag{
				Name:  "offset",
				Usage: "offset of binary backup's segment (works with --stat and --binary). Use -1 to show all offsets, or specify one for details",
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
	defer fp.Close()

	if !ctx.IsSet("offset") {
		return showBakSummary(ctx, fp, false)
	}

	offset := ctx.Int64("offset")
	if offset == -1 {
		return showBakSummary(ctx, fp, true)
	}

	return showBakDetail(ctx, fp, offset)
}

func showBakSummary(ctx *cli.Context, fp *os.File, withOffset bool) error {
	bak := &meta.BakFormat{}
	footer, err := bak.ReadFooter(fp)
	if err != nil {
		return fmt.Errorf("failed to read footer: %w", err)
	}

	fmt.Printf("Backup Version: %d\n", footer.Msg.Version)
	data := make([][]string, 0, len(footer.Msg.Infos))
	for name, info := range footer.Msg.Infos {
		if withOffset {
			data = append(data, []string{name, fmt.Sprintf("%d", info.Num), fmt.Sprintf("%d", info.Offset)})
		} else {
			data = append(data, []string{name, fmt.Sprintf("%d", info.Num)})
		}
	}
	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	if withOffset {
		fmt.Println(strings.Repeat("-", 34))
		fmt.Printf("%-10s| %-10s| %-10s\n", "Name", "Num", "Offset")
		fmt.Println(strings.Repeat("-", 34))
	} else {
		fmt.Println(strings.Repeat("-", 23))
		fmt.Printf("%-10s| %-10s\n", "Name", "Num")
		fmt.Println(strings.Repeat("-", 23))
	}
	for _, v := range data {
		fmt.Printf("%-10s| %-10s|", v[0], v[1])
		if withOffset {
			fmt.Printf(" %-10s", v[2])
		}
		fmt.Println()
	}
	return nil
}

func showBakDetail(ctx *cli.Context, fp *os.File, offset int64) error {
	bak := &meta.BakFormat{}
	if _, err := fp.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	seg, err := bak.ReadSegment(fp)
	if err != nil {
		return fmt.Errorf("failed to read segment: %w", err)
	}

	fmt.Printf("Segment: %s\n", seg.Name())
	fmt.Printf("Value: %s\n", seg)
	return nil
}
