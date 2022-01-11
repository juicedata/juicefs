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
