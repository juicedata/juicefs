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

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type unsupportedTierStorage struct {
	object.ObjectStorage
	initCalls *int
}

func (s *unsupportedTierStorage) InitTiers(_ object.Tiers) error {
	*s.initCalls = *s.initCalls + 1
	return errors.New("not supported")
}

func (s *unsupportedTierStorage) GetTier(context.Context) object.Tier {
	return object.Tier{}
}

func TestFixObjectSize(t *testing.T) {
	t.Run("Should make sure the size is in range", func(t *testing.T) {
		cases := []struct {
			input, expected uint64
		}{
			{30 << 10, 64 << 10},
			{0, 64 << 10},
			{2 << 40, 16 << 20},
			{16 << 21, 16 << 20},
		}
		for _, c := range cases {
			if size := fixObjectSize(c.input); size != c.expected {
				t.Fatalf("Expected %d, got %d", c.expected, size)
			}
		}
	})
	t.Run("Should use powers of two", func(t *testing.T) {
		cases := []struct {
			input, expected uint64
		}{
			{150 << 10, 128 << 10},
			{99 << 10, 64 << 10},
			{1077 << 10, 1024 << 10},
		}
		for _, c := range cases {
			if size := fixObjectSize(c.input); size != c.expected {
				t.Fatalf("Expected %d, got %d", c.expected, size)
			}
		}
	})
}

func TestCreateStorageTierWarning(t *testing.T) {
	const storageName = "unsupported-tier-test"
	initCalls := 0
	object.Register(storageName, func(bucket, accessKey, secretKey, token string) (object.ObjectStorage, error) {
		storage, err := object.CreateStorage("mem", bucket, accessKey, secretKey, token)
		if err != nil {
			return nil, err
		}
		return &unsupportedTierStorage{ObjectStorage: storage, initCalls: &initCalls}, nil
	})

	var logs bytes.Buffer
	originalOutput := logger.Out
	logger.SetOutput(&logs)
	defer logger.SetOutput(originalOutput)

	format := meta.Format{Name: "test", Storage: storageName, Tiers: object.NewTiers("")}
	if _, err := createStorage(format); err != nil {
		t.Fatalf("create storage without configured tiers: %s", err)
	}
	if initCalls != 1 {
		t.Fatalf("InitTiers called %d times, expected 1", initCalls)
	}
	if strings.Contains(logs.String(), "Set storage tier:") {
		t.Fatalf("unexpected storage tier warning: %s", logs.String())
	}

	logs.Reset()
	format.Tiers[0] = object.Tier{ID: 0, Sc: "STANDARD_IA"}
	if _, err := createStorage(format); err != nil {
		t.Fatalf("create storage with a configured tier: %s", err)
	}
	if initCalls != 2 {
		t.Fatalf("InitTiers called %d times, expected 2", initCalls)
	}
	if !strings.Contains(logs.String(), "Set storage tier: not supported") {
		t.Fatalf("missing storage tier warning: %s", logs.String())
	}
}

func TestFormat(t *testing.T) {
	rdb := resetTestMeta()
	if err := Main([]string{"", "format", "--bucket", t.TempDir(), testMeta, testVolume}); err != nil {
		t.Fatalf("format error: %s", err)
	}
	body, err := rdb.Get(context.Background(), "setting").Bytes()
	if err != nil {
		t.Fatalf("get setting: %s", err)
	}
	f := meta.Format{}
	if err = json.Unmarshal(body, &f); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if f.Name != testVolume {
		t.Fatalf("volume name %s != expected %s", f.Name, testVolume)
	}

	if err = Main([]string{"", "format", testMeta, testVolume, "--capacity", "1", "--inodes", "1000"}); err != nil {
		t.Fatalf("format error: %s", err)
	}
	if body, err = rdb.Get(context.Background(), "setting").Bytes(); err != nil {
		t.Fatalf("get setting: %s", err)
	}
	if err = json.Unmarshal(body, &f); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if f.Capacity != 1<<30 || f.Inodes != 1000 {
		t.Fatalf("unexpected volume: %+v", f)
	}
}
