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

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func dump(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	var fp io.WriteCloser
	if ctx.Args().Len() == 1 {
		fp = os.Stdout
	} else {
		var err error
		fp, err = os.OpenFile(ctx.Args().Get(1), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		defer fp.Close()
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true, Subdir: ctx.String("subdir")})
	if err := m.DumpMeta(fp, 0); err != nil {
		return err
	}
	logger.Infof("Dump metadata into %s succeed", ctx.Args().Get(1))
	return nil
}

func dumpFlags() *cli.Command {
	return &cli.Command{
		Name:      "dump",
		Usage:     "dump metadata into a JSON file",
		ArgsUsage: "META-URL [FILE]",
		Action:    dump,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "subdir",
				Usage: "only dump a sub-directory.",
			},
		},
	}
}
