# How to Setup Object Storage

By reading [JuiceFS Technical Architecture](architecture.md) and [How JuiceFS Store Files](how_juicefs_store_files.md), you will understand that JuiceFS is designed to store data and metadata independently. Generally , the data is stored in the cloud storage based on object storage, and the metadata corresponding to the data is stored in an independent database.

## Storage setting options

When creating a JuiceFS file system, setting up data storage generally involves the following options:

- `--storage` Specify the storage service to be used by the file system, e.g. `--storage s3`
- `--bucket` Specify the bucket endpoint of the object storage in a specific format, e.g. `--bucket https://myjuicefs.s3.us-east-2.amazonaws.com`
- `--access-key` and `--secret-key` is the authentication key used when accessing the object storage service. You need to create it on the corresponding cloud platform.

For example, the following command uses Amazon S3 object storage to create a file system:

```shell
$ juicefs format --storage s3 \
	--bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
	--access-key abcdefghijklmn \
	--secret-key nmlkjihgfedAcBdEfg \
	redis://192.168.1.6/1 \
	my-juice
```

Similarly, you can adjust the parameters and use almost all public/private cloud object storage services to create a file system.

## Access Key and Secret Key

Generally, the object storage service uses `access key` and `secret key` to verify user identity. When creating a file system, in addition to using the two options `--access-key` and `--secret-key` to explicitly set.  You can also set it through two environment variables `ACCESS_KEY` and `SECRET_KEY`.

Public cloud provider usually allow user create IAM (Identity and Access Management) role (e.g. [AWS IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html)) or similar thing (e.g. [Alibaba Cloud RAM role](https://help.aliyun.com/document_detail/93689.html)), then assign the role to VM instance. If your VM instance already have permission to access object storage, then you could omit `--access-key` and `--secret-key` options.

## Supported Object Storage

The following table lists the object storage services supported by JuiceFS. Click the name to view the setting details:

> If the object storage service you want is not in the list, please submit a request [issue](https://github.com/juicedata/juicefs/issues).

| Name                                                      | Value      |
| --------------------------------------------------------- | ---------- |
| [Amazon S3](#aws-s3)                                      | `s3`       |
| [Google Cloud Storage](#google-gs)                        | `gs`       |
| [Azure Blob Storage](#azure-wasb)                         | `wasb`     |
| [Backblaze B2 Cloud Storage](#backblaze-b2)               | `b2`       |
| [IBM Cloud Object Storage](#ibm-cos)                      | `ibmcos`   |
| [Scaleway Object Storage](#scaleway)                      | `scw`      |
| [DigitalOcean Spaces Object Storage](#do-spaces)          | `space`    |
| [Wasabi Cloud Object Storage](#wasabi)                    | `wasabi`   |
| [Storj DCS](#storj-dcs)                                   | `s3`       |
| [Vultr Object Storage](#vultr)                            | `s3`       |
| [Alibaba Cloud Object Storage Service](#aliyun-oss)       | `oss`      |
| [Tencent Cloud Object Storage](#qcloud-cos)               | `cos`      |
| [Huawei Cloud Object Storage Service](#huawei-obs)        | `obs`      |
| [Baidu Object Storage](#baidu-bos)                        | `bos`      |
| [Kingsoft Cloud Standard Storage Service](#kingsoft-ks3)  | `ks3`      |
| [Meituan Storage Service](#meituan-mss)                   | `mss`      |
| [NetEase Object Storage](#163-nos)                        | `nos`      |
| [QingStor Object Storage](#QingStor)                      | `qingstor` |
| [Qiniu Cloud Object Storage](#qiniu)                      | `qiniu`    |
| [Sina Cloud Storage](#sina-scs)                           | `scs`      |
| [CTYun Object-Oriented Storage](#ctyun-oos)               | `oos`      |
| [ECloud (China Mobile Cloud) Object Storage](#ecloud-eos) | `eos`      |
| [SpeedyCloud Object Storage](#speedycloud)                | `speedy`   |
| [UCloud US3](#ucloud-us3)                                 | `ufile`    |
| [Ceph RADOS](#ceph-rados)                                 | `ceph`     |
| [Ceph Object Gateway (RGW)](#ceph-rgw)                    | `s3`       |
| [Swift](#swift)                                           | `swift`    |
| [MinIO](#minio)                                           | `minio`    |
| [WebDAV](#webdav)                                         | `webdav`   |
| [HDFS](#hdfs)                                             | `hdfs`     |
| [Redis](#redis)                                           | `redis`    |
| [TiKV](#tikv)                                             | `tikv`     |
| [Local disk](#local)                                      | `file`     |

## S3 <span id='aws-s3'></span>

S3 supports [two style endpoint URI](https://docs.aws.amazon.com/AmazonS3/latest/dev/VirtualHosting.html): virtual hosted-style and path-style. The difference between them is:

- Virtual hosted-style: `https://<bucket>.s3.<region>.amazonaws.com`
- Path-style: `https://s3.<region>.amazonaws.com/<bucket>`

The `<region>` should be replaced with specific region code, e.g. the region code of US East (N. Virginia) is `us-east-1`. You could find all available regions at [here](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-regions-availability-zones.html#concepts-available-regions).

> **Note**: For AWS China user, you need add `.cn` to the host, i.e. `amazonaws.com.cn`. And check [this document](https://docs.amazonaws.cn/en_us/aws/latest/userguide/endpoints-arns.html) to know your region code.

JuiceFS supports both types of endpoint since v0.12 (before v0.12, only virtual hosted-style were supported). So when you format a volume, the `--bucket` option can be either virtual hosted-style URI or path-style URI. For example:

```bash
# virtual hosted-style
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    ... \
    localhost test
```

```bash
# path-style
$ ./juicefs format \
    --storage s3 \
    --bucket https://s3.<region>.amazonaws.com/<bucket> \
    ... \
    localhost test
```

You can also use S3 storage type to connect with S3-compatible storage. For example:

```bash
# virtual hosted-style
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

```bash
# path-style
$ ./juicefs format \
    --storage s3 \
    --bucket https://<endpoint>/<bucket> \
    ... \
    localhost test
```



## Google Cloud Storage <span id='google-gs'></span>

Because Google Cloud doesn't have access key and secret key, the `--access-key` and `--secret-key` options can be omitted. Please follow Google Cloud document to know how [authentication](https://cloud.google.com/docs/authentication) and [authorization](https://cloud.google.com/iam/docs/overview) work. Typically, when you running within Google Cloud, you already have permission to access the storage.

And because bucket name is [globally unique](https://cloud.google.com/storage/docs/naming-buckets#considerations), when you specify the `--bucket` option could just provide its name. For example:

```bash
$ ./juicefs format \
    --storage gs \
    --bucket gs://<bucket> \
    ... \
    localhost test
```

## Azure Blob Storage <span id='azure-wasb'></span>

Besides provide authorization information through `--access-key` and `--secret-key` options, you could also create a [connection string](https://docs.microsoft.com/en-us/azure/storage/common/storage-configure-connection-string) and set `AZURE_STORAGE_CONNECTION_STRING` environment variable. For example:

```bash
# Use connection string
$ export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=https;AccountName=XXX;AccountKey=XXX;EndpointSuffix=core.windows.net"
$ ./juicefs format \
    --storage wasb \
    --bucket https://<container> \
    ... \
    localhost test
```

> **Note**: For Azure China user, the value of `EndpointSuffix` is `core.chinacloudapi.cn`.

## Backblaze B2 Cloud Storage <span id='backblaze-b2'></span>

You need first creating [application key](https://www.backblaze.com/b2/docs/application_keys.html). The "Application Key ID" and "Application Key" are the equivalent of access key and secret key respectively.

The `--bucket` option could only have bucket name. For example:

```bash
$ ./juicefs format \
    --storage b2 \
    --bucket https://<bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    localhost test
```

## IBM Cloud Object Storage <span id='ibm-cos'></span>

You need first creating [API key](https://cloud.ibm.com/docs/account?topic=account-manapikey) and retrieving [instance ID](https://cloud.ibm.com/docs/key-protect?topic=key-protect-retrieve-instance-ID). The "API key" and "instance ID" are the equivalent of access key and secret key respectively.

IBM Cloud Object Storage provides [multiple endpoints](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints) for each region, depends on your network (e.g. public or private network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage ibmcos \
    --bucket https://<bucket>.<endpoint> \
    --access-key <API-key> \
    --secret-key <instance-ID> \
    ... \
    localhost test
```

## Scaleway Object Storage <span id='scaleway'></span>

Please follow [this document](https://www.scaleway.com/en/docs/generate-api-keys) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.scw.cloud`, replace `<region>` with specific region code, e.g. the region code of "Amsterdam, The Netherlands" is `nl-ams`. You could find all available regions at [here](https://www.scaleway.com/en/docs/object-storage-feature/#-Core-Concepts). For example:

```bash
$ ./juicefs format \
    --storage scw \
    --bucket https://<bucket>.s3.<region>.scw.cloud \
    ... \
    localhost test
```

## DigitalOcean Spaces Object Storage <span id='do-spaces'></span>

Please follow [this document](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<space-name>.<region>.digitaloceanspaces.com`, replace `<region>` with specific region code, e.g. `nyc3`. You could find all available regions at [here](https://www.digitalocean.com/docs/spaces/#regional-availability). For example:

```bash
$ ./juicefs format \
    --storage space \
    --bucket https://<space-name>.<region>.digitaloceanspaces.com \
    ... \
    localhost test
```

## Wasabi Cloud Object Storage <span id='wasabi'></span>

Please follow [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360019677192-Creating-a-Root-Access-Key-and-Secret-Key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.wasabisys.com`, replace `<region>` with specific region code, e.g. the region code of US East 1 (N. Virginia) is `us-east-1`. You could find all available regions at [here](https://wasabi-support.zendesk.com/hc/en-us/articles/360015106031-What-are-the-service-URLs-for-Wasabi-s-different-regions-). For example:

```bash
$ ./juicefs format \
    --storage wasabi \
    --bucket https://<bucket>.s3.<region>.wasabisys.com \
    ... \
    localhost test
```

***Note: For Tokyo (ap-northeast-1) region user, see [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360039372392-How-do-I-access-the-Wasabi-Tokyo-ap-northeast-1-storage-region-) to learn how to get appropriate endpoint URI.***

## Storj DCS <span id='storj-dcs'></span>

Storj DCS is an S3-compatible storage, just use `s3` for `--storage` option. The setting format of the `--bucket` option is `https://gateway.<region>.storjshare.io/<bucket>`, please replace `<region>` with the storage region you actually use. There are currently three avaliable regions: `us1`, `ap1` and `eu1`. For example:

```shell
$ juicefs format \
	--storage s3 \
	--bucket https://gateway.<region>.storjshare.io/<bucket> \
	--access-key <your-access-key> \
	--secret-key <your-sceret-key> \
	redis://localhost/1 my-jfs
```

Please refer to [this document](https://docs.storj.io/api-reference/s3-compatible-gateway) to learn how to create access key and secret key.

## Vultr Object Storage <span id='vultr'></span>

Vultr Object Storage is an S3-compatible storage, use `s3` for `--storage` option. The `--bucket` option is `https://<bucket>.<region>.vultrobjects.com/`. Currently there is one region available: `ewr1`. For example:

```shell
$ juicefs format \
	--storage s3 \
	--bucket https://<bucket>.ewr1.vultrobjects.com/ \
	--access-key <your-access-key> \
	--secret-key <your-sceret-key> \
	redis://localhost/1 my-jfs
```

Please find the access and secret keys for object storage [in the customer portal](https://my.vultr.com/objectstorage/).

## Alibaba Cloud Object Storage Service <span id='aliyun-oss'></span>

Please follow [this document](https://help.aliyun.com/document_detail/38738.html) to learn how to get access key and secret key. And if you already created [RAM role](https://help.aliyun.com/document_detail/93689.html) and assign it to VM instance, you could omit `--access-key` and `--secret-key` options. Alibaba Cloud also supports use [Security Token Service (STS)](https://help.aliyun.com/document_detail/100624.html) to authorize temporary access to OSS. If you wanna use STS, you should omit `--access-key` and `--secret-key` options and set `ALICLOUD_ACCESS_KEY_ID`, `ALICLOUD_ACCESS_KEY_SECRET`, `SECURITY_TOKEN` environment variables instead, for example:

```bash
# Use Security Token Service (STS)
$ export ALICLOUD_ACCESS_KEY_ID=XXX
$ export ALICLOUD_ACCESS_KEY_SECRET=XXX
$ export SECURITY_TOKEN=XXX
$ ./juicefs format \
    --storage oss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

OSS provides [multiple endpoints](https://help.aliyun.com/document_detail/31834.html) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. When you running within Alibaba Cloud, you could omit `<endpoint>` in `--bucket` option. JuiceFS will choose appropriate endpoint automatically. For example:

```bash
# Running within Alibaba Cloud
$ ./juicefs format \
    --storage oss \
    --bucket https://<bucket> \
    ... \
    localhost test
```

## Tencent Cloud Object Storage <span id='qcloud-cos'></span>

The naming rule of bucket in Tencent Cloud is `<bucket>-<APPID>`, so you must append `APPID` to the bucket name. Please follow [this document](https://cloud.tencent.com/document/product/436/13312) to learn how to get `APPID`.

The full format of `--bucket` option is `https://<bucket>-<APPID>.cos.<region>.myqcloud.com`, replace `<region>` with specific region code, e.g. the region code of Shanghai is `ap-shanghai`. You could find all available regions at [here](https://cloud.tencent.com/document/product/436/6224). For example:

```bash
$ ./juicefs format \
    --storage cos \
    --bucket https://<bucket>-<APPID>.cos.<region>.myqcloud.com \
    ... \
    localhost test
```

When you running within Tencent Cloud, you could omit `.cos.<region>.myqcloud.com` part in `--bucket` option. JuiceFS will choose appropriate endpoint automatically. For example:

```bash
# Running within Tencent Cloud
$ ./juicefs format \
    --storage cos \
    --bucket https://<bucket>-<APPID> \
    ... \
    localhost test
```

## Huawei Cloud Object Storage Service <span id='huawei-obs'></span>

Please follow [this document](https://support.huaweicloud.com/usermanual-ca/zh-cn_topic_0046606340.html) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.obs.<region>.myhuaweicloud.com`, replace `<region>` with specific region code, e.g. the region code of Beijing 1 is `cn-north-1`. You could find all available regions at [here](https://developer.huaweicloud.com/endpoint?OBS). For example:

```bash
$ ./juicefs format \
    --storage obs \
    --bucket https://<bucket>.obs.<region>.myhuaweicloud.com \
    ... \
    localhost test
```

When you running within Huawei Cloud, you could omit `.obs.<region>.myhuaweicloud.com` part in `--bucket` option. JuiceFS will choose appropriate endpoint automatically. For example:

```bash
# Running within Huawei Cloud
$ ./juicefs format \
    --storage obs \
    --bucket https://<bucket> \
    ... \
    localhost test
```

## Baidu Object Storage <span id='baidu-bos'></span>

Please follow [this document](https://cloud.baidu.com/doc/Reference/s/9jwvz2egb) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.bcebos.com`, replace `<region>` with specific region code, e.g. the region code of Beijing is `bj`. You could find all available regions at [here](https://cloud.baidu.com/doc/BOS/s/Ck1rk80hn#%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D%EF%BC%88endpoint%EF%BC%89). For example:

```bash
$ ./juicefs format \
    --storage bos \
    --bucket https://<bucket>.<region>.bcebos.com \
    ... \
    localhost test
```

When you running within Baidu Cloud, you could omit `.<region>.bcebos.com` part in `--bucket` option. JuiceFS will choose appropriate endpoint automatically. For example:

```bash
# Running within Baidu Cloud
$ ./juicefs format \
    --storage bos \
    --bucket https://<bucket> \
    ... \
    localhost test
```

## Kingsoft Cloud Standard Storage Service <span id='kingsoft-ks3'></span>

Please follow [this document](https://docs.ksyun.com/documents/1386) to learn how to get access key and secret key.

KS3 provides [multiple endpoints](https://docs.ksyun.com/documents/6761) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage ks3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Meituan Storage Service <span id='meituan-mss'></span>

Please follow [this document](https://www.mtyun.com/doc/api/mss/mss/fang-wen-kong-zhi) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<endpoint>`, replace `<endpoint>` with specific value, e.g. `mtmss.com`. You could find all available endpoints at [here](https://www.mtyun.com/doc/products/storage/mss/index#%E5%8F%AF%E7%94%A8%E5%8C%BA%E5%9F%9F). For example:

```bash
$ ./juicefs format \
    --storage mss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## NetEase Object Storage <span id='163-nos'></span>

Please follow [this document](https://www.163yun.com/help/documents/55485278220111872) to learn how to get access key and secret key.

NOS provides [multiple endpoints](https://www.163yun.com/help/documents/67078583131230208) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage nos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## QingStor Object Storage <span id='QingStor'></span>

Please follow [this document](https://docs.qingcloud.com/qingstor/api/common/signature.html#%E8%8E%B7%E5%8F%96-access-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.qingstor.com`, replace `<region>` with specific region code, e.g. the region code of Beijing 3-A is `pek3a`. You could find all available regions at [here](https://docs.qingcloud.com/qingstor/#%E5%8C%BA%E5%9F%9F%E5%8F%8A%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D). For example:

```bash
$ ./juicefs format \
    --storage qingstor \
    --bucket https://<bucket>.<region>.qingstor.com \
    ... \
    localhost test
```

## Qiniu Cloud Object Storage <span id='qiniu'></span>

Please follow [this document](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3-<region>.qiniucs.com`, replace `<region>` with specific region code, e.g. the region code of China East is `cn-east-1`. You could find all available regions at [here](https://developer.qiniu.com/kodo/4088/s3-access-domainname). For example:

```bash
$ ./juicefs format \
    --storage qiniu \
    --bucket https://<bucket>.s3-<region>.qiniucs.com \
    ... \
    localhost test
```

## Sina Cloud Storage <span id='sina-scs'></span>

Please follow [this document](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.stor.sinaapp.com`. For example:

```bash
$ ./juicefs format \
    --storage scs \
    --bucket https://<bucket>.stor.sinaapp.com \
    ... \
    localhost test
```

## CTYun Object-Oriented Storage <span id='ctyun-oos'></span>

Please follow [this document](https://www.ctyun.cn/help2/10000101/10473683) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.oss-<region>.ctyunapi.cn`, replace `<region>` with specific region code, e.g. the region code of Chengdu is `sccd`. You could find all available regions at [here](https://www.ctyun.cn/help2/10000101/10474062). For example:

```bash
$ ./juicefs format \
    --storage oos \
    --bucket https://<bucket>.oss-<region>.ctyunapi.cn \
    ... \
    localhost test
```

## ECloud (China Mobile Cloud) Object Storage <span id='ecloud-eos'></span>

Please follow [this document](https://ecloud.10086.cn/op-help-center/doc/article/24501) to learn how to get access key and secret key.

ECloud Object Storage provides [multiple endpoints](https://ecloud.10086.cn/op-help-center/doc/article/40956) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage eos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## SpeedyCloud Object Storage <span id='speedycloud'></span>

Writing ...

## UCloud US3 <span id='ucloud-us3'></span>

Please follow [this document](https://docs.ucloud.cn/uai-censor/access/key) to learn how to get access key and secret key.

US3 (formerly UFile) provides [multiple endpoints](https://docs.ucloud.cn/ufile/introduction/region) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage ufile \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Ceph RADOS <span id='ceph-rados'></span>

The [Ceph Storage Cluster](https://docs.ceph.com/en/latest/rados) has a messaging layer protocol that enables clients to interact with a Ceph Monitor and a Ceph OSD Daemon. The `librados` API enables you to interact with the two types of daemons:

- The [Ceph Monitor](https://docs.ceph.com/en/latest/rados/configuration/common/#monitors), which maintains a master copy of the cluster map.
- The [Ceph OSD Daemon (OSD)](https://docs.ceph.com/en/latest/rados/configuration/common/#osds), which stores data as objects on a storage node.

JuiceFS supports the use of native Ceph APIs based on `librados`. You need install `librados` library and build `juicefs` binary separately.

First installing `librados`:

```bash
# Debian based system
$ sudo apt-get install librados-dev

# RPM based system
$ sudo yum install librados-devel
```

Then compile JuiceFS for Ceph (ensure you have Go 1.15+ and GCC 5.4+):

```bash
$ make juicefs.ceph
```

The `--bucket` option format is `ceph://<pool-name>`. A [pool](https://docs.ceph.com/en/latest/rados/operations/pools) is logical partition for storing objects. You may need first creating a pool. The value of `--access-key` option is Ceph cluster name, the default cluster name is `ceph`. The value of `--secret-key` option is [Ceph client user name](https://docs.ceph.com/en/latest/rados/operations/user-management), the default user name is `client.admin`.

For connect to Ceph Monitor, `librados` will read Ceph configuration file by search default locations and the first found is used. The locations are:

- `CEPH_CONF` environment variable
- `/etc/ceph/ceph.conf`
- `~/.ceph/config`
- `ceph.conf` in the current working directory

The example command is:

```bash
$ ./juicefs.ceph format \
    --storage ceph \
    --bucket ceph://<pool-name> \
    --access-key <cluster-name> \
    --secret-key <user-name> \
    ... \
    localhost test
```

## Ceph Object Gateway (RGW) <span id='ceph-rgw'></span>

[Ceph Object Gateway](https://ceph.io/ceph-storage/object-storage) is an object storage interface built on top of `librados` to provide applications with a RESTful gateway to Ceph Storage Clusters. Ceph Object Gateway supports S3-compatible interface, so we could set `--storage` to `s3` directly.

The `--bucket` option format is `http://<bucket>.<endpoint>` (virtual hosted-style). For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket http://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Swift <span id='swift'></span>

[OpenStack Swift](https://github.com/openstack/swift) is a distributed object storage system designed to scale from a single machine to thousands of servers. Swift is optimized for multi-tenancy and high concurrency. Swift is ideal for backups, web and mobile content, and any other unstructured data that can grow without bound.

The `--bucket` option format is `http://<container>.<endpoint>`. A container defines a namespace for objects. **Currently, JuiceFS only supports [Swift V1 authentication](https://www.swiftstack.com/docs/cookbooks/swift_usage/auth.html).** The value of `--access-key` option is username. The value of `--secret-key` option is password. For example:

```bash
$ ./juicefs format \
    --storage swift \
    --bucket http://<container>.<endpoint> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    localhost test
```

## MinIO <span id='minio'></span>

[MinIO](https://min.io) is an open source high performance object storage. It is API compatible with Amazon S3. You need set `--storage` option to `minio`. Currently, JuiceFS only supports path-style URI when use MinIO storage. For example (`<endpoint>` may looks like `1.2.3.4:9000`):

```bash
$ ./juicefs format \
    --storage minio \
    --bucket http://<endpoint>/<bucket> \
    ... \
    localhost test
```

## WebDAV <span id='webdav'></span>

[WebDAV](https://en.wikipedia.org/wiki/WebDAV) is an extension of the Hypertext Transfer Protocol (HTTP)
that facilitates collaborative editing and management of documents stored on the WWW server among users.
Starting from JuiceFS v0.15+, for a storage that speaks WebDAV, JuiceFS can use it as the data store.

You need set `--storage` to `webdav`, and `--bucket` to the endpoint of WebDAV. If basic authorization is enable, username and password should be provided as `--access-key` and `--secret-key`, for example:

```bash
$ ./juicefs format \
    --storage webdav \
    --bucket http://<endpoint>/ \
    --access-key <username> \
    --secret-key <password> \
    localhost test
```

## HDFS <span id='hdfs'></span>

[HDFS](https://hadoop.apache.org) is the file system for Hadoop, which can be used as the object store for JuiceFS. When HDFS is used, `--access-key` can be used to specify the `username`, and `hdfs` is usually the default superuser. For example:

```bash
$ ./juicefs format \
    --storage hdfs \
    --bucket namenode1:8020 \
    --access-key hdfs \
    localhost test
```

When the `--access-key` is not specified during formatting, JuiceFS will use the current user of `juicefs mount` or Hadoop SDK to access HDFS. It will hang and fail with IO error eventually, if the current user don't have enough permission to read/write the blocks in HDFS.

JuiceFS will try to load configurations for HDFS client based on `$HADOOP_CONF_DIR` or `$HADOOP_HOME`. If an empty value is provided to `--bucket`, the default HDFS found in Hadoop configurations will be used.

For HA cluster, the addresses of NameNodes can be specified together like this: `--bucket=namenode1:port,namenode2:port`.

## Redis

[Redis](https://redis.io) is an open source, in-memory data structure store, used as a database, cache, and message broker. In addition to using Redis as the metadata engine of JuiceFS, Redis can also be used as data storage. It is recommended to use Redis to store data with a small amount of data, such as application configuration.

The `--bucket` option format is `redis://<host>:<port>/<db>`. The value of `--access-key` option is username. The value of `--secret-key` option is password. For example:

```bash
$ ./juicefs format \
    --storage redis \
    --bucket redis://<host>:<port>/<db> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    localhost test
```

## TiKV

[TiKV](https://tikv.org) is a highly scalable, low latency, and easy to use key-value database. It provides both raw and ACID-compliant transactional key-value API.

The `--bucket` option format is like `<host>:<port>,<host>:<port>,<host>:<port>`, the `<host>` is the address of Placement Driver (PD). The `--access-key` and `--secret-key` options have no effect and can be omitted. For example:

```bash
$ ./juicefs format \
    --storage tikv \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>" \
    ... \
    localhost test
```

## Local disk <span id='local'></span>

When creating JuiceFS storage, if no storage type is specified, the local disk will be used to store data by default. The default storage path for root user is `/var/jfs`, and `~/.juicefs/local` is for ordinary users.

For example, using the local Redis database and local disk to create a JuiceFS storage named `test`:

```shell
$ ./juicefs format redis://localhost:6379/1 test
```

Local storage is only used to understand and experience the basic functions of JuiceFS. The created JuiceFS storage cannot be mounted by other clients in the network and can only be used on a stand-alone machine.

If you need to evaluate JuiceFS, it is recommended to use object storage services.

> **Note**: JuiceFS storage created using local storage cannot be mounted by other hosts on the network. This is because the data sharing function of JuiceFS relies on the object storage and metadata service that can be accessed by all clients. If the storage service and metadata service used when creating JuiceFS storage cannot be accessed by other clients in the network, other clients cannot mount and use the JuiceFS storage.
