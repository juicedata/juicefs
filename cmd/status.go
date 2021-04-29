/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/urfave/cli/v2"
)

func status(ctx *cli.Context) error {
	setLoggerLevel(ctx)
	if ctx.Args().Len() < 1 {
		return fmt.Errorf("REDIS-URL is needed")
	}
	addr := ctx.Args().Get(0)
	if !strings.Contains(addr, "://") {
		addr = "redis://" + addr
	}

	logger.Infof("Meta address: %s", addr)
	var rc = meta.RedisConfig{Retries: 10, Strict: true}
	m, err := meta.NewRedisMeta(addr, &rc)
	if err != nil {
		logger.Fatalf("Meta: %s", err)
	}
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}
	format.SecretKey = ""
	format.EncryptKey = ""
	data, err := json.MarshalIndent(format, "", "  ")
	if err != nil {
		logger.Fatalf("json: %s", err)
	}
	fmt.Println(string(data))
	return nil
}

func statusFlags() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "show status of JuiceFS",
		ArgsUsage: "REDIS-URL",
		Action:    status,
	}
}
