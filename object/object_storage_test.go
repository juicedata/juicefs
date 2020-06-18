package object

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"
)

func get(s ObjectStorage, k string, off, limit int64) (string, error) {
	r, err := s.Get("/test", off, limit)
	if err != nil {
		return "", err
	}
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func testStorage(t *testing.T, s ObjectStorage) {
	s = WithPrefix(s, "unit-test")
	defer s.Delete("/test")
	defer s.Delete("/test2")
	k := "/large"
	defer s.Delete(k)

	_, err := s.Get("/not_exists", 0, -1)
	if err == nil {
		t.Fatalf("Get should failed: %s", err)
	}

	br := []byte("hello")
	if err := s.Put("/test", bytes.NewReader(br)); err != nil {
		t.Fatalf("PUT failed: %s", err.Error())
	}

	if d, e := get(s, "/test", 0, -1); d != "hello" {
		t.Fatalf("expect hello, but got %v, error:%s", d, e)
	}
	if d, e := get(s, "/test", 2, -1); d != "llo" {
		t.Fatalf("expect llo, but got %v, error:%s", d, e)
	}
	if d, e := get(s, "/test", 2, 2); d != "ll" {
		t.Fatalf("expect ll, but got %v, error:%s", d, e)
	}
	if d, e := get(s, "/test", 4, 2); d != "o" {
		// OSS fail
		t.Errorf("out-of-range get: 'o', but got %v, error:%s", len(d), e)
	}

	objs, err2 := s.List("", "", 1)
	if err2 == nil {
		if len(objs) != 1 {
			t.Fatalf("List should return 1 keys, but got %d", len(objs))
		}
		if objs[0].Key != "/test" {
			t.Fatalf("First key shold be /test, but got %s", objs[0].Key)
		}
		if !strings.Contains(s.String(), "encrypted") && objs[0].Size != 5 {
			t.Fatalf("Size of first key shold be 5, but got %v", objs[0].Size)
		}
		now := int(time.Now().Unix())
		if objs[0].Mtime < now-10 || objs[0].Mtime > now+10 {
			t.Fatalf("Mtime of key should be within 10 seconds")
		}
	} else {
		t.Fatalf("list failed: %s", err2.Error())
	}

	objs, err2 = s.List("", "/test", 10240)
	if err2 == nil {
		if len(objs) != 1 {
			t.Fatalf("List should return 2 keys, but got %d", len(objs))
		}
		if objs[0].Key != "/test2" {
			t.Fatalf("Second key shold be /test2")
		}
		if !strings.Contains(s.String(), "encrypted") && objs[0].Size != 5 {
			t.Fatalf("Size of first key shold be 5, but got %v", objs[0].Size)
		}
		now := int(time.Now().Unix())
		if objs[0].Mtime < now-10 || objs[0].Mtime > now+10 {
			t.Fatalf("Mtime of key should be within 10 seconds")
		}
	} else {
		t.Fatalf("list2 failed: %s", err2.Error())
	}

	objs, err2 = s.List("", "/test2", 1)
	if err2 != nil {
		t.Fatalf("list3 failed: %s", err2.Error())
	} else if len(objs) != 0 {
		t.Fatalf("list3 should not return anything, but got %d", len(objs))
	}

	f, _ := ioutil.TempFile("", "test")
	f.Write([]byte("this is a file"))
	f.Seek(0, 0)
	os.Remove(f.Name())
	defer f.Close()
	if err := s.Put("/file", f); err != nil {
		t.Fatalf("failed to put from file")
	} else if s.Exists("/file") != nil {
		t.Fatalf("/file should exists")
	} else {
		s.Delete("/file")
	}

	if err := s.Exists("/test"); err != nil {
		t.Fatalf("check exists failed: %s", err.Error())
	}

	if err := s.Delete("/test"); err != nil {
		t.Fatalf("delete failed: %s", err)
	}

	if err := s.Delete("/test"); err == nil {
		t.Fatalf("delete non exists: %v", err)
	}

	if uploader, err := s.CreateMultipartUpload(k); err == nil {
		partSize := uploader.MinPartSize
		uploadID := uploader.UploadID
		defer s.AbortUpload(k, uploadID)

		part1, err := s.UploadPart(k, uploadID, 1, make([]byte, partSize))
		if err != nil {
			t.Fatalf("UploadPart 1 failed: %s", err)
		}
		if pending, marker, err := s.ListUploads(""); err != nil {
			t.Logf("ListMultipart fail: %s", err.Error())
		} else {
			println(len(pending), marker)
		}
		part2Size := 1 << 20
		part2, err := s.UploadPart(k, uploadID, 2, make([]byte, part2Size))
		if err != nil {
			t.Fatalf("UploadPart 2 failed: %s", err)
		}
		part2Size = 2 << 20
		part2, err = s.UploadPart(k, uploadID, 2, make([]byte, part2Size))
		if err != nil {
			t.Fatalf("UploadPart 2 failed: %s", err)
		}

		if err := s.CompleteUpload(k, uploadID, []*Part{part1, part2}); err != nil {
			t.Fatalf("CompleteMultipart failed: %s", err.Error())
		}
		if in, err := s.Get(k, 0, -1); err != nil {
			t.Fatalf("large not exists")
		} else if d, err := ioutil.ReadAll(in); err != nil {
			t.Fatalf("fail to read large file")
		} else if len(d) != partSize+part2Size {
			t.Fatalf("size of large file: %d != %d", len(d), partSize+part2Size)
		}
	} else {
		t.Logf("%s does not support multipart upload: %s", s, err.Error())
	}

	// Copy empty objects
	defer s.Delete("/empty")
	defer s.Delete("/empty1")
	if err := s.Put("/empty", bytes.NewReader([]byte{})); err != nil {
		t.Fatalf("PUT empty object failed: %s", err.Error())
	}

	// Copy `/` suffixed object
	defer s.Delete("/slash/")
	defer s.Delete("/slash1/")
	if err := s.Put("/slash/", bytes.NewReader([]byte{})); err != nil {
		t.Fatalf("PUT `/` suffixed object failed: %s", err.Error())
	}
}

func TestMem(t *testing.T) {
	m := newMem("", "", "")
	testStorage(t, m)
}

func TestDisk(t *testing.T) {
	s := newDisk("/tmp/abc", "", "")
	testStorage(t, s)
}

func TestQingStor(t *testing.T) {
	if os.Getenv("QY_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	s := newQingStor("https://test.pek3a.qingstor.com",
		os.Getenv("QY_ACCESS_KEY"), os.Getenv("QY_SECRET_KEY"))
	testStorage(t, s)
}

func TestS3(t *testing.T) {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.SkipNow()
	}
	s := newS3("https://cfs-test-tmp1.s3-us-west-2.amazonaws.com",
		os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"))
	testStorage(t, s)
	s = newS3("https://jfs-test-tmp.s3.cn-north-1.amazonaws.com.cn",
		os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"))
	testStorage(t, s)
}

func TestOSS(t *testing.T) {
	if os.Getenv("OSS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	s := newOSS("https://test.oss-us-west-1.aliyuncs.com",
		os.Getenv("OSS_ACCESS_KEY"), os.Getenv("OSS_SECRET_KEY"))
	testStorage(t, s)
}

func TestUFile(t *testing.T) {
	if os.Getenv("UCLOUD_PUBLIC_KEY") == "" {
		t.SkipNow()
	}
	ufile := newUFile("https://test.us-ca.ufileos.com",
		os.Getenv("UCLOUD_PUBLIC_KEY"), os.Getenv("UCLOUD_PRIVATE_KEY"))
	testStorage(t, ufile)
}

func TestGS(t *testing.T) {
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" {
		t.SkipNow()
	}
	os.Setenv("GOOGLE_CLOUD_PROJECT", "davies-test")
	gs := newGS("https://test.us-west1.googleapi.com", "", "")
	testStorage(t, gs)
}

func TestQiniu(t *testing.T) {
	if os.Getenv("QINIU_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	qiniu := newQiniu("https://test.cn-east-1-s3.qiniu.com",
		os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"))
	testStorage(t, qiniu)
	qiniu = newQiniu("https://test.cn-north-1-s3.qiniu.com",
		os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"))
	testStorage(t, qiniu)
}

func TestKS3(t *testing.T) {
	if os.Getenv("KS3_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	ks3 := newKS3("https://test.kss.ksyun.com",
		os.Getenv("KS3_ACCESS_KEY"), os.Getenv("KS3_SECRET_KEY"))
	testStorage(t, ks3)
}

func TestCOS(t *testing.T) {
	if os.Getenv("COS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	cos := newCOS(
		fmt.Sprintf("https://test-%s.cos.ap-beijing.myqcloud.com", os.Getenv("COS_APPID")),
		os.Getenv("COS_ACCESS_KEY"), os.Getenv("COS_SECRET_KEY"))
	testStorage(t, cos)
}

func TestAzure(t *testing.T) {
	if os.Getenv("AZURE_STORAGE_ACCOUNT") == "" {
		t.SkipNow()
	}
	abs := newWabs("https://test-chunk.core.chinacloudapi.cn",
		os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_KEY"))
	testStorage(t, abs)
}

func TestNOS(t *testing.T) {
	if os.Getenv("NOS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	nos := newNOS("https://test.nos-eastchina1.126.net",
		os.Getenv("NOS_ACCESS_KEY"), os.Getenv("NOS_SECRET_KEY"))
	testStorage(t, nos)
}

func TestMSS(t *testing.T) {
	if os.Getenv("MSS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	mss := newMSS("https://test.mtmss.com",
		os.Getenv("MSS_ACCESS_KEY"), os.Getenv("MSS_SECRET_KEY"))
	testStorage(t, mss)
}

func TestJSS(t *testing.T) {
	if os.Getenv("JSS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	jss := newJSS("https://test.s3.cn-north-1.jcloudcs.com",
		os.Getenv("JSS_ACCESS_KEY"), os.Getenv("JSS_SECRET_KEY"))
	testStorage(t, jss)
}

func TestSpeedy(t *testing.T) {
	if os.Getenv("SPEEDY_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	cos := newSpeedy("https://test.oss-cn-beijing.speedycloud.org",
		os.Getenv("SPEEDY_ACCESS_KEY"), os.Getenv("SPEEDY_SECRET_KEY"))
	testStorage(t, cos)
}

func TestB2(t *testing.T) {
	if os.Getenv("B2_ACCOUNT_ID") == "" {
		t.SkipNow()
	}
	b := newB2("https://test.backblaze.com", os.Getenv("B2_ACCOUNT_ID"), os.Getenv("B2_APP_KEY"))
	testStorage(t, b)
}

func TestSpace(t *testing.T) {
	if os.Getenv("SPACE_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b := newSpace("https://test.nyc3.digitaloceanspaces.com", os.Getenv("SPACE_ACCESS_KEY"), os.Getenv("SPACE_SECRET_KEY"))
	testStorage(t, b)
}

func TestBOS(t *testing.T) {
	if os.Getenv("BOS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b := newBOS("https://test.su.bcebos.com", os.Getenv("BOS_ACCESS_KEY"), os.Getenv("BOS_SECRET_KEY"))
	testStorage(t, b)
}

func TestSftp(t *testing.T) {
	if os.Getenv("SFTP_HOST") == "" {
		t.SkipNow()
	}
	b := newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"))
	testStorage(t, b)
}

func TestOBS(t *testing.T) {
	if os.Getenv("OBS_ACCESS_KEY") == "" {
		t.SkipNow()
	}
	b := newObs("https://test.obs.cn-north-1.myhwclouds.com", os.Getenv("OBS_ACCESS_KEY"), os.Getenv("OBS_SECRET_KEY"))
	testStorage(t, b)
}

func TestHDFS(t *testing.T) {
	if os.Getenv("HDFS_ADDR") == "" {
		t.Skip()
	}
	dfs := newHDFS(os.Getenv("HDFS_ADDR"), "", "")
	testStorage(t, dfs)
}
