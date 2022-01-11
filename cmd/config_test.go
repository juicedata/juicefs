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
	"encoding/json"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/juicedata/juicefs/pkg/meta"
)

func getStdout(args []string) ([]byte, error) {
	tmp, err := os.CreateTemp("/tmp", "jfstest-*")
	if err != nil {
		return nil, err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	patch := gomonkey.ApplyGlobalVar(os.Stdout, *tmp)
	defer patch.Reset()

	if err = Main(args); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp.Name())
}

func TestConfig(t *testing.T) {
	metaUrl := "redis://localhost:6379/10"
	ResetRedis(metaUrl)
	if err := Main([]string{"", "format", metaUrl, "--bucket", "/tmp/testBucket", "test"}); err != nil {
		t.Fatalf("format: %s", err)
	}

	if err := Main([]string{"", "config", metaUrl, "--trash-days", "2"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	data, err := getStdout([]string{"", "config", metaUrl})
	if err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	var format meta.Format
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.TrashDays != 2 {
		t.Fatalf("trash-days %d != expect 2", format.TrashDays)
	}

	if err = Main([]string{"", "config", metaUrl, "--capacity", "10", "--inodes", "1000000"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if err = Main([]string{"", "config", metaUrl, "--bucket", "/tmp/newBucket", "--access-key", "testAK", "--secret-key", "testSK"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if data, err = getStdout([]string{"", "config", metaUrl}); err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.Capacity != 10<<30 || format.Inodes != 1000000 ||
		format.Bucket != "/tmp/newBucket" || format.AccessKey != "testAK" || format.SecretKey != "removed" {
		t.Fatalf("unexpect format: %+v", format)
	}
}
