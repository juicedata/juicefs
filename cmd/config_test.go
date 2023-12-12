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
	"encoding/json"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/juicedata/juicefs/pkg/meta"
)

//mutate_test_job_number: 3
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
	_ = resetTestMeta()
	bucketPath := "/tmp/testBucket"
	_ = os.RemoveAll(bucketPath)
	if err := Main([]string{"", "format", testMeta, "--bucket", bucketPath, testVolume}); err != nil {
		t.Fatalf("format: %s", err)
	}

	if err := Main([]string{"", "config", testMeta, "--trash-days", "2"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	data, err := getStdout([]string{"", "config", testMeta})
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

	if err = Main([]string{"", "config", testMeta, "--capacity", "10", "--inodes", "1000000"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if err = Main([]string{"", "config", testMeta, "--bucket", "/tmp/newBucket", "--access-key", "testAK", "--secret-key", "testSK", "--session-token", "token"}); err != nil {
		t.Fatalf("config: %s", err)
	}
	if data, err = getStdout([]string{"", "config", testMeta}); err != nil {
		t.Fatalf("getStdout: %s", err)
	}
	if err = json.Unmarshal(data, &format); err != nil {
		t.Fatalf("json unmarshal: %s", err)
	}
	if format.Capacity != 10<<30 || format.Inodes != 1000000 ||
		format.Bucket != "/tmp/newBucket/" || format.AccessKey != "testAK" || format.SecretKey != "removed" || format.SessionToken != "removed" {
		t.Fatalf("unexpect format: %+v", format)
	}
}
