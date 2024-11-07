package object

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"
)

var name string

func init() {
	flag.StringVar(&name, "name", "", "name of object storage")
}
func TestList(t *testing.T) {
	t.Logf(name)
	var s ObjectStorage
	var err error
	switch name {
	case "disk":
		_ = os.RemoveAll("/tmp/abc/")
		s, err = newDisk("/tmp/abc/", "", "", "")
	case "qingstor":
		s, err = newQingStor(os.Getenv("QY_ENDPOINT"), os.Getenv("QY_ACCESS_KEY"), os.Getenv("QY_SECRET_KEY"), "")
	case "s3":
		s, err = newS3(os.Getenv("AWS_ENDPOINT"), os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN"))
	case "oss":
		s, err = newOSS(os.Getenv("ALICLOUD_ENDPOINT"), os.Getenv("ALICLOUD_ACCESS_KEY_ID"), os.Getenv("ALICLOUD_ACCESS_KEY_SECRET"), "")
	case "ufile":
		s, err = newUFile(os.Getenv("UCLOUD_ENDPOINT"), os.Getenv("UCLOUD_PUBLIC_KEY"), os.Getenv("UCLOUD_PRIVATE_KEY"), "")
	case "gs":
		s, err = newGS(os.Getenv("GOOGLE_ENDPOINT"), "", "", "")
	case "qiniu":
		s, err = newQiniu(os.Getenv("QINIU_ENDPOINT"), os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"), "")
	case "ks3":
		s, err = newKS3(os.Getenv("KS3_ENDPOINT"), os.Getenv("KS3_ACCESS_KEY"), os.Getenv("KS3_SECRET_KEY"), "")
	case "cos":
		s, err = newCOS(os.Getenv("COS_ENDPOINT"), os.Getenv("COS_SECRETID"), os.Getenv("COS_SECRETKEY"), "")
	case "azure":
		s, err = newWasb(os.Getenv("AZURE_ENDPOINT"), os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_KEY"), "")
	case "jss":
		s, err = newJSS(os.Getenv("JSS_ENDPOINT"), os.Getenv("JSS_ACCESS_KEY"), os.Getenv("JSS_SECRET_KEY"), "")
	case "b2":
		s, err = newB2(os.Getenv("B2_ENDPOINT"), os.Getenv("B2_ACCOUNT_ID"), os.Getenv("B2_APP_KEY"), "")
	case "space":
		s, err = newSpace(os.Getenv("SPACE_ENDPOINT"), os.Getenv("SPACE_ACCESS_KEY"), os.Getenv("SPACE_SECRET_KEY"), "")
	case "bos":
		s, err = newBOS(os.Getenv("BDCLOUD_ENDPOINT"), os.Getenv("BDCLOUD_ACCESS_KEY"), os.Getenv("BDCLOUD_SECRET_KEY"), "")
	case "sftp":
		s, err = newSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	case "obs":
		s, err = newOBS(os.Getenv("HWCLOUD_ENDPOINT"), os.Getenv("HWCLOUD_ACCESS_KEY"), os.Getenv("HWCLOUD_SECRET_KEY"), "")
	case "nfs":
		s, err = newNFSStore(os.Getenv("NFS_ADDR"), os.Getenv("NFS_ACCESS_KEY"), os.Getenv("NFS_SECRET_KEY"), "")
	case "oos":
		s, err = newOOS(os.Getenv("OOS_ENDPOINT"), os.Getenv("OOS_ACCESS_KEY"), os.Getenv("OOS_SECRET_KEY"), "")
	case "minio":
		s, err = newMinio(os.Getenv("MINIO_TEST_BUCKET"), os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), "")
	case "eos":
		s, err = newEos(os.Getenv("EOS_ENDPOINT"), os.Getenv("EOS_ACCESS_KEY"), os.Getenv("EOS_SECRET_KEY"), "")
	case "wasabi":
		s, err = newWasabi(os.Getenv("WASABI_ENDPOINT"), os.Getenv("WASABI_ACCESS_KEY"), os.Getenv("WASABI_SECRET_KEY"), "")
	case "scs":
		s, err = newSCS(os.Getenv("SCS_ENDPOINT"), os.Getenv("SCS_ACCESS_KEY"), os.Getenv("SCS_SECRET_KEY"), "")
	case "ibmcos":
		s, err = newIBMCOS(os.Getenv("IBMCOS_ENDPOINT"), os.Getenv("IBMCOS_ACCESS_KEY"), os.Getenv("IBMCOS_SECRET_KEY"), "")
	case "tos":
		s, err = newTOS(os.Getenv("TOS_ENDPOINT"), os.Getenv("TOS_ACCESS_KEY"), os.Getenv("TOS_SECRET_KEY"), "")
	}
	if err != nil {
		t.Fatalf("create storage err: %s", err)
	}
	testList(t, s)
}

func testList(t *testing.T, s ObjectStorage) {
	prefix := "listV2-test/"
	s = WithPrefix(s, prefix)

	_, err := s.Head("999_file")
	if errors.Is(err, os.ErrNotExist) {
		for i := 0; i < 1000; i++ {
			_ = s.Put(fmt.Sprintf("%d_dir/%d_file", i, i), bytes.NewReader([]byte("a")))
			_ = s.Put(fmt.Sprintf("%d_file", i), bytes.NewReader([]byte("a")))
			t.Logf("put %d \n", i)
		}
	}
	t.Logf("Data is ready")
	var duration time.Duration
	for i := 0; i < 100; i++ {
		start := time.Now()
		objs, _, _, err := ListWrap(s, "", "", "", "/", 1000, true)
		since := time.Since(start)
		t.Logf("list %d took %s", i, since)
		duration += since
		if err != nil {
			t.Fatal(err)
		}
		if len(objs) != 1000 {
			t.Fatalf("list should return 1000 results but got %d", len(objs))
		}
		t.Logf("list %d done", i)
	}
	t.Logf("average list took %s", duration/100)
}
