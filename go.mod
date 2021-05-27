module github.com/juicedata/juicefs

go 1.14

require (
	cloud.google.com/go v0.39.0
	github.com/Arvintian/scs-go-sdk v1.1.0
	github.com/Azure/azure-sdk-for-go v11.1.1-beta+incompatible
	github.com/Azure/go-autorest/autorest v0.11.17 // indirect
	github.com/DataDog/zstd v1.4.5
	github.com/IBM/ibm-cos-sdk-go v1.6.0
	github.com/NetEase-Object-Storage/nos-golang-sdk v0.0.0-20171031020902-cc8892cb2b05
	github.com/aliyun/aliyun-oss-go-sdk v2.1.0+incompatible
	github.com/aws/aws-sdk-go v1.35.20
	github.com/baidubce/bce-sdk-go v0.9.47
	github.com/billziss-gh/cgofuse v1.4.0
	github.com/ceph/go-ceph v0.4.0
	github.com/colinmarc/hdfs/v2 v2.2.0
	github.com/go-ini/ini v1.62.0 // indirect
	github.com/go-redis/redis/v8 v8.4.0
	github.com/go-sql-driver/mysql v1.5.0
	github.com/gonutz/w32/v2 v2.2.0
	github.com/google/gops v0.3.13
	github.com/google/uuid v1.1.2
	github.com/hanwen/go-fuse/v2 v2.0.4-0.20210104155004-09a3c381714c
	github.com/huaweicloud/huaweicloud-sdk-go-obs v0.0.0-20190127152727-3a9e1f8023d5
	github.com/hungys/go-lz4 v0.0.0-20170805124057-19ff7f07f099
	github.com/jcmturner/gokrb5/v8 v8.4.2
	github.com/juicedata/godaemon v0.0.0-20210118074000-659b6681b236
	github.com/juju/ratelimit v1.0.1
	github.com/kr/fs v0.1.0 // indirect
	github.com/ks3sdklib/aws-sdk-go v0.0.0-20180820074416-dafab05ad142
	github.com/kurin/blazer v0.2.1
	github.com/mattn/go-isatty v0.0.12
	github.com/mattn/go-sqlite3 v1.14.0
	github.com/minio/cli v1.22.0
	github.com/minio/minio v0.0.0-20210206053228-97fe57bba92c
	github.com/minio/minio-go v6.0.14+incompatible
	github.com/ncw/swift v1.0.53
	github.com/pengsrc/go-shared v0.2.0 // indirect
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.10.0
	github.com/prometheus/client_golang v1.9.0
	github.com/prometheus/client_model v0.2.0
	github.com/qiniu/api.v7/v7 v7.8.0
	github.com/satori/go.uuid v1.2.0
	github.com/satori/uuid v1.2.0 // indirect
	github.com/shirou/gopsutil v3.21.3+incompatible // indirect
	github.com/sirupsen/logrus v1.7.0
	github.com/tencentyun/cos-go-sdk-v5 v0.7.8
	github.com/tklauser/go-sysconf v0.3.6 // indirect
	github.com/upyun/go-sdk/v3 v3.0.2
	github.com/urfave/cli/v2 v2.3.0
	github.com/viki-org/dnscache v0.0.0-20130720023526-c70c1f23c5d8
	github.com/yunify/qingstor-sdk-go v2.2.15+incompatible
	golang.org/x/crypto v0.0.0-20201124201722-c8d3bf9c5392
	golang.org/x/net v0.0.0-20201216054612-986b41b23924
	golang.org/x/oauth2 v0.0.0-20190517181255-950ef44c6e07
	golang.org/x/sys v0.0.0-20210316164454-77fc1eacc6aa
	golang.org/x/term v0.0.0-20201126162022-7de9c90e9dd1
	google.golang.org/api v0.5.0
	xorm.io/xorm v1.0.7
)

replace github.com/minio/minio v0.0.0-20210206053228-97fe57bba92c => github.com/juicedata/minio v0.0.0-20210222051636-e7cabdf948f4
