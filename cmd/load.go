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

func load(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("META-URL is needed")
	}
	var fp io.ReadCloser
	if ctx.Args().Len() == 1 {
		fp = os.Stdin
	} else {
		var err error
		fp, err = os.Open(ctx.Args().Get(1))
		if err != nil {
			return err
		}
		defer fp.Close()
	}
	m := meta.NewClient(ctx.Args().Get(0), &meta.Config{Retries: 10, Strict: true})
	if err := m.LoadMeta(fp); err != nil {
		return err
	}
	logger.Infof("Load metadata from %s succeed", ctx.Args().Get(1))
	return nil
}

func loadFlags() *cli.Command {
	return &cli.Command{
		Name:      "load",
		Usage:     "load metadata from a previously dumped JSON file",
		ArgsUsage: "META-URL [FILE]",
		Action:    load,
	}
}
