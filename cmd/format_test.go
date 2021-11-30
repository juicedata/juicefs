/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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
	"encoding/json"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"

	"github.com/go-redis/redis/v8"
)

func TestFixObjectSize(t *testing.T) {
	t.Run("Should make sure the size is in range", func(t *testing.T) {
		cases := []struct {
			input, expected int
		}{
			{30, 64},
			{0, 64},
			{2 << 30, 16 << 10},
			{16 << 11, 16 << 10},
		}
		for _, c := range cases {
			if size := fixObjectSize(c.input); size != c.expected {
				t.Fatalf("Expected %d, got %d", c.expected, size)
			}
		}
	})
	t.Run("Should use powers of two", func(t *testing.T) {
		cases := []struct {
			input, expected int
		}{
			{150, 128},
			{99, 64},
			{1077, 1024},
		}
		for _, c := range cases {
			if size := fixObjectSize(c.input); size != c.expected {
				t.Fatalf("Expected %d, got %d", c.expected, size)
			}
		}
	})
}

func TestFormat(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	opt, err := redis.ParseURL(metaUrl)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	rdb := redis.NewClient(opt)
	ctx := context.Background()
	rdb.FlushDB(ctx)
	defer rdb.FlushDB(ctx)
	name := "test"
	formatArgs := []string{"", "format", "--storage", "file", "--bucket", "/tmp/testMountDir", metaUrl, name}
	err = Main(formatArgs)
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	body, err := rdb.Get(ctx, "setting").Bytes()
	if err == redis.Nil {
		t.Fatalf("database is not formatted")
	}
	if err != nil {
		t.Fatalf("database is not formatted")
	}
	f := meta.Format{}
	err = json.Unmarshal(body, &f)
	if err != nil {
		t.Fatalf("database formatted error: %v", err)
	}

	if f.Name != name {
		t.Fatalf("database formatted error: %v", err)
	}

}
