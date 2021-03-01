# How to Setup Object Storage

This is a guide about how to setup object storage when format a volume. Different object storage may has different option value. Check the specific object storage for your need.

## Supported Object Storage

This table lists all JuiceFS supported object storage, when you format a volume you need specify storage type through `--storage` option, e.g. for Amazon S3 the value of `--storage` is `s3`.

| Name                                       | Value      |
| ----                                       | -----      |
| Amazon S3                                  | `s3`       |
| Google Cloud Storage                       | `gs`       |
| Azure Blob Storage                         | `wasb`     |
| Backblaze B2 Cloud Storage                 | `b2`       |
| IBM Cloud Object Storage                   | `ibmcos`   |
| Scaleway Object Storage                    | `scw`      |
| DigitalOcean Spaces Object Storage         | `space`    |
| Wasabi Cloud Object Storage                | `wasabi`   |
| Alibaba Cloud Object Storage Service       | `oss`      |
| Tencent Cloud Object Storage               | `cos`      |
| Huawei Cloud Object Storage Service        | `obs`      |
| Baidu Object Storage                       | `bos`      |
| Kingsoft Cloud Standard Storage Service    | `ks3`      |
| Meituan Storage Service                    | `mss`      |
| NetEase Object Storage                     | `nos`      |
| QingStor Object Storage                    | `qingstor` |
| Qiniu Cloud Object Storage                 | `qiniu`    |
| Sina Cloud Storage                         | `scs`      |
| CTYun Object-Oriented Storage              | `oos`      |
| ECloud (China Mobile Cloud) Object Storage | `eos`      |
| SpeedyCloud Object Storage                 | `speedy`   |
| UCloud US3                                 | `ufile`    |
| Ceph RADOS                                 | `ceph`     |
| Ceph Object Gateway (RGW)                  | `s3`       |
| Swift                                      | `swift`    |
| MinIO                                      | `minio`    |
| HDFS                                       | `hdfs`     |
| Redis                                      | `redis`    |
| Local disk                                 | `file`     |

## Access key and secret key

For authorization, the access key and secret key are needed. You could specify them through `--access-key` and `--secret-key` options. Or you can set `ACCESS_KEY` and `SECRET_KEY` environment variables.

Public cloud provider usually allow user create IAM (Identity and Access Management) role (e.g. [AWS IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html)) or similar thing (e.g. [Alibaba Cloud RAM role](https://help.aliyun.com/document_detail/93689.html)), then assign the role to VM instance. If your VM instance already have permission to access object storage, then you could omit `--access-key` and `--secret-key` options.

## S3

S3 supports [two style endpoint URI](https://docs.aws.amazon.com/AmazonS3/latest/dev/VirtualHosting.html): virtual hosted-style and path-style. The difference between them is:

- Virtual hosted-style: `https://<bucket>.s3.<region>.amazonaws.com`
- Path-style: `https://s3.<region>.amazonaws.com/<bucket>`

The `<region>` should be replaced with specific region code, e.g. the region code of US East (N. Virginia) is `us-east-1`. You could find all available regions at [here](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-regions-availability-zones.html#concepts-available-regions).

***Note: For AWS China user, you need add `.cn` to the host, i.e. `amazonaws.com.cn`. And check [this document](https://docs.amazonaws.cn/en_us/aws/latest/userguide/endpoints-arns.html) to know your region code.***

Currently, **JuiceFS only supports virtual hosted-style** and maybe support path-style in the future ([#134](https://github.com/juicedata/juicefs/issues/134)). So when you format a volume, the `--bucket` option should be virtual hosted-style URI. For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    ... \
    localhost test
```

You can also use S3 storage type to connect with S3-compatible storage. But beware that you still need use virtual hosted-style URI. For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Google Cloud Storage

Because Google Cloud doesn't have access key and secret key, the `--access-key` and `--secret-key` options can be omitted. Please follow Google Cloud document to know how [authentication](https://cloud.google.com/docs/authentication) and [authorization](https://cloud.google.com/iam/docs/overview) work. Typically, when you running within Google Cloud, you already have permission to access the storage.

And because bucket name is [globally unique](https://cloud.google.com/storage/docs/naming-buckets#considerations), when you specify the `--bucket` option could just provide its name. For example:

```bash
$ ./juicefs format \
    --storage gs \
    --bucket gs://<bucket> \
    ... \
    localhost test
```

## Azure Blob Storage

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

***Note: For Azure China user, the value of `EndpointSuffix` is `core.chinacloudapi.cn`.***

## Backblaze B2 Cloud Storage

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

## IBM Cloud Object Storage

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

## Scaleway Object Storage

Please follow [this document](https://www.scaleway.com/en/docs/generate-api-keys) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.scw.cloud`, replace `<region>` with specific region code, e.g. the region code of "Amsterdam, The Netherlands" is `nl-ams`. You could find all available regions at [here](https://www.scaleway.com/en/docs/object-storage-feature/#-Core-Concepts). For example:

```bash
$ ./juicefs format \
    --storage scw \
    --bucket https://<bucket>.s3.<region>.scw.cloud \
    ... \
    localhost test
```

## DigitalOcean Spaces Object Storage

Please follow [this document](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<space-name>.<region>.digitaloceanspaces.com`, replace `<region>` with specific region code, e.g. `nyc3`. You could find all available regions at [here](https://www.digitalocean.com/docs/spaces/#regional-availability). For example:

```bash
$ ./juicefs format \
    --storage space \
    --bucket https://<space-name>.<region>.digitaloceanspaces.com \
    ... \
    localhost test
```

## Wasabi Cloud Object Storage

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

## Alibaba Cloud Object Storage Service

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

## Tencent Cloud Object Storage

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

## Huawei Cloud Object Storage Service

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

## Baidu Object Storage

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

## Kingsoft Cloud Standard Storage Service

Please follow [this document](https://docs.ksyun.com/documents/1386) to learn how to get access key and secret key.

KS3 provides [multiple endpoints](https://docs.ksyun.com/documents/6761) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage ks3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Meituan Storage Service

Please follow [this document](https://www.mtyun.com/doc/api/mss/mss/fang-wen-kong-zhi) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<endpoint>`, replace `<endpoint>` with specific value, e.g. `mtmss.com`. You could find all available endpoints at [here](https://www.mtyun.com/doc/products/storage/mss/index#%E5%8F%AF%E7%94%A8%E5%8C%BA%E5%9F%9F). For example:

```bash
$ ./juicefs format \
    --storage mss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## NetEase Object Storage

Please follow [this document](https://www.163yun.com/help/documents/55485278220111872) to learn how to get access key and secret key.

NOS provides [multiple endpoints](https://www.163yun.com/help/documents/67078583131230208) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage nos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## QingStor Object Storage

Please follow [this document](https://docs.qingcloud.com/qingstor/api/common/signature.html#%E8%8E%B7%E5%8F%96-access-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.qingstor.com`, replace `<region>` with specific region code, e.g. the region code of Beijing 3-A is `pek3a`. You could find all available regions at [here](https://docs.qingcloud.com/qingstor/#%E5%8C%BA%E5%9F%9F%E5%8F%8A%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D). For example:

```bash
$ ./juicefs format \
    --storage qingstor \
    --bucket https://<bucket>.<region>.qingstor.com \
    ... \
    localhost test
```

## Qiniu Cloud Object Storage

Please follow [this document](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3-<region>.qiniucs.com`, replace `<region>` with specific region code, e.g. the region code of China East is `cn-east-1`. You could find all available regions at [here](https://developer.qiniu.com/kodo/4088/s3-access-domainname). For example:

```bash
$ ./juicefs format \
    --storage qiniu \
    --bucket https://<bucket>.s3-<region>.qiniucs.com \
    ... \
    localhost test
```

## Sina Cloud Storage

Please follow [this document](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.stor.sinaapp.com`. For example:

```bash
$ ./juicefs format \
    --storage scs \
    --bucket https://<bucket>.stor.sinaapp.com \
    ... \
    localhost test
```

## CTYun Object-Oriented Storage

Please follow [this document](https://www.ctyun.cn/help2/10000101/10473683) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.oss-<region>.ctyunapi.cn`, replace `<region>` with specific region code, e.g. the region code of Chengdu is `sccd`. You could find all available regions at [here](https://www.ctyun.cn/help2/10000101/10474062). For example:

```bash
$ ./juicefs format \
    --storage oos \
    --bucket https://<bucket>.oss-<region>.ctyunapi.cn \
    ... \
    localhost test
```

## ECloud (China Mobile Cloud) Object Storage

Please follow [this document](https://ecloud.10086.cn/op-help-center/doc/article/24501) to learn how to get access key and secret key.

ECloud Object Storage provides [multiple endpoints](https://ecloud.10086.cn/op-help-center/doc/article/40956) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage eos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## UCloud US3

Please follow [this document](https://docs.ucloud.cn/uai-censor/access/key) to learn how to get access key and secret key.

US3 (formerly UFile) provides [multiple endpoints](https://docs.ucloud.cn/ufile/introduction/region) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage ufile \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Ceph RADOS

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

Then compile JuiceFS for Ceph:

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

## Ceph Object Gateway (RGW)

[Ceph Object Gateway](https://ceph.io/ceph-storage/object-storage) is an object storage interface built on top of `librados` to provide applications with a RESTful gateway to Ceph Storage Clusters. Ceph Object Gateway supports S3-compatible interface, so we could set `--storage` to `s3` directly.

The `--bucket` option format is `http://<bucket>.<endpoint>` (virtual hosted-style). For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket http://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Swift

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

## MinIO

[MinIO](https://min.io) is an open source high performance object storage. It is API compatible with Amazon S3. You need set `--storage` option to `minio`. Currently, JuiceFS only supports path-style URI when use MinIO storage. For example (`<endpoint>` may looks like `1.2.3.4:9000`):

```bash
$ ./juicefs format \
    --storage minio \
    --bucket http://<endpoint>/<bucket> \
    ... \
    localhost test
```
