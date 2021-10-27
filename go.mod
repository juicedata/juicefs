module github.com/juicedata/juicefs

go 1.15

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
	github.com/baiyubin/aliyun-sts-go-sdk v0.0.0-20180326062324-cfa1a18b161f // indirect
	github.com/billziss-gh/cgofuse v1.4.0
	github.com/ceph/go-ceph v0.4.0
	github.com/colinmarc/hdfs/v2 v2.2.0
	github.com/deckarep/golang-set v1.7.1 // indirect
	github.com/dnaeon/go-vcr v1.2.0 // indirect
	github.com/emersion/go-webdav v0.3.0
	github.com/go-redis/redis/v8 v8.4.0
	github.com/go-sql-driver/mysql v1.5.0
	github.com/golang-jwt/jwt v3.2.2+incompatible // indirect
	github.com/google/btree v1.0.1
	github.com/google/gops v0.3.13
	github.com/google/readahead v0.0.0-20161222183148-eaceba169032 // indirect
	github.com/google/uuid v1.1.2
	github.com/hanwen/go-fuse/v2 v2.1.1-0.20210611132105-24a1dfe6b4f8
	github.com/huaweicloud/huaweicloud-sdk-go-obs v3.21.1+incompatible
	github.com/hungys/go-lz4 v0.0.0-20170805124057-19ff7f07f099
	github.com/jcmturner/gokrb5/v8 v8.4.2
	github.com/juicedata/godaemon v0.0.0-20210629045518-3da5144a127d
	github.com/juju/ratelimit v1.0.1
	github.com/kr/fs v0.1.0 // indirect
	github.com/ks3sdklib/aws-sdk-go v1.0.12
	github.com/lib/pq v1.8.0
	github.com/mattn/go-isatty v0.0.12
	github.com/mattn/go-sqlite3 v2.0.1+incompatible
	github.com/minio/cli v1.22.0
	github.com/minio/minio v0.0.0-20210206053228-97fe57bba92c
	github.com/minio/minio-go v6.0.14+incompatible
	github.com/nats-io/nats-server/v2 v2.6.2 // indirect
	github.com/ncw/swift v1.0.53
	github.com/pengsrc/go-shared v0.2.0 // indirect
	github.com/pingcap/log v0.0.0-20210317133921-96f4fcab92a4
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.10.0
	github.com/pquerna/ffjson v0.0.0-20190930134022-aa0246cd15f7 // indirect
	github.com/prometheus/client_golang v1.9.0
	github.com/prometheus/client_model v0.2.0
	github.com/qiniu/api.v7/v7 v7.8.0
	github.com/satori/go.uuid v1.2.0
	github.com/satori/uuid v1.2.0 // indirect
	github.com/shirou/gopsutil v3.21.3+incompatible // indirect
	github.com/sirupsen/logrus v1.7.0
	github.com/tencentyun/cos-go-sdk-v5 v0.7.8
	github.com/tidwall/gjson v1.9.3 // indirect
	github.com/tikv/client-go/v2 v2.0.0-alpha.0.20210709052506-aadf3cf62721
	github.com/tklauser/go-sysconf v0.3.6 // indirect
	github.com/upyun/go-sdk/v3 v3.0.2
	github.com/urfave/cli/v2 v2.3.0
	github.com/vbauerster/mpb/v7 v7.0.3
	github.com/viki-org/dnscache v0.0.0-20130720023526-c70c1f23c5d8
	github.com/yunify/qingstor-sdk-go v2.2.15+incompatible
	golang.org/x/crypto v0.0.0-20210616213533-5ff15b29337e
	golang.org/x/net v0.0.0-20210405180319-a5a99cb37ef4
	golang.org/x/oauth2 v0.0.0-20190517181255-950ef44c6e07
	golang.org/x/sys v0.0.0-20210823070655-63515b42dcdf
	golang.org/x/term v0.0.0-20201126162022-7de9c90e9dd1
	google.golang.org/api v0.5.0
	gopkg.in/kothar/go-backblaze.v0 v0.0.0-20210124194846-35409b867216
	xorm.io/xorm v1.0.7
)

replace github.com/minio/minio v0.0.0-20210206053228-97fe57bba92c => github.com/juicedata/minio v0.0.0-20210222051636-e7cabdf948f4

replace github.com/hanwen/go-fuse/v2 v2.1.1-0.20210611132105-24a1dfe6b4f8 => github.com/juicedata/go-fuse/v2 v2.1.1-0.20210926080226-cfe1ec802a7f

replace github.com/golang-jwt/jwt v3.2.2+incompatible => github.com/dgrijalva/jwt-go v3.2.0+incompatible

replace github.com/dgrijalva/jwt-go v3.2.0+incompatible => github.com/golang-jwt/jwt v3.2.2+incompatible
