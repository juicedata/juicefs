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
	"context"
	"os"
	"testing"

	"github.com/go-redis/redis/v8"
)

func TestDumpAndLoad(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	t.Run("Test Load", func(t *testing.T) {
		loadArgs := []string{"", "load", metaUrl, "./../pkg/meta/metadata.sample"}
		opt, err := redis.ParseURL(metaUrl)
		if err != nil {
			t.Fatalf("ParseURL: %v", err)
		}
		rdb := redis.NewClient(opt)
		rdb.FlushDB(context.Background())

		Main(loadArgs)
		if rdb.DBSize(context.Background()).Val() == 0 {
			t.Fatalf("load error: %v", err)
		}

	})
	t.Run("Test dump", func(t *testing.T) {
		dumpArgs := []string{"", "dump", metaUrl, "/tmp/dump_test.json"}

		Main(dumpArgs)
		_, err := os.Stat("/tmp/dump_test.json")
		if err != nil {
			t.Fatalf("dump error: %v", err)
		}
	})

}
