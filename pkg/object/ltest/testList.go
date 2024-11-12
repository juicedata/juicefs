package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"os"
	"sync"
	"time"
)

var name string
var delimiter string
var prefix string
var makeData bool
var parallel int64
var commPrefix string

var logger = utils.GetLogger("juicefs")

func main() {
	flag.StringVar(&name, "name", "", "name of object storage")
	flag.StringVar(&delimiter, "delimiter", "", "use delimiter")
	flag.StringVar(&prefix, "prefix", "", "prefix")
	flag.StringVar(&commPrefix, "commPrefix", "list-prefix/", "commPrefix")
	flag.BoolVar(&makeData, "makedata", false, "make data")
	flag.Int64Var(&parallel, "parallel", 100, "parallel")
	flag.Parse()
	var s object.ObjectStorage
	var err error
	switch name {
	case "disk":
		_ = os.RemoveAll("/tmp/abc/")
		s, err = object.NewDisk("/tmp/abc/", "", "", "")
	case "qingstor":
		s, err = object.NewQingStor(os.Getenv("QY_ENDPOINT"), os.Getenv("QY_ACCESS_KEY"), os.Getenv("QY_SECRET_KEY"), "")
	case "s3":
		s, err = object.NewS3(os.Getenv("AWS_ENDPOINT"), os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN"))
	case "oss":
		s, err = object.NewOSS(os.Getenv("ALICLOUD_ENDPOINT"), os.Getenv("ALICLOUD_ACCESS_KEY_ID"), os.Getenv("ALICLOUD_ACCESS_KEY_SECRET"), "")
	case "ufile":
		s, err = object.NewUFile(os.Getenv("UCLOUD_ENDPOINT"), os.Getenv("UCLOUD_PUBLIC_KEY"), os.Getenv("UCLOUD_PRIVATE_KEY"), "")
	case "gs":
		s, err = object.NewGS(os.Getenv("GOOGLE_ENDPOINT"), "", "", "")
	case "qiniu":
		s, err = object.NewQiniu(os.Getenv("QINIU_ENDPOINT"), os.Getenv("QINIU_ACCESS_KEY"), os.Getenv("QINIU_SECRET_KEY"), "")
	case "ks3":
		s, err = object.NewKS3(os.Getenv("KS3_ENDPOINT"), os.Getenv("KS3_ACCESS_KEY"), os.Getenv("KS3_SECRET_KEY"), "")
	case "cos":
		s, err = object.NewCOS(os.Getenv("COS_ENDPOINT"), os.Getenv("COS_SECRETID"), os.Getenv("COS_SECRETKEY"), "")
	case "azure":
		s, err = object.NewWasb(os.Getenv("AZURE_ENDPOINT"), os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_KEY"), "")
	case "jss":
		s, err = object.NewJSS(os.Getenv("JSS_ENDPOINT"), os.Getenv("JSS_ACCESS_KEY"), os.Getenv("JSS_SECRET_KEY"), "")
	case "b2":
		s, err = object.NewB2(os.Getenv("B2_ENDPOINT"), os.Getenv("B2_ACCOUNT_ID"), os.Getenv("B2_APP_KEY"), "")
	case "space":
		s, err = object.NewSpace(os.Getenv("SPACE_ENDPOINT"), os.Getenv("SPACE_ACCESS_KEY"), os.Getenv("SPACE_SECRET_KEY"), "")
	case "bos":
		s, err = object.NewBOS(os.Getenv("BDCLOUD_ENDPOINT"), os.Getenv("BDCLOUD_ACCESS_KEY"), os.Getenv("BDCLOUD_SECRET_KEY"), "")
	case "sftp":
		s, err = object.NewSftp(os.Getenv("SFTP_HOST"), os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS"), "")
	case "obs":
		s, err = object.NewOBS(os.Getenv("HWCLOUD_ENDPOINT"), os.Getenv("HWCLOUD_ACCESS_KEY"), os.Getenv("HWCLOUD_SECRET_KEY"), "")
	case "nfs":
		s, err = object.NewNFSStore(os.Getenv("NFS_ADDR"), os.Getenv("NFS_ACCESS_KEY"), os.Getenv("NFS_SECRET_KEY"), "")
	case "oos":
		s, err = object.NewOOS(os.Getenv("OOS_ENDPOINT"), os.Getenv("OOS_ACCESS_KEY"), os.Getenv("OOS_SECRET_KEY"), "")
	case "minio":
		s, err = object.NewMinio(os.Getenv("MINIO_TEST_BUCKET"), os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), "")
	case "eos":
		s, err = object.NewEos(os.Getenv("EOS_ENDPOINT"), os.Getenv("EOS_ACCESS_KEY"), os.Getenv("EOS_SECRET_KEY"), "")
	case "wasabi":
		s, err = object.NewWasabi(os.Getenv("WASABI_ENDPOINT"), os.Getenv("WASABI_ACCESS_KEY"), os.Getenv("WASABI_SECRET_KEY"), "")
	case "scs":
		s, err = object.NewSCS(os.Getenv("SCS_ENDPOINT"), os.Getenv("SCS_ACCESS_KEY"), os.Getenv("SCS_SECRET_KEY"), "")
	case "ibmcos":
		s, err = object.NewIBMCOS(os.Getenv("IBMCOS_ENDPOINT"), os.Getenv("IBMCOS_ACCESS_KEY"), os.Getenv("IBMCOS_SECRET_KEY"), "")
	case "tos":
		s, err = object.NewTOS(os.Getenv("TOS_ENDPOINT"), os.Getenv("TOS_ACCESS_KEY"), os.Getenv("TOS_SECRET_KEY"), "")
	}
	if err != nil {
		logger.Fatalf("create storage err: %s", err)
	}
	testList(s)
}

func testList(s object.ObjectStorage) {
	s = object.WithPrefix(s, commPrefix)
	if commPrefix == "" {
		logger.Fatal("prefix is empty")
	}
	var ch = make(chan struct{}, parallel)
	if makeData {
		progress := utils.NewProgress(false)
		bar := progress.AddCountBar("make data", int64(1200*2000))
		start := time.Now()
		var wg sync.WaitGroup
		for i := 0; i < 1200; i++ {
			ch <- struct{}{}
			wg.Add(1)
			go func(id int) {
				defer func() {
					wg.Done()
					<-ch
				}()
				for j := 0; j < 2000; j++ {
					if err := s.Put(fmt.Sprintf("%d_dir/%d_file", i, j), bytes.NewReader([]byte("a"))); err != nil {
						logger.Fatal(err)
					}
					bar.Increment()
					time.Sleep(10 * time.Millisecond)
				}
			}(i)
		}
		wg.Wait()
		close(ch)
		bar.Done()
		progress.Done()
		logger.Infof("make data took %s", time.Since(start))
	}
	logger.Infof("Data is ready")
	var duration time.Duration
	for i := 0; i < 5; i++ {
		start := time.Now()
		var hasMore bool
		var objs []object.Object
		var token string
		for {
			var objs1 []object.Object
			var err error
			objs1, hasMore, token, err = object.ListV2(s, prefix, "", token, delimiter, 1000, true)
			if err != nil {
				logger.Fatal("list err", err)
			}
			objs = append(objs, objs1...)
			if !hasMore {
				break
			}
		}
		since := time.Since(start)
		logger.Infof("list %d took %s", i, since)
		duration += since
		logger.Infof(" %d list return %d results", i, len(objs))
	}
	logger.Infof("name=%s prefix=%s delimite= %s average list took %s", name, prefix, delimiter, duration/5)
}
