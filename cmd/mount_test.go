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
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/go-redis/redis/v8"
)

func ResetPrometheus() {
	http.DefaultServeMux = http.NewServeMux()
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
}

func MountTmp(metaUrl, mountpoint string) error {
	formatArgs := []string{"", "format", "--storage", "file", "--bucket", "/tmp/testMountDir", metaUrl, "test"}
	err := Main(formatArgs)
	if err != nil {
		return err
	}
	mountArgs := []string{"", "mount", metaUrl, mountpoint}

	//Must be reset, otherwise panic will appear
	ResetPrometheus()

	go func() {
		err := Main(mountArgs)
		if err != nil {
			fmt.Printf("mount failed: %v", err)
		}
	}()
	time.Sleep(2 * time.Second)
	return nil
}
func ResetRedis(metaUrl string) {
	opt, _ := redis.ParseURL(metaUrl)
	rdb := redis.NewClient(opt)
	rdb.FlushDB(context.Background())
}

func TestMount(t *testing.T) {
	metaUrl := "redis://127.0.0.1:6379/10"
	mountpoint := "/tmp/testDir"
	defer ResetRedis(metaUrl)
	if err := MountTmp(metaUrl, mountpoint); err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	defer func(mountpoint string) {
		err := UmountTmp(mountpoint)
		if err != nil {
			t.Fatalf("umount failed: %v", err)
		}
	}(mountpoint)

	err := ioutil.WriteFile(fmt.Sprintf("%s/f1.txt", mountpoint), []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Test mount failed: %v", err)
	}

}
