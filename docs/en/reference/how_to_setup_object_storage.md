---
sidebar_label: How to Setup Object Storage
sidebar_position: 4
slug: /how_to_setup_object_storage
---

# How to Setup Object Storage

As you can learn from [JuiceFS Technical Architecture](../introduction/architecture.md), JuiceFS is a distributed file system with data and metadata separation, using object storage as the main data storage and Redis, PostgreSQL, MySQL and other databases as metadata storage.

## Storage options

When creating a JuiceFS file system, setting up the storage generally involves the following options:

- `--storage`: Specify the type of storage to be used by the file system, e.g. `--storage s3`
- `--bucket`: Specify the storage access address, e.g. `--bucket https://myjuicefs.s3.us-east-2.amazonaws.com`
- `--access-key` and `--secret-key`: Specify the authentication information when accessing the storage

For example, the following command uses Amazon S3 object storage to create a file system:

```shell
$ juicefs format --storage s3 \
	--bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
	--access-key abcdefghijklmn \
	--secret-key nmlkjihgfedAcBdEfg \
	redis://192.168.1.6/1 \
	myjfs
```

## Access Key and Secret Key

In general, object storages are authenticated by `Access Key ID` and `Access Key Secret`, which correspond to the `--access-key` and `--secret-key` options (or AK, SK for short) on the JuiceFS file system.

In addition to explicitly specifying the `--access-key` and `--secret-key` options when creating a filesystem, it is more secure to pass key information via the `ACCESS_KEY` and `SECRET_KEY` environment variables, e.g.

```shell
$ export ACCESS_KEY=abcdefghijklmn
$ export SECRET_KEY=nmlkjihgfedAcBdEfg
$ juicefs format --storage s3 \
	--bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
	redis://192.168.1.6/1 \
	myjfs
```

Public clouds typically allow users to create IAM (Identity and Access Management) roles, such as [AWS IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html) or [Alibaba Cloud RAM role](https://www.alibabacloud.com/help/doc-detail/110376.htm), which can be assigned to VM instances. If the cloud server instance already has read and write access to the object storage, there is no need to specify `--access-key` and `--secret-key`.

## Using Proxy

If the network environment where the client is located is affected by firewall policies or other factors that require access to external object storage services through a proxy, the corresponding proxy settings are different for different operating systems, please refer to the corresponding user manual for settings.

On Linux, for example, the proxy can be set by creating `http_proxy` and `https_proxy` environment variables.

```shell
$ export http_proxy=http://localhost:8035/
$ export https_proxy=http://localhost:8035/
$ juicefs format \
    --storage s3 \
    ... \
    myjfs
```

## Supported Object Storage

If you wish to use a storage type that is not listed, feel free to submit a requirement [issue](https://github.com/juicedata/juicefs/issues).

| Name                                                      | Value      |
| --------------------------------------------------------- | ---------- |
| [Amazon S3](#amazon-s3)                                   | `s3`       |
| [Google Cloud Storage](#google-cloud-storage)             | `gs`       |
| [Azure Blob Storage](#azure-blob-storage)                 | `wasb`     |
| [Backblaze B2](#backblaze-b2)                             | `b2`       |
| [IBM Cloud Object Storage](#ibm-cloud-object-storage)     | `ibmcos`   |
| [Scaleway Object Storage](#scaleway-object-storage)       | `scw`      |
| [DigitalOcean Spaces](#digitalocean-spaces)               | `space`    |
| [Wasabi](#wasabi)                                         | `wasabi`   |
| [Storj DCS](#storj-dcs)                                   | `s3`       |
| [Vultr Object Storage](#vultr-object-storage)             | `s3`       |
| [Alibaba Cloud OSS](#alibaba-cloud-oss)                   | `oss`      |
| [Tencent Cloud COS](#tencent-cloud-cos)                   | `cos`      |
| [Huawei Cloud OBS](#huawei-cloud-obs)                     | `obs`      |
| [Baidu Object Storage](#baidu-object-storage)             | `bos`      |
| [Kingsoft KS3](#kingsoft-ks3)                             | `ks3`      |
| [NetEase Object Storage](#netease-object-storage)         | `nos`      |
| [QingStor](#qingstor)                                     | `qingstor` |
| [Qiniu Object Storage](#qiniu-object-storage)             | `qiniu`    |
| [Sina Cloud Storage](#sina-cloud-storage)                 | `scs`      |
| [CTYun OOS](#ctyun-oos)                                   | `oos`      |
| [ECloud Object Storage](#ecloud-object-storage)           | `eos`      |
| [UCloud US3](#ucloud-us3)                                 | `ufile`    |
| [Ceph RADOS](#ceph-rados)                                 | `ceph`     |
| [Ceph RGW](#ceph-rgw)                                     | `s3`       |
| [Swift](#swift)                                           | `swift`    |
| [MinIO](#minio)                                           | `minio`    |
| [WebDAV](#webdav)                                         | `webdav`   |
| [HDFS](#hdfs)                                             | `hdfs`     |
| [Redis](#redis)                                           | `redis`    |
| [TiKV](#tikv)                                             | `tikv`     |
| [Local disk](#local-disk)                                 | `file`     |

## Amazon S3

S3 supports [two style endpoint URI](https://docs.aws.amazon.com/AmazonS3/latest/dev/VirtualHosting.html): virtual hosted-style and path-style. The difference between them is:

- Virtual hosted-style: `https://<bucket>.s3.<region>.amazonaws.com`
- Path-style: `https://s3.<region>.amazonaws.com/<bucket>`

The `<region>` should be replaced with specific region code, e.g. the region code of US East (N. Virginia) is `us-east-1`. You could find all available regions at [here](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-regions-availability-zones.html#concepts-available-regions).

:::note
For AWS China user, you need add `.cn` to the host, i.e. `amazonaws.com.cn`. And check [this document](https://docs.amazonaws.cn/en_us/aws/latest/userguide/endpoints-arns.html) to know your region code.
:::

:::note
If the S3 bucket has public access (anonymous access is supported), please set `--access-key` to `anonymous`.
:::

Versions prior to JuiceFS v0.12 only supported the virtual hosting type, v0.12 and later versions support both styles. Example.

```bash
# virtual hosted-style
$ juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    ... \
    myjfs
```

```bash
# path-style
$ juicefs format \
    --storage s3 \
    --bucket https://s3.<region>.amazonaws.com/<bucket> \
    ... \
    myjfs
```

You can also set `--storage` to `s3` to connect to S3-compatible object storage, e.g.

```bash
# virtual hosted-style
$ juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

```bash
# path-style
$ juicefs format \
    --storage s3 \
    --bucket https://<endpoint>/<bucket> \
    ... \
    myjfs
```

:::tip
The format of `--bucket` option for all S3 compatible object storage services is `https://<bucket>.<endpoint>` or `https://<endpoint>/<bucket>`. The default `region` is `us-east-1`. When a different `region` is required, it can be set manually via the environment variable `AWS_REGION` or `AWS_DEFAULT_REGION`.
:::

## Google Cloud Storage

Google Cloud uses [IAM](https://cloud.google.com/iam/docs/overview) to manage the access rights of resources, and through the authorization of [service accounts](https://cloud.google.com/iam/docs/creating-managing- service-accounts#iam-service-accounts-create-gcloud) authorization, you can fine-grained control the access rights of cloud servers and object storage.

For cloud servers and object storage that belong to the same service account, as long as the account grants access to the relevant resources, there is no need to provide authentication information when creating a JuiceFS file system, and the cloud platform will automatically complete authentication.

For cases where you want to access the object storage from outside the Google Cloud Platform, for example to create a JuiceFS file system on your local computer using Google Cloud Storage, you need to configure authentication information. Since Google Cloud Storage does not use `Access Key ID` and `Access Key Secret`, but rather the `JSON key file` of the service account to authenticate the identity.

Please refer to "[Authentication as a service account](https://cloud.google.com/docs/authentication/production)" to create `JSON key file` for the service account and download it to the local computer, and define the path to the key file via `GOOGLE_APPLICATION_ CREDENTIALS` environment variable to define the path to the key file, e.g.

```shell
export GOOGLE_APPLICATION_CREDENTIALS="$HOME/service-account-file.json"
```

You can write the command to create environment variables to `~/.bashrc` or `~/.profile` and have the shell set it automatically every time you start.

Once you have configured the environment variables for passing key information, the commands to create a file system locally and on Google Cloud Server are identical. For example.

```bash
$ juicefs format \
    --storage gs \
    --bucket <bucket> \
    ... \
    myjfs
```

As you can see, there is no need to include authentication information in the command, and the client will authenticate the access to the object storage through the JSON key file set in the previous environment variable. Also, since the bucket name is [globally unique](https://cloud.google.com/storage/docs/naming-buckets#considerations), when creating a file system, the `--bucket` option only needs to specify the bucket name.

## Azure Blob Storage

Besides provide authorization information through `--access-key` and `--secret-key` options, you could also create a [connection string](https://docs.microsoft.com/en-us/azure/storage/common/storage-configure-connection-string) and set `AZURE_STORAGE_CONNECTION_STRING` environment variable. For example:

```bash
# Use connection string
$ export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=https;AccountName=XXX;AccountKey=XXX;EndpointSuffix=core.windows.net"
$ juicefs format \
    --storage wasb \
    --bucket https://<container> \
    ... \
    myjfs
```

:::note
For Azure China user, the value of `EndpointSuffix` is `core.chinacloudapi.cn`.
:::

## Backblaze B2

To use Backblaze B2 as a data storage for JuiceFS, you need to create [application key](https://www.backblaze.com/b2/docs/application_keys.html) first, **Application Key ID** and ** Application Key** corresponds to `Access key` and `Secret key` respectively.

Backblaze B2 supports two access interfaces: the B2 native API and the S3-compatible API.

### B2 native API

The storage type should be set to `b2` and `--bucket` should only set the bucket name. For example:

```bash
$ juicefs format \
    --storage b2 \
    --bucket <bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    myjfs
```

### S3-compatible API

The storage type should be set to `s3` and `--bucket` should specify the full bucket address. For example:

```bash
$ juicefs format \
    --storage s3 \
    --bucket https://s3.eu-central-003.backblazeb2.com/<bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    myjfs
```

## IBM Cloud Object Storage

You need first creating [API key](https://cloud.ibm.com/docs/account?topic=account-manapikey) and retrieving [instance ID](https://cloud.ibm.com/docs/key-protect?topic=key-protect-retrieve-instance-ID). The "API key" and "instance ID" are the equivalent of access key and secret key respectively.

IBM Cloud Object Storage provides [multiple endpoints](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints) for each region, depends on your network (e.g. public or private network), you should use appropriate endpoint. For example:

```bash
$ juicefs format \
    --storage ibmcos \
    --bucket https://<bucket>.<endpoint> \
    --access-key <API-key> \
    --secret-key <instance-ID> \
    ... \
    myjfs
```

## Scaleway Object Storage

Please follow [this document](https://www.scaleway.com/en/docs/generate-api-keys) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.scw.cloud`, replace `<region>` with specific region code, e.g. the region code of "Amsterdam, The Netherlands" is `nl-ams`. You could find all available regions at [here](https://www.scaleway.com/en/docs/object-storage-feature/#-Core-Concepts). For example:

```bash
$ juicefs format \
    --storage scw \
    --bucket https://<bucket>.s3.<region>.scw.cloud \
    ... \
    myjfs
```

## DigitalOcean Spaces

Please follow [this document](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<space-name>.<region>.digitaloceanspaces.com`, replace `<region>` with specific region code, e.g. `nyc3`. You could find all available regions at [here](https://www.digitalocean.com/docs/spaces/#regional-availability). For example:

```bash
$ juicefs format \
    --storage space \
    --bucket https://<space-name>.<region>.digitaloceanspaces.com \
    ... \
    myjfs
```

## Wasabi

Please follow [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360019677192-Creating-a-Root-Access-Key-and-Secret-Key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.wasabisys.com`, replace `<region>` with specific region code, e.g. the region code of US East 1 (N. Virginia) is `us-east-1`. You could find all available regions at [here](https://wasabi-support.zendesk.com/hc/en-us/articles/360.15.26031-What-are-the-service-URLs-for-Wasabi-s-different-regions-). For example:

```bash
$ juicefs format \
    --storage wasabi \
    --bucket https://<bucket>.s3.<region>.wasabisys.com \
    ... \
    myjfs
```

:::note
For Tokyo (ap-northeast-1) region user, see [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360039372392-How-do-I-access-the-Wasabi-Tokyo-ap-northeast-1-storage-region-) to learn how to get appropriate endpoint URI.***
:::

## Storj DCS

Please refer to [this document](https://docs.storj.io/api-reference/s3-compatible-gateway) to learn how to create access key and secret key.

Storj DCS is an S3-compatible storage, just use `s3` for `--storage` option. The setting format of the `--bucket` option is `https://gateway.<region>.storjshare.io/<bucket>`, please replace `<region>` with the storage region you actually use. There are currently three avaliable regions: `us1`, `ap1` and `eu1`. For example:

```shell
$ juicefs format \
	--storage s3 \
	--bucket https://gateway.<region>.storjshare.io/<bucket> \
	--access-key <your-access-key> \
	--secret-key <your-sceret-key> \
	... \
    myjfs
```

## Vultr Object Storage

Vultr Object Storage is an S3-compatible storage, use `s3` for `--storage` option. The `--bucket` option is `https://<bucket>.<region>.vultrobjects.com/`. For example:

```shell
$ juicefs format \
	--storage s3 \
	--bucket https://<bucket>.ewr1.vultrobjects.com/ \
	--access-key <your-access-key> \
	--secret-key <your-sceret-key> \
	... \
    myjfs
```

Please find the access and secret keys for object storage [in the customer portal](https://my.vultr.com/objectstorage/).

## Alibaba Cloud OSS

Please follow [this document](https://www.alibabacloud.com/help/doc-detail/125558.htm) to learn how to get access key and secret key. And if you already created [RAM role](https://www.alibabacloud.com/help/doc-detail/110376.htm) and assign it to VM instance, you could omit `--access-key` and `--secret-key` options.

Alibaba Cloud also supports use [Security Token Service (STS)](https://www.alibabacloud.com/help/doc-detail/100624.htm) to authorize temporary access to OSS. If you wanna use STS, you should omit `--access-key` and `--secret-key` options and set `ALICLOUD_ACCESS_KEY_ID`, `ALICLOUD_ACCESS_KEY_SECRET`, `SECURITY_TOKEN` environment variables instead, for example:

```bash
# Use Security Token Service (STS)
$ export ALICLOUD_ACCESS_KEY_ID=XXX
$ export ALICLOUD_ACCESS_KEY_SECRET=XXX
$ export SECURITY_TOKEN=XXX
$ juicefs format \
    --storage oss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

OSS provides [multiple endpoints](https://www.alibabacloud.com/help/doc-detail/31834.htm) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint.

If you are creating a filesystem on AliCloud's server, you can specify the bucket name directly in the `--bucket` option. For example.

```bash
# Running within Alibaba Cloud
$ juicefs format \
    --storage oss \
    --bucket <bucket> \
    ... \
    myjfs
```

## Tencent Cloud COS

The naming rule of bucket in Tencent Cloud is `<bucket>-<APPID>`, so you must append `APPID` to the bucket name. Please follow [this document](https://intl.cloud.tencent.com/document/product/436/13312) to learn how to get `APPID`.

The full format of `--bucket` option is `https://<bucket>-<APPID>.cos.<region>.myqcloud.com`, replace `<region>` with specific region code, e.g. the region code of Shanghai is `ap-shanghai`. You could find all available regions at [here](https://intl.cloud.tencent.com/document/product/436/6224). For example:

```bash
$ juicefs format \
    --storage cos \
    --bucket https://<bucket>-<APPID>.cos.<region>.myqcloud.com \
    ... \
    myjfs
```

If you are creating a file system on Tencent Cloud's server, you can specify the bucket name directly in the `--bucket` option. For example.

```bash
# Running within Tencent Cloud
$ juicefs format \
    --storage cos \
    --bucket <bucket>-<APPID> \
    ... \
    myjfs
```

## Huawei Cloud OBS

Please follow [this document](https://support.huaweicloud.com/usermanual-ca/zh-cn_topic_0046606340.html) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.obs.<region>.myhuaweicloud.com`, replace `<region>` with specific region code, e.g. the region code of Beijing 1 is `cn-north-1`. You could find all available regions at [here](https://developer.huaweicloud.com/endpoint?OBS). For example:

```bash
$ juicefs format \
    --storage obs \
    --bucket https://<bucket>.obs.<region>.myhuaweicloud.com \
    ... \
    myjfs
```

If you create the file system on Huawei Cloud's server, you can specify the bucket name directly in `--bucket`. For example.

```bash
# Running within Huawei Cloud
$ juicefs format \
    --storage obs \
    --bucket <bucket> \
    ... \
    myjfs
```

## Baidu Object Storage

Please follow [this document](https://cloud.baidu.com/doc/Reference/s/9jwvz2egb) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.bcebos.com`, replace `<region>` with specific region code, e.g. the region code of Beijing is `bj`. You could find all available regions at [here](https://cloud.baidu.com/doc/BOS/s/Ck1rk80hn#%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D%EF%BC%88endpoint%EF%BC%89). For example:

```bash
$ juicefs format \
    --storage bos \
    --bucket https://<bucket>.<region>.bcebos.com \
    ... \
    myjfs
```

If you are creating a file system on Baidu Cloud's server, you can specify the bucket name directly in `--bucket`. For example.

```bash
# Running within Baidu Cloud
$ juicefs format \
    --storage bos \
    --bucket <bucket> \
    ... \
    myjfs
```

## Kingsoft Cloud KS3

Please follow [this document](https://docs.ksyun.com/documents/1386) to learn how to get access key and secret key.

KS3 provides [multiple endpoints](https://docs.ksyun.com/documents/6761) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ juicefs format \
    --storage ks3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

## Mtyun Storage Service

Please follow [this document](https://www.mtyun.com/doc/api/mss/mss/fang-wen-kong-zhi) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<endpoint>`, replace `<endpoint>` with specific value, e.g. `mtmss.com`. You could find all available endpoints at [here](https://www.mtyun.com/doc/products/storage/mss/index#%E5%8F%AF%E7%94%A8%E5%8C%BA%E5%9F%9F). For example:

```bash
$ juicefs format \
    --storage mss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

## NetEase Object Storage

Please follow [this document](https://www.163yun.com/help/documents/55485278220111872) to learn how to get access key and secret key.

NOS provides [multiple endpoints](https://www.163yun.com/help/documents/67078583131230208) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ juicefs format \
    --storage nos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

## QingStor

Please follow [this document](https://docsv3.qingcloud.com/storage/object-storage/api/practices/signature/#%E8%8E%B7%E5%8F%96-access-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.qingstor.com`, replace `<region>` with specific region code, e.g. the region code of Beijing 3-A is `pek3a`. You could find all available regions at [here](https://docs.qingcloud.com/qingstor/#%E5%8C%BA%E5%9F%9F%E5%8F%8A%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D). For example:

```bash
$ juicefs format \
    --storage qingstor \
    --bucket https://<bucket>.<region>.qingstor.com \
    ... \
    myjfs
```

:::note
The format of `--bucket` option for all QingStor compatible object storage services is `http://<bucket>.<endpoint>`.
:::

## Qiniu

Please follow [this document](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3-<region>.qiniucs.com`, replace `<region>` with specific region code, e.g. the region code of China East is `cn-east-1`. You could find all available regions at [here](https://developer.qiniu.com/kodo/4088/s3-access-domainname). For example:

```bash
$ juicefs format \
    --storage qiniu \
    --bucket https://<bucket>.s3-<region>.qiniucs.com \
    ... \
    myjfs
```

## Sina Cloud Storage

Please follow [this document](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.stor.sinaapp.com`. For example:

```bash
$ juicefs format \
    --storage scs \
    --bucket https://<bucket>.stor.sinaapp.com \
    ... \
    myjfs
```

## CTYun OOS

Please follow [this document](https://www.ctyun.cn/help2/10000101/10473683) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.oss-<region>.ctyunapi.cn`, replace `<region>` with specific region code, e.g. the region code of Chengdu is `sccd`. You could find all available regions at [here](https://www.ctyun.cn/help2/10000101/10474062). For example:

```bash
$ juicefs format \
    --storage oos \
    --bucket https://<bucket>.oss-<region>.ctyunapi.cn \
    ... \
    myjfs
```

## ECloud Object Storage

Please follow [this document](https://ecloud.10086.cn/op-help-center/doc/article/24501) to learn how to get access key and secret key.

ECloud Object Storage provides [multiple endpoints](https://ecloud.10086.cn/op-help-center/doc/article/40956) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ juicefs format \
    --storage eos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

## UCloud US3

Please follow [this document](https://docs.ucloud.cn/uai-censor/access/key) to learn how to get access key and secret key.

US3 (formerly UFile) provides [multiple endpoints](https://docs.ucloud.cn/ufile/introduction/region) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ juicefs format \
    --storage ufile \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

## Ceph RADOS

:::note
The minimum version of Ceph supported by JuiceFS is Luminous (v12.2.*), please make sure your version of Ceph meets the requirements.
:::

The [Ceph Storage Cluster](https://docs.ceph.com/en/latest/rados) has a messaging layer protocol that enables clients to interact with a Ceph Monitor and a Ceph OSD Daemon. The [`librados`](https://docs.ceph.com/en/latest/rados/api/librados-intro) API enables you to interact with the two types of daemons:

- The [Ceph Monitor](https://docs.ceph.com/en/latest/rados/configuration/common/#monitors), which maintains a master copy of the cluster map.
- The [Ceph OSD Daemon (OSD)](https://docs.ceph.com/en/latest/rados/configuration/common/#osds), which stores data as objects on a storage node.

JuiceFS supports the use of native Ceph APIs based on `librados`. You need install `librados` library and build `juicefs` binary separately.

First installing `librados`:

:::note
It is recommended to use `librados` that matches your Ceph version, e.g. if Ceph version is Octopus (v15.2.\*), then `librados` is also recommended to use v15.2.\*. Some Linux distributions (e.g. CentOS 7) may come with a lower version of `librados`, so if you fail to compile JuiceFS try downloading a higher version of the package.
:::

```bash
# Debian based system
$ sudo apt-get install librados-dev

# RPM based system
$ sudo yum install librados2-devel
```

Then compile JuiceFS for Ceph (ensure you have Go 1.16+ and GCC 5.4+):

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
$ juicefs.ceph format \
    --storage ceph \
    --bucket ceph://<pool-name> \
    --access-key <cluster-name> \
    --secret-key <user-name> \
    ... \
    myjfs
```

## Ceph RGW

[Ceph Object Gateway](https://ceph.io/ceph-storage/object-storage) is an object storage interface built on top of `librados` to provide applications with a RESTful gateway to Ceph Storage Clusters. Ceph Object Gateway supports S3-compatible interface, so we could set `--storage` to `s3` directly.

The `--bucket` option format is `http://<bucket>.<endpoint>` (virtual hosted-style). For example:

```bash
$ juicefs format \
    --storage s3 \
    --bucket http://<bucket>.<endpoint> \
    ... \
    myjfs
```

## Swift

[OpenStack Swift](https://github.com/openstack/swift) is a distributed object storage system designed to scale from a single machine to thousands of servers. Swift is optimized for multi-tenancy and high concurrency. Swift is ideal for backups, web and mobile content, and any other unstructured data that can grow without bound.

The `--bucket` option format is `http://<container>.<endpoint>`. A container defines a namespace for objects.

**Currently, JuiceFS only supports [Swift V1 authentication](https://www.swiftstack.com/docs/cookbooks/swift_usage/auth.html).**

The value of `--access-key` option is username. The value of `--secret-key` option is password. For example:

```bash
$ juicefs format \
    --storage swift \
    --bucket http://<container>.<endpoint> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

## MinIO

[MinIO](https://min.io) is an open source lightweight object storage, compatible with Amazon S3 API.

It is easy to run a MinIO object store instance locally using Docker. For example, the following command sets and maps port `9900` for the console with `-console-address ":9900"` and also maps the data path for the MinIO object store to the `minio-data` folder in the current directory, which you can modify as needed.

```shell
$ sudo docker run -d --name minio \
    -p 9000:9000 \
    -p 9900:9900 \
    -e "MINIO_ROOT_USER=minioadmin" \
    -e "MINIO_ROOT_PASSWORD=minioadmin" \
    -v $PWD/minio-data:/data \
    --restart unless-stopped \
    minio/minio server /data --console-address ":9900"
```

It is accessed using the following address:

- **MinIO UI**：[http://127.0.0.1:9900](http://127.0.0.1:9900/)
- **MinIO API**：[http://127.0.0.1:9000](http://127.0.0.1:9000/)

The initial Access Key and Secret Key of the object store are both `minioadmin`.

Use MinIO as data storage for JuiceFS, with the `--storage` option set to `minio`.

```bash
$ juicefs format \
    --storage minio \
    --bucket http://127.0.0.1:9000/<bucket> \
    --access-key minioadmin \
    --secret-key minioadmin \
    ... \
    myjfs
```

:::note
Currently, JuiceFS only supports path-style MinIO URI addresses, e.g., `http://127.0.0.1:9000/myjfs`.
:::

## WebDAV

[WebDAV](https://en.wikipedia.org/wiki/WebDAV) is an extension of the Hypertext Transfer Protocol (HTTP)
that facilitates collaborative editing and management of documents stored on the WWW server among users.
Starting from JuiceFS v0.15+, for a storage that speaks WebDAV, JuiceFS can use it as the data store.

You need set `--storage` to `webdav`, and `--bucket` to the endpoint of WebDAV. If basic authorization is enable, username and password should be provided as `--access-key` and `--secret-key`, for example:

```bash
$ juicefs format \
    --storage webdav \
    --bucket http://<endpoint>/ \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

## HDFS

[HDFS](https://hadoop.apache.org) is the file system for Hadoop, which can be used as the object store for JuiceFS.

When HDFS is used, `--access-key` can be used to specify the `username`, and `hdfs` is usually the default superuser. For example:

```bash
$ juicefs format \
    --storage hdfs \
    --bucket namenode1:8020 \
    --access-key hdfs \
    ... \
    myjfs
```

When the `--access-key` is not specified during formatting, JuiceFS will use the current user of `juicefs mount` or Hadoop SDK to access HDFS. It will hang and fail with IO error eventually, if the current user don't have enough permission to read/write the blocks in HDFS.

JuiceFS will try to load configurations for HDFS client based on `$HADOOP_CONF_DIR` or `$HADOOP_HOME`. If an empty value is provided to `--bucket`, the default HDFS found in Hadoop configurations will be used.

For HA cluster, the addresses of NameNodes can be specified together like this: `--bucket=namenode1:port,namenode2:port`.

## Redis

[Redis](https://redis.io) can be used as both a metadata storage for JuiceFS and as a data storage, but when using Redis as a data storage, it is recommended not to store large scale data.

The `--bucket` option format is `redis://<host>:<port>/<db>`. The value of `--access-key` option is username. The value of `--secret-key` option is password. For example:

```bash
$ juicefs format \
    --storage redis \
    --bucket redis://<host>:<port>/<db> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

## TiKV

[TiKV](https://tikv.org) is a highly scalable, low latency, and easy to use key-value database. It provides both raw and ACID-compliant transactional key-value API.

TiKV can be used as both metadata storage and data storage for JuiceFS.

The `--bucket` option format is like `<host>:<port>,<host>:<port>,<host>:<port>`, the `<host>` is the address of Placement Driver (PD). The `--access-key` and `--secret-key` options have no effect and can be omitted. For example:

```bash
$ juicefs format \
    --storage tikv \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>" \
    ... \
    myjfs
```

## Local disk

When creating JuiceFS storage, if no storage type is specified, the local disk will be used to store data by default. The default storage path for root user is `/var/jfs`, and `~/.juicefs/local` is for ordinary users.

For example, using the local Redis database and local disk to create a JuiceFS storage named `test`:

```shell
$ juicefs format redis://localhost:6379/1 test
```

Local storage is usually only used to understand and experience the basic features of JuiceFS. The created JuiceFS storage cannot be mounted by other clients within the network and can only be used on a single machine.
