package object

import (
	"bytes"
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
	if err := s.Create(); err != nil {
		t.Fatalf("Can't create bucket %s: %s", s, err.Error())
	}

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
		t.Errorf("out-of-range get: 'o', but got %v, error:%s", d, e)
	}

	if err := s.Copy("/test2", "/test"); err != nil {
		t.Fatalf("copy failed: %s", err)
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
		t.Fatalf("list failed", err2.Error())
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
		t.Fatalf("list2 failed", err2.Error())
	}

	objs, err2 = s.List("", "/test2", 1)
	if err2 != nil {
		t.Fatalf("list3 failed", err2.Error())
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
}

func TestMem(t *testing.T) {
	m := newMem("", "", "")
	testStorage(t, m)
}

func TestQingStor(t *testing.T) {
	s := newQingStor("https://cfs-test-tmp1.pek3a.qingstor.com",
		os.Getenv("QY_ACCESS_KEY"), os.Getenv("QY_SECRET_KEY"))
	testStorage(t, s)
}

func TestS3(t *testing.T) {
	s := newS3("https://cfs-test-tmp1.s3-us-west-2.amazonaws.com",
		os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"))
	testStorage(t, s)
	s = newS3("https://jfs-test-tmp.s3-us-east-1.amazonaws.com",
		os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"))
	testStorage(t, s)
}

func TestOSS(t *testing.T) {
	s := newOSS("https://cfs-test-tmp1.oss-us-west-1.aliyuncs.com",
		os.Getenv("OSS_ACCESS_KEY"), os.Getenv("OSS_SECRET_KEY"))
	testStorage(t, s)
}

func TestUFile(t *testing.T) {
	ufile := newUFile("https://cfs-test-tmp-2.us-ca.ufileos.com",
		os.Getenv("UCLOUD_PUBLIC_KEY"), os.Getenv("UCLOUD_PRIVATE_KEY"))
	testStorage(t, ufile)
}

func TestGS(t *testing.T) {
	os.Setenv("GOOGLE_CLOUD_PROJECT", "davies-test")
	gs := newGS("https://jfs-test-2.us-west1.googleapi.com", "", "")
	testStorage(t, gs)
}

func TestQiniu(t *testing.T) {
	qiniu := newQiniu("https://jfs-test2.cn-east-1-s3.qiniu.com",
		os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"))
	testStorage(t, qiniu)
	qiniu = newQiniu("https://jfs-test-tmp.cn-north-1-s3.qiniu.com",
		os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"))
	testStorage(t, qiniu)
}

func TestReplicated(t *testing.T) {
	s1 := newMem("", "", "")
	s2 := newMem("", "", "")

	rep := NewReplicated(s1, s2)
	testStorage(t, rep)

	// test healing
	s2.Put("/a", bytes.NewBuffer([]byte("a")))
	if r, e := rep.Get("/a", 0, 1); e != nil {
		t.Fatalf("Fail to get /a")
	} else if s, _ := ioutil.ReadAll(r); string(s) != "a" {
		t.Fatalf("Fail to get /a")
	}
	if s1.Exists("/a") == nil {
		t.Fatalf("a should not be in s1")
	}
	if r, e := rep.Get("/a", 0, -1); e != nil {
		t.Fatalf("Fail to get /a")
	} else if s, _ := ioutil.ReadAll(r); string(s) != "a" {
		t.Fatalf("Fail to get /a")
	}
	if s1.Exists("/a") != nil {
		t.Fatalf("a should be in s1")
	}

	// test sync
	s1.Put("b", bytes.NewBuffer([]byte("a")))
	s2.Put("c", bytes.NewBuffer(make([]byte, 15<<20)))
	r := rep.(*Replicated)
	r.Backfill("b")
	if s2.Exists("b") == nil {
		t.Fatalf("b should not be in s2")
	}
	r.Backfill("c")
	if s1.Exists("c") != nil {
		t.Fatalf("c should be in s1")
	}
}

func TestKS3(t *testing.T) {
	ks3 := newKS3("https://jfs-temp4.kss.ksyun.com",
		os.Getenv("KS3_ACCESS_KEY"), os.Getenv("KS3_SECRET_KEY"))
	testStorage(t, ks3)
}

func TestCOS(t *testing.T) {
	cos := newCOS("https://jfstest1-1252455339.cos.ap-beijing.myqcloud.com",
		os.Getenv("COS_ACCESS_KEY"), os.Getenv("COS_SECRET_KEY"))
	testStorage(t, cos)
}

func TestAzure(t *testing.T) {
	abs := newAbs("https://test-chunk.core.chinacloudapi.cn",
		os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_KEY"))
	testStorage(t, abs)
}

func TestNOS(t *testing.T) {
	nos := newNOS("https://jfs-test.nos-eastchina1.126.net",
		os.Getenv("NOS_ACCESS_KEY"), os.Getenv("NOS_SECRET_KEY"))
	testStorage(t, nos)
}

func TestMSS(t *testing.T) {
	mss := newMSS("https://jfstest.mtmss.com",
		os.Getenv("MSS_ACCESS_KEY"), os.Getenv("MSS_SECRET_KEY"))
	testStorage(t, mss)
}

func TestJSS(t *testing.T) {
	jss := newJSS("https://jfstest.s3.cn-north-1.jcloudcs.com",
		os.Getenv("JSS_ACCESS_KEY"), os.Getenv("JSS_SECRET_KEY"))
	testStorage(t, jss)
}

func TestSpeedy(t *testing.T) {
	cos := newSpeedy("https://jfs-test.oss-cn-beijing.speedycloud.org",
		os.Getenv("SPEEDY_ACCESS_KEY"), os.Getenv("SPEEDY_SECRET_KEY"))
	testStorage(t, cos)
}

func TestDisk(t *testing.T) {
	s := newDisk("/tmp/abc", "", "")
	testStorage(t, s)
}

func TestB2(t *testing.T) {
	b := newB2("https://jfs-test.backblaze.com", os.Getenv("B2_ACCOUNT_ID"), os.Getenv("B2_APP_KEY"))
	testStorage(t, b)
}

func TestSpace(t *testing.T) {
	b := newSpace("https://jfs-test.nyc3.digitaloceanspaces.com", os.Getenv("SPACE_ACCESS_KEY"), os.Getenv("SPACE_SECRET_KEY"))
	testStorage(t, b)
}

func TestBOS(t *testing.T) {
	b := newBOS("https://jfs-test.su.bcebos.com", os.Getenv("BOS_ACCESS_KEY"), os.Getenv("BOS_SECRET_KEY"))
	testStorage(t, b)
}
