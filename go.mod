module github.com/juicedata/juicefs

go 1.20

require (
	cloud.google.com/go/compute/metadata v0.2.3
	cloud.google.com/go/storage v1.28.1
	github.com/Arvintian/scs-go-sdk v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.0.0
	github.com/DataDog/zstd v1.5.0
	github.com/IBM/ibm-cos-sdk-go v1.10.0
	github.com/agiledragon/gomonkey/v2 v2.6.0
	github.com/aliyun/aliyun-oss-go-sdk v2.2.7+incompatible
	github.com/aws/aws-sdk-go v1.44.262
	github.com/baidubce/bce-sdk-go v0.9.200
	github.com/billziss-gh/cgofuse v1.5.0
	github.com/ceph/go-ceph v0.18.0
	github.com/colinmarc/hdfs/v2 v2.3.0
	github.com/davies/groupcache v0.0.0-20230821031435-e4e8362f58e1
	github.com/dgraph-io/badger/v4 v4.2.0
	github.com/dustin/go-humanize v1.0.1
	github.com/erikdubbelboer/gspt v0.0.0-20210805194459-ce36a5128377
	github.com/go-http-utils/headers v0.0.0-20181008091004-fed159eddc2a
	github.com/go-sql-driver/mysql v1.7.1
	github.com/goccy/go-json v0.10.2
	github.com/gofrs/flock v0.8.1
	github.com/google/btree v1.1.2
	github.com/google/uuid v1.5.0
	github.com/grafana/pyroscope-go v1.0.4
	github.com/hanwen/go-fuse/v2 v2.1.1-0.20210611132105-24a1dfe6b4f8
	github.com/hashicorp/consul/api v1.15.2
	github.com/hashicorp/go-hclog v1.5.0
	github.com/huaweicloud/huaweicloud-sdk-go-obs v3.21.12+incompatible
	github.com/hungys/go-lz4 v0.0.0-20170805124057-19ff7f07f099
	github.com/jackc/pgx/v5 v5.5.4
	github.com/jcmturner/gokrb5/v8 v8.4.4
	github.com/json-iterator/go v1.1.12
	github.com/juicedata/godaemon v0.0.0-20210629045518-3da5144a127d
	github.com/juicedata/gogfapi v0.0.0-20241204082332-ecd102647f80
	github.com/juju/ratelimit v1.0.2
	github.com/ks3sdklib/aws-sdk-go v1.2.2
	github.com/l0wl3vel/bunny-storage-go-sdk v0.0.10
	github.com/mattn/go-isatty v0.0.18
	github.com/mattn/go-sqlite3 v1.14.24
	github.com/minio/cli v1.24.2
	github.com/minio/minio v0.0.0-20210206053228-97fe57bba92c
	github.com/minio/minio-go/v7 v7.0.11-0.20210302210017-6ae69c73ce78
	github.com/ncw/swift/v2 v2.0.1
	github.com/pingcap/log v1.1.1-0.20221015072633-39906604fb81
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.13.5
	github.com/pkg/xattr v0.4.9
	github.com/prometheus/client_golang v1.15.1
	github.com/prometheus/client_model v0.4.0
	github.com/prometheus/common v0.43.0
	github.com/qingstor/qingstor-sdk-go/v4 v4.4.0
	github.com/qiniu/go-sdk/v7 v7.15.0
	github.com/redis/go-redis/v9 v9.0.2
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.8.4
	github.com/studio-b12/gowebdav v0.0.0-20230203202212-3282f94193f2
	github.com/tencentyun/cos-go-sdk-v5 v0.7.45
	github.com/tikv/client-go/v2 v2.0.4
	github.com/upyun/go-sdk/v3 v3.0.4
	github.com/urfave/cli/v2 v2.19.3
	github.com/vbauerster/mpb/v7 v7.0.3
	github.com/viki-org/dnscache v0.0.0-20130720023526-c70c1f23c5d8
	github.com/vimeo/go-util v1.4.1
	github.com/vmware/go-nfs-client v0.0.0-20190605212624-d43b92724c1b
	github.com/volcengine/ve-tos-golang-sdk/v2 v2.7.0
	github.com/youmark/pkcs8 v0.0.0-20201027041543-1326539a0a0a
	go.etcd.io/etcd v3.3.27+incompatible
	go.etcd.io/etcd/client/v3 v3.5.9
	go.uber.org/automaxprocs v1.5.2
	go.uber.org/zap v1.20.0
	golang.org/x/crypto v0.31.0
	golang.org/x/net v0.33.0
	golang.org/x/oauth2 v0.7.0
	golang.org/x/sync v0.10.0
	golang.org/x/sys v0.28.0
	golang.org/x/term v0.27.0
	golang.org/x/text v0.21.0
	google.golang.org/api v0.114.0
	google.golang.org/protobuf v1.30.0
	gopkg.in/kothar/go-backblaze.v0 v0.0.0-20210124194846-35409b867216
	pgregory.net/rapid v0.5.3
	xorm.io/xorm v1.0.7
)

require (
	cloud.google.com/go v0.110.0 // indirect
	cloud.google.com/go/compute v1.19.1 // indirect
	cloud.google.com/go/iam v0.13.0 // indirect
	git.apache.org/thrift.git v0.13.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.1.1 // indirect
	github.com/Azure/go-ntlmssp v0.0.0-20200615164410-66371956d46c // indirect
	github.com/VividCortex/ewma v1.2.0 // indirect
	github.com/acarl005/stripansi v0.0.0-20180116102854-5a71ef0e047d // indirect
	github.com/alecthomas/participle v0.2.1 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/apple/foundationdb/bindings/go v0.0.0-20211207225159-47b9a81d1c10
	github.com/armon/go-metrics v0.3.10 // indirect
	github.com/bcicen/jstream v1.0.1 // indirect
	github.com/beevik/ntp v0.3.0 // indirect
	github.com/benbjohnson/clock v1.1.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cheggaaa/pb v1.0.29 // indirect
	github.com/clbanning/mxj v1.8.4 // indirect
	github.com/coredns/coredns v1.6.6 // indirect
	github.com/coreos/etcd v3.3.27+incompatible // indirect
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf // indirect
	github.com/coreos/go-systemd/v22 v22.3.2 // indirect
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/cznic/mathutil v0.0.0-20181122101859-297441e03548 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dchest/siphash v1.2.1 // indirect
	github.com/dgraph-io/ristretto v0.1.1 // indirect
	github.com/dgrijalva/jwt-go v3.2.0+incompatible // indirect
	github.com/dgryski/go-farm v0.0.0-20190423205320-6a90982ecee2 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/djherbis/atime v1.0.0 // indirect
	github.com/dswarbrick/smart v0.0.0-20190505152634-909a45200d6d // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/felixge/httpsnoop v1.0.1 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.1 // indirect
	github.com/go-ldap/ldap/v3 v3.2.4 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-resty/resty/v2 v2.11.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt v3.2.2+incompatible // indirect
	github.com/golang/glog v1.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/flatbuffers v1.12.1 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/google/readahead v0.0.0-20161222183148-eaceba169032 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.3 // indirect
	github.com/googleapis/gax-go/v2 v2.7.1 // indirect
	github.com/gorilla/handlers v1.5.1 // indirect
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/grafana/pyroscope-go/godeltaprof v0.1.8
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.1 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.0 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/serf v0.9.7 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/goidentity/v6 v6.0.1 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/klauspost/compress v1.17.8 // indirect
	github.com/klauspost/cpuid v1.3.1 // indirect
	github.com/klauspost/cpuid/v2 v2.2.7 // indirect
	github.com/klauspost/pgzip v1.2.5 // indirect
	github.com/klauspost/readahead v1.3.1 // indirect
	github.com/klauspost/reedsolomon v1.9.11 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-runewidth v0.0.13 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/miekg/dns v1.1.41 // indirect
	github.com/minio/highwayhash v1.0.2 // indirect
	github.com/minio/md5-simd v1.1.1 // indirect
	github.com/minio/selfupdate v0.3.1 // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/minio/simdjson-go v0.2.1 // indirect
	github.com/minio/sio v0.2.1 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.4.3 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/montanaflynn/stats v0.5.0 // indirect
	github.com/mozillazg/go-httpheader v0.2.1 // indirect
	github.com/ncw/directio v1.0.5 // indirect
	github.com/nxadm/tail v1.4.8 // indirect
	github.com/oliverisaac/shellescape v0.0.0-20220131224704-1b6c6b87b668
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pengsrc/go-shared v0.2.1-0.20190131101655-1999055a4a14 // indirect
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/pierrec/lz4 v2.5.2+incompatible // indirect
	github.com/pingcap/errors v0.11.5-0.20211224045212-9687c2b0f87c // indirect
	github.com/pingcap/failpoint v0.0.0-20210918120811-547c13e3eb00 // indirect
	github.com/pingcap/kvproto v0.0.0-20221129023506-621ec37aac7a // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/pquerna/ffjson v0.0.0-20190930134022-aa0246cd15f7 // indirect
	github.com/prometheus/procfs v0.11.0 // indirect
	github.com/rasky/go-xdr v0.0.0-20170124162913-1a41d1a06c93 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20200410134404-eec4a21b6bb0 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/rjeczalik/notify v0.9.3 // indirect
	github.com/rs/cors v1.7.0 // indirect
	github.com/rs/xid v1.2.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/secure-io/sio-go v0.3.1 // indirect
	github.com/shirou/gopsutil/v3 v3.23.11 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/smartystreets/goconvey v1.7.2 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/stathat/consistent v1.0.0 // indirect
	github.com/syndtr/goleveldb v1.0.0 // indirect
	github.com/tiancaiamao/gp v0.0.0-20221230034425-4025bc8a4d4a // indirect
	github.com/tidwall/gjson v1.9.3 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tidwall/sjson v1.0.4 // indirect
	github.com/tikv/pd/client v0.0.0-20221031025758-80f0d8ca4d07 // indirect
	github.com/tinylib/msgp v1.1.6 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/twmb/murmur3 v1.1.8
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.52.0 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/willf/bitset v1.1.11 // indirect
	github.com/willf/bloom v2.0.3+incompatible // indirect
	github.com/xrash/smetrics v0.0.0-20201216005158-039620a65673 // indirect
	github.com/yusufpapurcu/wmi v1.2.3 // indirect
	go.etcd.io/etcd/api/v3 v3.5.9 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.5.9 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.uber.org/atomic v1.10.0 // indirect
	go.uber.org/multierr v1.7.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 // indirect
	google.golang.org/grpc v1.56.3 // indirect
	gopkg.in/ini.v1 v1.57.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	xorm.io/builder v0.3.7 // indirect
)

replace github.com/minio/minio v0.0.0-20210206053228-97fe57bba92c => github.com/juicedata/minio v0.0.0-20240912134328-6a47725331ab

replace github.com/hanwen/go-fuse/v2 v2.1.1-0.20210611132105-24a1dfe6b4f8 => github.com/juicedata/go-fuse/v2 v2.1.1-0.20241120081217-1d367e67855d

replace github.com/dgrijalva/jwt-go v3.2.0+incompatible => github.com/golang-jwt/jwt v3.2.1+incompatible

replace github.com/vbauerster/mpb/v7 v7.0.3 => github.com/juicedata/mpb/v7 v7.0.4-0.20231024073412-2b8d31be510b

replace xorm.io/xorm v1.0.7 => gitea.com/davies/xorm v1.0.8-0.20220528043536-552d84d1b34a

replace github.com/huaweicloud/huaweicloud-sdk-go-obs v3.21.12+incompatible => github.com/juicedata/huaweicloud-sdk-go-obs v3.22.12-0.20230228031208-386e87b5c091+incompatible

replace github.com/urfave/cli/v2 v2.19.3 => github.com/juicedata/cli/v2 v2.19.4-0.20230605075551-9c9c5c0dce83

replace github.com/vmware/go-nfs-client v0.0.0-20190605212624-d43b92724c1b => github.com/juicedata/go-nfs-client v0.0.0-20231018052507-dbca444fa7e8
