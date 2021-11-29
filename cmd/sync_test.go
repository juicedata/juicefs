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
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
)

func TestSync(t *testing.T) {

	minioDir := "synctest"
	localDir := "/tmp/synctest"
	defer os.RemoveAll(localDir)
	storage, err := object.CreateStorage("minio", os.Getenv("MINIO_TEST_BUCKET"), os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"))
	if err != nil {
		t.Fatalf("create storage failed: %v", err)
	}

	testInstances := []struct{ path, content string }{
		{"t1.txt", "content1"},
		{"testDir1/t2.txt", "content2"},
		{"testDir1/testDir3/t3.txt", "content3"},
	}

	for _, instance := range testInstances {
		err = storage.Put(fmt.Sprintf("/%s/%s", minioDir, instance.path), bytes.NewReader([]byte(instance.content)))
		if err != nil {
			t.Fatalf("storage put failed: %v", err)
		}
	}
	syncArgs := []string{"", "sync", fmt.Sprintf("minio://%s/%s", os.Getenv("MINIO_TEST_BUCKET"), minioDir), fmt.Sprintf("file://%s", localDir)}
	err = Main(syncArgs)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	for _, instance := range testInstances {
		c, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", localDir, instance.path))
		if err != nil || string(c) != instance.content {
			t.Fatalf("sync failed: %v", err)
		}
	}

}
