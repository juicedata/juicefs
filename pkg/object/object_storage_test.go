/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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

package object

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/colinmarc/hdfs/v2/hadoopconf"
	"github.com/juicedata/juicefs/pkg/utils"

	blob2 "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"

	"github.com/redis/go-redis/v9"
)

func get(s ObjectStorage, k string, off, limit int64, getters ...AttrGetter) (string, error) {
	r, err := s.Get(k, off, limit, getters...)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func listAll(s ObjectStorage, prefix, marker string, limit int64, followLink bool) ([]Object, error) {
	ch, err := ListAll(s, prefix, marker, followLink)
	if err == nil {
		objs := make([]Object, 0)
		for obj := range ch {
			if len(objs) < int(limit) {
				objs = append(objs, obj)
			}
		}
		return objs, nil
	}
	return nil, err
}

func setStorageClass(o ObjectStorage) string {
	if osc, ok := o.(SupportStorageClass); ok {
		var sc = "STANDARD_IA"
		switch o.(type) {
		case *wasb:
			sc = string(blob2.AccessTierCool)
		case *gs:
			sc = "NEARLINE"
		case *ossClient:
			sc = string(oss.StorageIA)
		case *tosClient:
			sc = string(enum.StorageClassIa)
		case *obsClient:
			sc = string(obs.StorageClassStandard)
		case *bosclient:
			sc = api.STORAGE_CLASS_STANDARD
		case *minio:
			sc = "REDUCED_REDUNDANCY"
		case *scw:
			sc = "ONEZONE_IA" // STANDARD, ONEZONE_IA, GLACIER
		}
		err := osc.SetStorageClass(sc)
		if err != nil {
			sc = ""
		}
		return sc
	}
	return ""
}

// nolint:errcheck
func testStorage(t *testing.T, s ObjectStorage) {
	sc := setStorageClass(s)
	if err := s.Create(); err != nil {
		t.Fatalf("Can't create bucket %s: %s", s, err)
	}
	if err := s.Create(); err != nil {
		t.Fatalf("err should be nil when creating a bucket with the same name")
	}
	prefix := "unit-test/"
	s = WithPrefix(s, prefix)
	defer func() {
		if err := s.Delete("test"); err != nil {
			t.Fatalf("delete failed: %s", err)
		}
	}()

	var scPut string
	key := "测试编码文件" + `{"name":"juicefs"}` + string('\u001F') + "%uFF081%uFF09.jpg"
	if err := s.Put(key, bytes.NewReader(nil), WithStorageClass(&scPut)); err != nil {
		t.Logf("PUT testEncodeFile failed: %s", err.Error())
	} else {
		if scPut != sc {
			t.Fatalf("Storage class should be %q, got %q", sc, scPut)
		}
		if resp, _, _, err := s.List("测试编码文件", "", "", "", 1, true); err != nil && err != notSupported {
			t.Logf("List testEncodeFile Failed: %s", err)
		} else if len(resp) == 1 && resp[0].Key() != key {
			t.Logf("List testEncodeFile Failed: expect key %s, but got %s", key, resp[0].Key())
		}
	}
	_ = s.Delete(key)

	_, err := s.Get("not_exists", 0, -1)
	if err == nil {
		t.Fatalf("Get should failed: %s", err)
	}

	br := []byte("hello")
	if err := s.Put("test", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}

	var scGet string
	// get all
	if d, e := get(s, "test", 0, -1, WithStorageClass(&scGet)); e != nil || d != "hello" {
		t.Fatalf("expect hello, but got %v, error: %s", d, e)
	}
	if scGet != sc { // Relax me when testing against a storage that doesn't use specified storage class
		t.Fatalf("Storage class should be %q, got %q", sc, scGet)
	}

	if d, e := get(s, "test", 0, 5); e != nil || d != "hello" {
		t.Fatalf("expect hello, but got %v, error: %s", d, e)
	}
	// get first
	if d, e := get(s, "test", 0, 1); e != nil || d != "h" {
		t.Fatalf("expect h, but got %v, error: %s", d, e)
	}
	// get last
	if d, e := get(s, "test", 4, 1); e != nil || d != "o" {
		t.Fatalf("expect o, but got %v, error: %s", d, e)
	}
	// get last 3
	if d, e := get(s, "test", 2, 3); e != nil || d != "llo" {
		t.Fatalf("expect llo, but got %v, error: %s", d, e)
	}
	// get middle
	if d, e := get(s, "test", 2, 2); e != nil || d != "ll" {
		t.Fatalf("expect ll, but got %v, error: %s", d, e)
	}
	// get the end out of range
	if d, e := get(s, "test", 4, 2); e != nil || d != "o" {
		t.Logf("out-of-range get: 'o', but got %v, error: %s", len(d), e)
	}
	// get the off out of range
	if d, e := get(s, "test", 6, 2); e != nil || d != "" {
		t.Logf("out-of-range get: '', but got %v, error: %s", len(d), e)
	}
	switch s.(*withPrefix).os.(type) {
	case FileSystem:
		objs, err2 := listAll(s, "", "", 2, true)
		if err2 == nil {
			if len(objs) != 2 {
				t.Fatalf("List should return 2 keys, but got %d", len(objs))
			}
			if objs[0].Key() != "" {
				t.Fatalf("First key should be empty string, but got %s", objs[0].Key())
			}
			if objs[0].Size() != 0 {
				t.Fatalf("First object size should be 0, but got %d", objs[0].Size())
			}
			if objs[1].Key() != "test" {
				t.Fatalf("Second key should be test, but got %s", objs[1].Key())
			}
			if !strings.Contains(s.String(), "encrypted") && objs[1].Size() != 5 {
				t.Fatalf("Size of first key shold be 5, but got %v", objs[1].Size())
			}
			now := time.Now()
			if objs[1].Mtime().Before(now.Add(-30*time.Second)) || objs[1].Mtime().After(now.Add(time.Second*30)) {
				t.Fatalf("Mtime of key should be within 10 seconds, but got %s", objs[1].Mtime().Sub(now))
			}
		} else {
			t.Fatalf("list failed: %s", err2.Error())
		}

		objs, err2 = listAll(s, "", "test2", 1, true)
		if err2 != nil {
			t.Fatalf("list3 failed: %s", err2.Error())
		} else if len(objs) != 0 {
			t.Fatalf("list3 should not return anything, but got %d", len(objs))
		}
	default:
		objs, err2 := listAll(s, "", "", 1, true)
		if err2 == nil {
			if len(objs) != 1 {
				t.Fatalf("List should return 1 keys, but got %d", len(objs))
			}
			if objs[0].Key() != "test" {
				t.Fatalf("First key should be test, but got %s", objs[0].Key())
			}
			if !strings.Contains(s.String(), "encrypted") && objs[0].Size() != 5 {
				t.Fatalf("Size of first key shold be 5, but got %v", objs[0].Size())
			}
			now := time.Now()
			if objs[0].Mtime().Before(now.Add(-30*time.Second)) || objs[0].Mtime().After(now.Add(time.Second*30)) {
				t.Fatalf("Mtime of key should be within 10 seconds, but got %s", objs[0].Mtime().Sub(now))
			}
		} else {
			t.Fatalf("list failed: %s", err2.Error())
		}

		objs, err2 = listAll(s, "", "test2", 1, true)
		if err2 != nil {
			t.Fatalf("list3 failed: %s", err2.Error())
		} else if len(objs) != 0 {
			t.Fatalf("list3 should not return anything, but got %d", len(objs))
		}
	}

	defer s.Delete("a/")
	defer s.Delete("a/a")
	if err := s.Put("a/a", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}
	defer s.Delete("a/a1")
	if err := s.Put("a/a1", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}
	defer s.Delete("b/")
	defer s.Delete("b/b")
	if err := s.Put("b/b", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}
	defer s.Delete("b/b1")
	if err := s.Put("b/b1", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}
	defer s.Delete("c/")
	//tikv will appear empty value is not supported
	if err1 := s.Put("c/", bytes.NewReader(nil)); err1 != nil {
		//minio will appear XMinioObjectExistsAsDirectory: Object name already exists as a directory. status code:  409
		if err2 := s.Put("c/", bytes.NewReader(br)); err2 != nil {
			t.Fatalf("PUT failed err1: %s, err2: %s", err1.Error(), err2.Error())
		}
	}
	defer s.Delete("a1")
	if err := s.Put("a1", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}
	defer s.Delete("a/b/c/d/e/f")
	if err := s.Put("a/b/c/d/e/f", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}

	br = []byte("hello2")
	if err := s.Put("a1", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}

	if obs, more, nextMarker, err := s.List("", "", "", "/", 4, true); err != nil {
		if !errors.Is(err, notSupported) {
			t.Fatalf("list: %s", err)
		} else {
			t.Logf("list is not supported")
		}
	} else {
		if _, ok := s.(*withPrefix).os.(FileSystem); !ok {
			keys := []string{"a/", "a1", "b/", "c/"}
			if len(obs) != 4 {
				t.Fatalf("list should return 4 results but got %d", len(obs))
			}
			for i, o := range obs {
				if o.Key() != keys[i] {
					t.Fatalf("should get key %s but got %s", keys[i], o.Key())
				}
			}
			if !more {
				t.Fatalf("should have more results")
			}
			if nextMarker == "" {
				t.Fatalf("next marker should not be empty")
			}
			obs, more, nextMarker, err = s.List("", obs[len(obs)-1].Key(), nextMarker, "/", 4, true)
			if err != nil {
				t.Fatalf("list with marker: %s", err)
			}
			if len(obs) != 1 {
				t.Fatalf("list should return 1 results but got %d", len(obs))
			}
			if obs[0].Key() != "test" {
				t.Fatalf("should get key test but got %s", obs[0].Key())
			}
			_, more, nextMarker, err = s.List("", obs[len(obs)-1].Key(), nextMarker, "/", 4, true)
			if more {
				t.Fatalf("should no more results")
			}
			if nextMarker != "" {
				t.Fatalf("next marker should not be empty")
			}
		}
	}

	if obs, _, _, err := s.List("", "", "", "/", 10, true); err != nil {
		if !errors.Is(err, notSupported) {
			t.Fatalf("list with delimiter: %s", err)
		} else {
			t.Logf("list with delimiter is not supported")
		}
	} else {
		switch s.(*withPrefix).os.(type) {
		case FileSystem:
			if len(obs) == 0 || obs[0].Key() != "" {
				t.Fatalf("list should return itself")
			} else {
				obs = obs[1:] // ignore itself
			}
		}
		if len(obs) != 5 {
			t.Fatalf("list with delimiter should return five results but got %d", len(obs))
		}
		keys := []string{"a/", "a1", "b/", "c/", "test"}
		for i, o := range obs {
			if o.Key() != keys[i] {
				t.Fatalf("should get key %s but got %s", keys[i], o.Key())
			}
		}
	}

	if obs, _, _, err := s.List("a", "", "", "/", 10, true); err != nil {
		if !errors.Is(err, notSupported) {
			t.Fatalf("list with delimiter: %s", err)
		}
	} else {
		if len(obs) != 2 {
			t.Fatalf("list with delimiter should return two results but got %d", len(obs))
		}
		keys := []string{"a/", "a1"}
		for i, o := range obs {
			if o.Key() != keys[i] {
				t.Fatalf("should get key %s but got %s", keys[i], o.Key())
			}
		}
	}

	if obs, _, _, err := s.List("a/", "", "", "/", 10, true); err != nil {
		if !errors.Is(err, notSupported) {
			t.Fatalf("list with delimiter: %s", err)
		} else {
			t.Logf("list with delimiter is not supported")
		}
	} else {
		switch s.(*withPrefix).os.(type) {
		case FileSystem:
			if len(obs) == 0 || obs[0].Key() != "a/" {
				t.Fatalf("list should return itself")
			} else {
				obs = obs[1:] // ignore itself
			}
		}
		if len(obs) != 3 {
			t.Fatalf("list with delimiter should return three results but got %d", len(obs))
		}
		keys := []string{"a/a", "a/a1", "a/b/"}
		for i, o := range obs {
			if o.Key() != keys[i] {
				t.Fatalf("should get key %s but got %s", keys[i], o.Key())
			}
		}
	}

	// test redis cluster list all api
	keyTotal := 100
	var sortedKeys []string
	for i := 0; i < keyTotal; i++ {
		k := fmt.Sprintf("hashKey%d", i)
		sortedKeys = append(sortedKeys, k)
		if err := s.Put(k, bytes.NewReader(br)); err != nil {
			t.Fatalf("PUT failed: %s", err.Error())
		}
	}
	sort.Strings(sortedKeys)
	defer func() {
		for i := 0; i < keyTotal; i++ {
			_ = s.Delete(fmt.Sprintf("hashKey%d", i))
		}
	}()
	objs, err := listAll(s, "hashKey", "", int64(keyTotal), true)
	if err != nil {
		t.Fatalf("list4 failed: %s", err.Error())
	} else {
		for i := 0; i < keyTotal; i++ {
			if objs[i].Key() != sortedKeys[i] {
				t.Fatal("The result for list4 is incorrect")
			}
			if sc != "" && objs[i].StorageClass() != sc {
				t.Fatal("storage class is not correct")
			}
		}
	}

	f, _ := os.CreateTemp("", "test")
	f.Write([]byte("this is a file"))
	f.Seek(0, 0)
	os.Remove(f.Name())
	defer f.Close()
	if err := s.Put("file", f); err != nil {
		t.Fatalf("failed to put from file")
	} else if _, err := s.Head("file"); err != nil {
		t.Fatalf("file should exists")
	} else {
		if err := s.Delete("file"); err != nil {
			t.Fatalf("delete failed %s", err)
		}
	}

	if _, err := s.Head("not-exist-file"); !os.IsNotExist(err) {
		t.Fatal("err should be os.ErrNotExist")
	}

	if o, err := s.Head("test"); err != nil {
		t.Fatalf("check exists failed: %s", err.Error())
	} else if sc != "" && o.StorageClass() != sc {
		t.Fatalf("storage class should be %s but got %s", sc, o.StorageClass())
	}

	dstKey := "test-copy"
	defer s.Delete(dstKey)
	err = s.Copy(fmt.Sprintf("%s%s", prefix, dstKey), fmt.Sprintf("%stest", prefix))
	if err != nil && err != notSupported {
		t.Fatalf("copy failed: %s", err.Error())
	}
	if err == nil {
		if o, err := s.Head(dstKey); err != nil {
			t.Fatalf("check exists failed: %s", err.Error())
		} else if sc != "" && o.StorageClass() != sc {
			t.Fatalf("storage class should be %s but got %s", sc, o.StorageClass())
		}
	}

	if err := s.Delete("test"); err != nil {
		t.Fatalf("delete failed: %s", err)
	}

	if err := s.Delete("test"); err != nil {
		t.Fatalf("delete non exists: %v", err)
	}

	getMockData := func(seed []byte, idx int) []byte {
		size := len(seed)
		if size == 0 {
			return nil
		}
		content := make([]byte, size)
		if idx == 0 {
			content = seed
		} else {
			i := idx % size
			copy(content[:size-i], seed[i:size])
			copy(content[size-i:size], seed[:i])
		}
		return content
	}
	k := "large"
	defer s.Delete(k)

	if upload, err := s.CreateMultipartUpload(k); err == nil {
		total := 3
		seed := make([]byte, upload.MinPartSize)
		utils.RandRead(seed)
		parts := make([]*Part, total)
		content := make([][]byte, total)
		for i := 0; i < total; i++ {
			content[i] = getMockData(seed, i)
		}
		pool := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for i := 1; i <= total; i++ {
			pool <- struct{}{}
			wg.Add(1)
			num := i
			go func() {
				defer func() {
					<-pool
					wg.Done()
				}()
				parts[num-1], err = s.UploadPart(k, upload.UploadID, num, content[num-1])
				if err != nil {
					t.Fatalf("multipart upload error: %v", err)
				}
			}()
		}
		wg.Wait()
		// overwrite the first part
		firstPartContent := append(getMockData(seed, 0), getMockData(seed, 0)...)
		if len(firstPartContent) < int(s.Limits().MaxPartSize) {
			firstPartContent = getMockData(seed, 0)
			firstPartContent[0] = 'a'
		}
		oldPart := parts[0]
		if parts[0], err = s.UploadPart(k, upload.UploadID, 1, firstPartContent); err != nil {
			t.Logf("overwrite the first part error: %v", err)
			parts[0] = oldPart
		} else {
			content[0] = firstPartContent
		}

		// overwrite the last part
		lastPartContent := []byte("hello")
		oldPart = parts[total-1]
		if parts[total-1], err = s.UploadPart(k, upload.UploadID, total, lastPartContent); err != nil {
			t.Logf("overwrite the last part error: %v", err)
			parts[total-1] = oldPart
		} else {
			content[total-1] = lastPartContent
		}

		if err = s.CompleteUpload(k, upload.UploadID, parts); err != nil {
			t.Fatalf("failed to complete multipart upload: %v", err)
		}
		if meta, err := s.Head(k); err != nil {
			t.Fatalf("failed to head object: %v", err)
		} else if sc != "" && meta.StorageClass() != sc {
			t.Fatalf("storage class should be %s but got %s", sc, meta.StorageClass())
		}
		checkContent := func(key string, content []byte) {
			r, err := s.Get(key, 0, -1)
			if err != nil {
				t.Fatalf("failed to get multipart upload file: %v", err)
			}
			cnt, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("failed to get multipart upload file: %v", err)
			}
			if !bytes.Equal(cnt, content) {
				t.Fatal("the content of the multipart upload file is incorrect")
			}
		}
		checkContent(k, bytes.Join(content, nil))

		if s.Limits().IsSupportUploadPartCopy {
			var copyUpload *MultipartUpload
			var dstKey = "dstUploadPartCopyKey"
			defer s.Delete(dstKey)
			if copyUpload, err = s.CreateMultipartUpload(dstKey); err != nil {
				t.Fatalf("failed to create multipart upload: %v", err)
			}
			copyParts := make([]*Part, total)
			var startIdx = 0
			for i, c := range content {
				copyParts[i], err = s.UploadPartCopy(dstKey, copyUpload.UploadID, i+1, k, int64(startIdx), int64(len(c)))
				if err != nil {
					t.Fatalf("failed to upload part copy: %v", err)
				}
				startIdx += len(c)
			}
			if err = s.CompleteUpload(dstKey, copyUpload.UploadID, copyParts); err != nil {
				t.Fatalf("failed to complete multipart upload: %v", err)
			}
			checkContent(dstKey, bytes.Join(content, nil))
		}
	} else {
		t.Logf("%s does not support multipart upload: %s", s, err.Error())
	}

	// Copy empty objects
	defer func() {
		if err := s.Delete("empty"); err != nil {
			t.Logf("delete empty file failed: %s", err)
		}
	}()

	if err := s.Put("empty", bytes.NewReader([]byte{})); err != nil {
		t.Logf("PUT empty object failed: %s", err.Error())
	}

	// Copy `/` suffixed object
	defer func() {
		if err := s.Delete("slash/"); err != nil {
			t.Logf("delete slash/ failed %s", err)
		}
	}()
	if err := s.Put("slash/", bytes.NewReader([]byte{})); err != nil {
		t.Logf("PUT `/` suffixed object failed: %s", err.Error())
	}
}

func TestMem(t *testing.T) {
	m, _ := newMem("", "", "", "")
	testStorage(t, m)
}

func TestDisk(t *testing.T) {
	_ = os.RemoveAll("/tmp/abc/")
	s, _ := newDisk("/tmp/abc/", "", "", "")
	testStorage(t, s)
}

func TestQingStor(t *testing.T) { //skip mutate
	if os.Getenv("QY_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	s, _ := newQingStor(os.Getenv("QY_ENDPOINT"),
		os.Getenv("QY_ACCESS_KEY"), os.Getenv("QY_SECRET_KEY"), "")
	testStorage(t, s)

	//private cloud
	if os.Getenv("PRIVATE_QY_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	s2, _ := newQingStor("http://test.jn1.is.shanhe.com",
		os.Getenv("PRIVATE_QY_ACCESS_KEY"), os.Getenv("PRIVATE_QY_SECRET_KEY"), "")
	testStorage(t, s2)
}

func TestS3(t *testing.T) { //skip mutate
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.SkipNow()
	}
	s, _ := newS3(os.Getenv("AWS_ENDPOINT"),
		os.Getenv("AWS_ACCESS_KEY_ID"),
		os.Getenv("AWS_SECRET_ACCESS_KEY"),
		os.Getenv("AWS_SESSION_TOKEN"))
	testStorage(t, s)
}

func TestOracleCompileRegexp(t *testing.T) {
	ep := "axntujn0ebj1.compat.objectstorage.ap-singapore-1.oraclecloud.com"
	oracleCompile := regexp.MustCompile(oracleCompileRegexp)
	if oracleCompile.MatchString(ep) {
		if submatch := oracleCompile.FindStringSubmatch(ep); len(submatch) >= 2 {
			if submatch[1] != "ap-singapore-1" {
				t.Fatalf("oracle endpoint parse failed")
			}
		} else {
			t.Fatalf("oracle endpoint parse failed")
		}
	} else {
		t.Fatalf("oracle endpoint parse failed")
	}
}

func TestOVHCompileRegexp(t *testing.T) {
	for _, ep := range []string{"s3.gra.cloud.ovh.net", "s3.gra.perf.cloud.ovh.net", "s3.gra.io.cloud.ovh.net"} {
		ovhCompile := regexp.MustCompile(OVHCompileRegexp)
		if ovhCompile.MatchString(ep) {
			if submatch := ovhCompile.FindStringSubmatch(ep); len(submatch) >= 2 {
				if submatch[1] != "gra" {
					t.Fatalf("ovh endpoint parse failed")
				}
			} else {
				t.Fatalf("ovh endpoint parse failed")
			}
		} else {
			t.Fatalf("ovh endpoint parse failed")
		}
	}
}

func TestOSS(t *testing.T) { //skip mutate
	if os.Getenv("ALICLOUD_ACCESS_KEY_ID") == "" {
		t.SkipNow()
	}
	s, _ := newOSS(os.Getenv("ALICLOUD_ENDPOINT"),
		os.Getenv("ALICLOUD_ACCESS_KEY_ID"),
		os.Getenv("ALICLOUD_ACCESS_KEY_SECRET"), "")
	testStorage(t, s)
}

func TestUFile(t *testing.T) { //skip mutate
	if os.Getenv("UCLOUD_PUBLIC_KEY") == "" {
		t.SkipNow()
	}
	ufile, _ := newUFile(os.Getenv("UCLOUD_ENDPOINT"),
		os.Getenv("UCLOUD_PUBLIC_KEY"), os.Getenv("UCLOUD_PRIVATE_KEY"), "")
	testStorage(t, ufile)
}

func TestGS(t *testing.T) { //skip mutate
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		t.SkipNow()
	}
	gs, _ := newGS(os.Getenv("GOOGLE_ENDPOINT"), "", "", "")
	testStorage(t, gs)
}

func TestQiniu(t *testing.T) { //skip mutate
	if os.Getenv("QINIU_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	qiniu, _ := newQiniu(os.Getenv("QINIU_ENDPOINT"),
		os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"), "")
	testStorage(t, qiniu)
	//qiniu, _ = newQiniu("https://test.cn-north-1-s3.qiniu.com",
	//	os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"))
	//testStorage(t, qiniu)
}

func TestKS3(t *testing.T) { //skip mutate
	if os.Getenv("KS3_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	ks3, _ := newKS3(os.Getenv("KS3_ENDPOINT"),
		os.Getenv("KS3_ACCESS_KEY"), os.Getenv("KS3_SECRET_KEY"), "")
	testStorage(t, ks3)
}

func TestCOS(t *testing.T) { //skip mutate
	if os.Getenv("COS_SECRETID") == "" {
		t.SkipNow()
	}
	cos, _ := newCOS(
		os.Getenv("COS_ENDPOINT"),
		os.Getenv("COS_SECRETID"), os.Getenv("COS_SECRETKEY"), "")
	testStorage(t, cos)
}

func TestAzure(t *testing.T) { //skip mutate
	if os.Getenv("AZURE_STORAGE_ACCOUNT") == "" {
		t.SkipNow()
	}
	//https://containersName.core.windows.net
	abs, _ := newWasb(os.Getenv("AZURE_ENDPOINT"),
		os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_KEY"), "")
	testStorage(t, abs)
}

func TestJSS(t *testing.T) { //skip mutate
	if os.Getenv("JSS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	jss, _ := newJSS(os.Getenv("JSS_ENDPOINT"),
		os.Getenv("JSS_ACCESS_KEY"), os.Getenv("JSS_SECRET_KEY"), "")
	testStorage(t, jss)
}

func TestB2(t *testing.T) { //skip mutate
	if os.Getenv("B2_ACCOUNT_ID") == "" {
		t.SkipNow()
	}
	b, err := newB2(os.Getenv("B2_ENDPOINT"), os.Getenv("B2_ACCOUNT_ID"), os.Getenv("B2_APP_KEY"), "")
	if err != nil {
		t.Fatalf("create B2: %s", err)
	}
	testStorage(t, b)
}

func TestSpace(t *testing.T) { //skip mutate
	if os.Getenv("SPACE_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b, _ := newSpace(os.Getenv("SPACE_ENDPOINT"), os.Getenv("SPACE_ACCESS_KEY"), os.Getenv("SPACE_SECRET_KEY"), "")
	testStorage(t, b)
}

func TestBOS(t *testing.T) { //skip mutate
	if os.Getenv("BDCLOUD_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b, _ := newBOS(os.Getenv("BDCLOUD_ENDPOINT"),
		os.Getenv("BDCLOUD_ACCESS_KEY"), os.Getenv("BDCLOUD_SECRET_KEY"), "")
	testStorage(t, b)
}

func TestSftp(t *testing.T) { //skip mutate
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	b, _ := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	testStorage(t, b)
}

func TestOBS(t *testing.T) { //skip mutate
	if os.Getenv("HWCLOUD_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b, _ := newOBS(os.Getenv("HWCLOUD_ENDPOINT"),
		os.Getenv("HWCLOUD_ACCESS_KEY"), os.Getenv("HWCLOUD_SECRET_KEY"), "")
	testStorage(t, b)
}

func TestNFS(t *testing.T) { //skip mutate
	if os.Getenv("NFS_ADDR") == "" {
		t.SkipNow()
	}
	b, err := newNFSStore(os.Getenv("NFS_ADDR"), os.Getenv("NFS_ACCESS_KEY"), os.Getenv("NFS_SECRET_KEY"), "")
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, b)
}

func TestHDFS(t *testing.T) { //skip mutate
	conf := make(hadoopconf.HadoopConf)
	conf["dfs.namenode.rpc-address.ns.namenode1"] = "hadoop01:8020"
	conf["dfs.namenode.rpc-address.ns.namenode2"] = "hadoop02:8020"

	checkAddr := func(addr string, expected []string, base string) {
		addresses, basePath := parseHDFSAddr(addr, conf)
		sort.Strings(addresses)
		if !reflect.DeepEqual(addresses, expected) {
			t.Fatalf("expected addrs is %+v but got %+v from %s", expected, addresses, addr)
		}
		if basePath != base {
			t.Fatalf("expected path is %s but got %s from %s", base, basePath, addr)
		}
	}

	checkAddr("hadoop01:8020", []string{"hadoop01:8020"}, "/")
	checkAddr("hdfs://hadoop01:8020/", []string{"hadoop01:8020"}, "/")
	checkAddr("hadoop01:8020/user/juicefs/", []string{"hadoop01:8020"}, "/user/juicefs/")
	checkAddr("hadoop01:8020/user/juicefs", []string{"hadoop01:8020"}, "/user/juicefs/")
	checkAddr("hdfs://hadoop01:8020/user/juicefs/", []string{"hadoop01:8020"}, "/user/juicefs/")

	// for HA
	checkAddr("hadoop01:8020,hadoop02:8020", []string{"hadoop01:8020", "hadoop02:8020"}, "/")
	checkAddr("hadoop01:8020,hadoop02:8020/user/juicefs/", []string{"hadoop01:8020", "hadoop02:8020"}, "/user/juicefs/")
	checkAddr("hdfs://ns/user/juicefs", []string{"hadoop01:8020", "hadoop02:8020"}, "/user/juicefs/")
	checkAddr("ns/user/juicefs/", []string{"hadoop01:8020", "hadoop02:8020"}, "/user/juicefs/")

	if os.Getenv("HDFS_ADDR") == "" {
		t.SkipNow()
	}
	dfs, _ := newHDFS(os.Getenv("HDFS_ADDR"), "", "", "")
	testStorage(t, dfs)
}

func TestOOS(t *testing.T) { //skip mutate
	if os.Getenv("OOS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b, _ := newOOS(os.Getenv("OOS_ENDPOINT"),
		os.Getenv("OOS_ACCESS_KEY"), os.Getenv("OOS_SECRET_KEY"), "")
	testStorage(t, b)
}

func TestScw(t *testing.T) { //skip mutate
	if os.Getenv("SCW_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b, _ := newScw(os.Getenv("SCW_ENDPOINT"), os.Getenv("SCW_ACCESS_KEY"), os.Getenv("SCW_SECRET_KEY"), "")
	testStorage(t, b)
}

func TestMinIO(t *testing.T) {
	if os.Getenv("MINIO_TEST_BUCKET") == "" {
		t.SkipNow()
	}
	b, _ := newMinio(os.Getenv("MINIO_TEST_BUCKET"), os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), "")
	testStorage(t, b)
}

// func TestUpYun(t *testing.T) {
// 	s, _ := newUpyun("http://jfstest", "test", "")
// 	testStorage(t, s)
// }

func TestTiKV(t *testing.T) { //skip mutate
	if os.Getenv("TIKV_ADDR") == "" {
		t.SkipNow()
	}
	s, err := newTiKV(os.Getenv("TIKV_ADDR"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)
}

func TestRedis(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.SkipNow()
	}

	opt, _ := redis.ParseURL(os.Getenv("REDIS_ADDR"))
	rdb := redis.NewClient(opt)
	_ = rdb.FlushDB(context.Background())

	s, err := newRedis(os.Getenv("REDIS_ADDR"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)
}

func TestSwift(t *testing.T) { //skip mutate
	if os.Getenv("SWIFT_ADDR") == "" {
		t.SkipNow()
	}
	s, err := newSwiftOSS(os.Getenv("SWIFT_ADDR"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)
}

func TestWebDAV(t *testing.T) { //skip mutate
	if os.Getenv("WEBDAV_TEST_BUCKET") == "" {
		t.SkipNow()
	}
	s, _ := newWebDAV(os.Getenv("WEBDAV_TEST_BUCKET"), "", "", "")
	testStorage(t, s)
}

func TestEncrypted(t *testing.T) {
	s, _ := CreateStorage("mem", "", "", "", "")
	privkey, _ := rsa.GenerateKey(rand.Reader, 2048)
	kc := NewRSAEncryptor(privkey)
	dc, _ := NewDataEncryptor(kc, AES256GCM_RSA)
	es := NewEncrypted(s, dc)
	testStorage(t, es)
}

func TestMarsharl(t *testing.T) {
	if os.Getenv("HDFS_ADDR") == "" {
		t.SkipNow()
	}
	s, _ := newHDFS(os.Getenv("HDFS_ADDR"), "", "", "")
	if err := s.Put("hello", bytes.NewReader([]byte("world"))); err != nil {
		t.Fatalf("PUT failed: %s", err)
	}
	fs := s.(FileSystem)
	_ = fs.Chown("hello", "user", "group")
	_ = fs.Chmod("hello", 0764)
	o, err := s.Head("hello")
	if err != nil {
		t.Fatalf("HEAD failed: %s", err)
	}

	m := MarshalObject(o)
	d, _ := json.Marshal(m)
	var m2 map[string]interface{}
	if err := json.Unmarshal(d, &m2); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	o2 := UnmarshalObject(m2)
	if math.Abs(float64(o2.Mtime().UnixNano()-o.Mtime().UnixNano())) > 1000 {
		t.Fatalf("mtime %s != %s", o2.Mtime(), o.Mtime())
	}
	o2.(*file).mtime = o.Mtime()
	if !reflect.DeepEqual(o, o2) {
		t.Fatalf("%+v != %+v", o2, o)
	}
}

func TestSharding(t *testing.T) {
	s, _ := NewSharded("mem", "%d", "", "", "", 10)
	testStorage(t, s)
}

func TestSQLite(t *testing.T) {
	s, err := newSQLStore("sqlite3", "/tmp/teststore.db", "", "")
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	testStorage(t, s)
}

func TestPG(t *testing.T) { //skip mutate
	if os.Getenv("PG_ADDR") == "" {
		t.SkipNow()
	}
	s, err := newSQLStore("postgres", os.Getenv("PG_ADDR"), os.Getenv("PG_USER"), os.Getenv("PG_PASSWORD"))
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	testStorage(t, s)

}
func TestPGWithSearchPath(t *testing.T) { //skip mutate
	_, err := newSQLStore("postgres", "127.0.0.1:5432/test?sslmode=disable&search_path=juicefs,public", "", "")
	if !strings.Contains(err.Error(), "currently, only one schema is supported in search_path") {
		t.Fatalf("TestPGWithSearchPath error: %s", err)
	}
}

func TestMySQL(t *testing.T) { //skip mutate
	if os.Getenv("MYSQL_ADDR") == "" {
		t.SkipNow()
	}
	s, err := newSQLStore("mysql", os.Getenv("MYSQL_ADDR"), os.Getenv("MYSQL_USER"), os.Getenv("MYSQL_PASSWORD"))
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	testStorage(t, s)
}

func TestNameString(t *testing.T) {
	s, _ := newMem("test", "", "", "")
	s = WithPrefix(s, "a/")
	s = WithPrefix(s, "b/")
	if s.String() != "mem://test/a/b/" {
		t.Fatalf("name with two prefix does not match: %s", s.String())
	}
}

func TestEtcd(t *testing.T) { //skip mutate
	if os.Getenv("ETCD_ADDR") == "" {
		t.SkipNow()
	}
	s, _ := newEtcd(os.Getenv("ETCD_ADDR"), "", "", "")
	testStorage(t, s)
}

//func TestCeph(t *testing.T) {
//	if os.Getenv("CEPH_ENDPOINT") == "" {
//		t.SkipNow()
//	}
//	s, _ := newCeph(os.Getenv("CEPH_ENDPOINT"), os.Getenv("CEPH_CLUSTER"), os.Getenv("CEPH_USER"))
//	testStorage(t, s)
//}

func TestEOS(t *testing.T) { //skip mutate
	if os.Getenv("EOS_ENDPOINT") == "" {
		t.SkipNow()
	}
	s, _ := newEos(os.Getenv("EOS_ENDPOINT"), os.Getenv("EOS_ACCESS_KEY"), os.Getenv("EOS_SECRET_KEY"), "")
	testStorage(t, s)
}

func TestWASABI(t *testing.T) { //skip mutate
	if os.Getenv("WASABI_ENDPOINT") == "" {
		t.SkipNow()
	}
	s, _ := newWasabi(os.Getenv("WASABI_ENDPOINT"), os.Getenv("WASABI_ACCESS_KEY"), os.Getenv("WASABI_SECRET_KEY"), "")
	testStorage(t, s)
}

func TestSCS(t *testing.T) { //skip mutate
	if os.Getenv("SCS_ENDPOINT") == "" {
		t.SkipNow()
	}
	s, _ := newSCS(os.Getenv("SCS_ENDPOINT"), os.Getenv("SCS_ACCESS_KEY"), os.Getenv("SCS_SECRET_KEY"), "")
	testStorage(t, s)
}

func TestIBMCOS(t *testing.T) { //skip mutate
	if os.Getenv("IBMCOS_ENDPOINT") == "" {
		t.SkipNow()
	}
	s, _ := newIBMCOS(os.Getenv("IBMCOS_ENDPOINT"), os.Getenv("IBMCOS_ACCESS_KEY"), os.Getenv("IBMCOS_SECRET_KEY"), "")
	testStorage(t, s)
}

func TestTOS(t *testing.T) { //skip mutate
	if os.Getenv("TOS_ENDPOINT") == "" {
		t.SkipNow()
	}
	tos, err := newTOS(os.Getenv("TOS_ENDPOINT"), os.Getenv("TOS_ACCESS_KEY"), os.Getenv("TOS_SECRET_KEY"), "")
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	testStorage(t, tos)
}

func TestDragonfly(t *testing.T) { //skip mutate
	if os.Getenv("DRAGONFLY_ENDPOINT") == "" {
		t.SkipNow()
	}
	dragonfly, err := newDragonfly(os.Getenv("DRAGONFLY_ENDPOINT"), "", "", "")
	if err != nil {
		t.Fatalf("create: %s", err)
	}
	testStorage(t, dragonfly)
}

// func TestBunny(t *testing.T) { //skip mutate
// 	if os.Getenv("BUNNY_ENDPOINT") == "" {
// 		t.SkipNow()
// 	}
// 	bunny, err := newBunny(os.Getenv("BUNNY_ENDPOINT"), "", os.Getenv("BUNNY_SECRET_KEY"), "")
// 	if err != nil {
// 		t.Fatalf("create: %s", err)
// 	}
// 	testStorage(t, bunny)
// }

func TestMain(m *testing.M) {
	if envFile := os.Getenv("JUICEFS_ENV_FILE_FOR_TEST"); envFile != "" {
		// schema: S3 AWS_ENDPOINT=xxxxx
		if _, err := os.Stat(envFile); err == nil {
			file, _ := os.ReadFile(envFile)
			for _, line := range strings.Split(strings.TrimSpace(string(file)), "\n") {
				if envkv := strings.SplitN(line, "=", 2); len(envkv) == 2 {
					if err := os.Setenv(envkv[0], envkv[1]); err != nil {
						logger.Errorf("set env %s=%s error", envkv[0], envkv[1])
					}
				}
			}
		}
	}
	m.Run()
}
