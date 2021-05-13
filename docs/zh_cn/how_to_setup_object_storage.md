# JuiceFS 支持的对象存储和设置指南

通过阅读 [JuiceFS 的技术架构](architecture.md) 和 [JuiceFS 如何存储文件](how_juicefs_store_files.md)，你会了解到 JuiceFS 被设计成了一种将数据和元数据独立存储的架构，通常来说，数据被存储在以对象存储为主的云存储中，而数据所对应的元数据则被存储在独立的数据库中。

## 存储参数

在创建 JuiceFS 文件系统时，设置数据存储一般涉及以下几个选项：

- `--storage` 指定文件系统要使用的存储服务，例如：`--storage s3`
- `--bucket` 按特定格式指定对象存储的 bucket 地址，例如：`--bucket https://myjuicefs.s3.us-east-2.amazonaws.com`
- `--access-key` 和 `--secret-key` 用来指定访问对象存储的身份认证密钥，需要在相应云平台上创建。

例如，以下命令使用 Amazon S3 对象存储创建文件系统：

```shell
$ juicefs format --storage s3 \
	--bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
	--access-key abcdefghijklmn \
	--secret-key nmlkjihgfedAcBdEfg \
	redis://192.168.1.6/1 \
	my-juice
```

类似的，你可以调整参数，使用几乎所有的公有云/私有云对象存储服务来创建文件系统。

## Access key 和 secret key

一般而言，对象存储服务通过 `access key` 和 `secret key` 验证用户身份，创建文件系统时，除了使用 `--access-key` 和 `--secret-key` 两个选项显式设置以外，还可以通过 `ACCESS_KEY` 和 `SECRET_KEY` 这两个环境变量进行设置。

公有云通常允许用户创建 IAM (Identity and Access Management) 角色，例如： [AWS IAM 角色](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html) 或  [阿里云 RAM 角色](https://help.aliyun.com/document_detail/93689.html)，可将角色分配给 VM 实例。如果云服务器实例已经拥有访问对象存储的权限，则无需设置 `--access-key` 和 `--secret-key` 这两个选项。

## 支持的存储服务

下表列出了 JuiceFS 支持的对象存储服务，点击名称查看设置方法：

> 如果你想要的对象存储不在列表中，欢迎提交需求 [issue](https://github.com/juicedata/juicefs/issues)。

| Name                                   | Value      |
| -------------------------------------- | ---------- |
| [Amazon S3](#aws-s3)                   | `s3`       |
| [Google 云存储](#google-gs)            | `gs`       |
| [Azure Blob 存储](#azure-wasb)         | `wasb`     |
| [Backblaze B2](#backblaze-b2)          | `b2`       |
| [IBM 云对象存储](#ibm-cos)             | `ibmcos`   |
| [Scaleway](#scaleway)                  | `scw`      |
| [DigitalOcean Spaces](#do-space)       | `space`    |
| [Wasabi](#wasabi)                      | `wasabi`   |
| [Storj DCS](#storj-dcs)                | `s3`       |
| [阿里云 OSS](#aliyun-oss)              | `oss`      |
| [腾讯云 COS](#qcloud-cos)              | `cos`      |
| [华为云 OBS](#huawei-obs)              | `obs`      |
| [百度云 BOS](#baidu-bos)               | `bos`      |
| [金山云 KS3](#kingsoft-ks3)            | `ks3`      |
| [美团云 MMS](#meituan-mms)             | `mss`      |
| [网易云 NOS](#163-nos)                 | `nos`      |
| [青云 QingStor](#QingStor)             | `qingstor` |
| [七牛云 Kodo](#kodo)                   | `qiniu`    |
| [新浪云 SCS](#sina-scs)                | `scs`      |
| [天翼云 OOS](#ct-oos)                  | `oos`      |
| [移动云 EOS](#ecloud-eos)              | `eos`      |
| [迅达云 COS](#speedy-cos)              | `speedy`   |
| [优刻得 US3](#ucloud-us3)              | `ufile`    |
| [Ceph RADOS](#ceph-rados)              | `ceph`     |
| [Ceph Object Gateway (RGW)](#ceph-rgw) | `s3`       |
| [Swift](#swift)                        | `swift`    |
| [MinIO](#minio)                        | `minio`    |
| [HDFS](#hdfs)                          | `hdfs`     |
| [Redis](#redis)                        | `redis`    |
| [本地磁盘](#local)                     | `file`     |

## Amazon S3 <span id='aws-s3'></span>

S3 支持  [两种风格的 endpoint URI](https://docs.aws.amazon.com/zh_cn/AmazonS3/latest/userguide/VirtualHosting.html)：`虚拟托管类型` 和 `路径类型`。

- 虚拟托管类型：`https://<bucket>.s3.<region>.amazonaws.com`
- 路径类型：`https://s3.<region>.amazonaws.com/<bucket>`

其中 `<region>` 要替换成实际的区域代码，比如：美国西部（俄勒冈）的区域代码为 `us-west-2`。[点此查看](https://docs.aws.amazon.com/zh_cn/AWSEC2/latest/UserGuide/using-regions-availability-zones.html#concepts-available-regions)所有的区域代码。

**注意**：AWS 中国的用户，应使用 `amazonaws.com.cn` 域名。相应的区域代码信息[点此查看](https://docs.amazonaws.cn/aws/latest/userguide/endpoints-arns.html)。

JuiceFS v0.12 之前的版本仅支持虚拟托管类型。v0.12 以及之后的版本两种风格都支持。因此，在创建文件系统时，`--bucket` 选项既可以使用虚拟托管类型的链接，也可以使用路径类型的链接，例如：

```bash
# 虚拟托管类型
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    ... \
    localhost test
```

```bash
# 路径类型
$ ./juicefs format \
    --storage s3 \
    --bucket https://s3.<region>.amazonaws.com/<bucket> \
    ... \
    localhost test
```

此外，所有 S3 兼容的对象存储服务，都可以通过 `--storage s3` 存储类型进行设定。例如：

```bash
# 虚拟托管类型
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

```bash
# 路径类型
$ ./juicefs format \
    --storage s3 \
    --bucket https://<endpoint>/<bucket> \
    ... \
    localhost test
```

## Google 云存储 <span id='google-gs'></span>

使用 Google 云存储创建 JuiceFS 文件系统时，由于 Google 云存储没有 `Access key` 和 `Secret key`，因此在创建文件系统时可以忽略 `--access-key` 和 `--secret-key` 选项。请查阅 Google Cloud 的文档了解 [身份验证](https://cloud.google.com/docs/authentication) 和 [身份及访问管理 (IAM)](https://cloud.google.com/iam/docs/overview) 相关内容。一般来说，无需额外配置，在使用 Google 云服务器时默认已经拥有云存储的访问权限。

另外，由于 bucket 名称是 [全局唯一](https://cloud.google.com/storage/docs/naming-buckets#considerations) 的，创建文件系统时，`--bucket` 选项中只需指定 bucket 名称即可。例如：

```bash
$ ./juicefs format \
    --storage gs \
    --bucket gs://<bucket> \
    ... \
    localhost test
```

## Azure Blob 存储 <span id='azure-wasb'></span>

使用 Azure Blob 存储创建 JuiceFS 文件系统，除了使用 `--access-key` 和 `--secret-key` 选项之外，你也可以使用 [连接字符串](https://docs.microsoft.com/zh-cn/azure/storage/common/storage-configure-connection-string) 并通过 `AZURE_STORAGE_CONNECTION_STRING` 环境变量进行设定。例如：

```bash
# Use connection string
$ export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=https;AccountName=XXX;AccountKey=XXX;EndpointSuffix=core.windows.net"
$ ./juicefs format \
    --storage wasb \
    --bucket https://<container> \
    ... \
    localhost test
```

> **注意**：Azure China 用户，`EndpointSuffix` 值为 `core.chinacloudapi.cn`。

## Backblaze B2 <span id='backblaze-b2'></span>

使用 Backblaze B2 创建 JuiceFS 文件系统时，你需要先创建  [application key](https://www.backblaze.com/b2/docs/application_keys.html)。**Application Key ID** 和 **Application Key** 分别对应 `Access key` 和 `Secret key`。

`--bucket` 选项可以仅指定 bucket 名称。例如：

```bash
$ ./juicefs format \
    --storage b2 \
    --bucket https://<bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    localhost test
```

## IBM 云对象存储 <span id='ibm-cos'></span>

使用 IBM 云对象存储创建 JuiceFS 文件系统，你首先需要创建 [API key](https://cloud.ibm.com/docs/account?topic=account-manapikey) 和 [instance ID](https://cloud.ibm.com/docs/key-protect?topic=key-protect-retrieve-instance-ID)。**API key** 和 **instance ID** 分别对应 `Access key` 和 `Secret key`。

IBM Cloud Object Storage provides [multiple endpoints](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints) for each region, depends on your network (e.g. public or private network), you should use appropriate endpoint. For example:

IBM 云对象存储为每一个区域提供了 `公网` 和 `内网` 两种 [endpoint 地址](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints)，你可以根据实际需要选用。例如：

```bash
$ ./juicefs format \
    --storage ibmcos \
    --bucket https://<bucket>.<endpoint> \
    --access-key <API-key> \
    --secret-key <instance-ID> \
    ... \
    localhost test
```

## Scaleway <span id='scaleway'></span>

使用 Scaleway 对象存储创建 JuiceFS 文件系统时，请先 [查看文档](https://www.scaleway.com/en/docs/generate-api-keys) 了解如何创建  `Access key` 和 `Secret key`。

`--bucket` 选项的设置格式为 `https://<bucket>.s3.<region>.scw.cloud`，请将其中的  `<region>`  替换成实际的区域代码，例如：荷兰阿姆斯特丹的区域代码是 `nl-ams`。[点此查看](https://www.scaleway.com/en/docs/object-storage-feature/#-Core-Concepts) 所有可用的区域代码。

```bash
$ ./juicefs format \
    --storage scw \
    --bucket https://<bucket>.s3.<region>.scw.cloud \
    ... \
    localhost test
```

## DigitalOcean Spaces <span id='do-space'></span>

使用 DigitalOcean Spaces 创建 JuiceFS 文件系统时，请先 [查看文档](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) 了解如何创建  `Access key` 和 `Secret key`。

`--bucket` 选项的设置格式为 `https://<space-name>.<region>.digitaloceanspaces.com`，请将其中的  `<region>`  替换成实际的区域代码，例如：`nyc3`。[点此查看](https://www.digitalocean.com/docs/spaces/#regional-availability) 所有可用的区域代码。

```bash
$ ./juicefs format \
    --storage space \
    --bucket https://<space-name>.<region>.digitaloceanspaces.com \
    ... \
    localhost test
```

## Wasabi <span id='wasabi'></span>

使用 Wasabi 创建 JuiceFS 文件系统时，请先 [查看文档](https://wasabi-support.zendesk.com/hc/en-us/articles/360019677192-Creating-a-Root-Access-Key-and-Secret-Key) 了解如何创建  `Access key` 和 `Secret key`。

`--bucket` 选项的设置格式为 `https://<bucket>.s3.<region>.wasabisys.com`，请将其中的  `<region>`  替换成实际的区域代码，例如：US East 1 (N. Virginia) 的区域代码为 `us-east-1`。[点此查看](https://wasabi-support.zendesk.com/hc/en-us/articles/360015106031-What-are-the-service-URLs-for-Wasabi-s-different-regions-) 所有可用的区域代码。

```bash
$ ./juicefs format \
    --storage wasabi \
    --bucket https://<bucket>.s3.<region>.wasabisys.com \
    ... \
    localhost test
```

> **提示**：Tokyo (ap-northeast-1) 区域的用户，查看 [这篇文档](https://wasabi-support.zendesk.com/hc/en-us/articles/360039372392-How-do-I-access-the-Wasabi-Tokyo-ap-northeast-1-storage-region-) 了解 endpoint URI 的设置方法。

## Storj DCS <span id='storj-dcs'></span>

使用 Storj DCS 创建 JuiceFS 文件系统时，请先参照 [这篇文档](https://docs.storj.io/api-reference/s3-compatible-gateway) 了解如何创建 `Access key` 和 `Secret key`。

Storj DCS 兼容 AWS S3，`--storage` 使用 `s3` 即可。`--bucket` 选项的设置格式为 `https://gateway.<region>.storjshare.io/<bucket>`，请将 `<region>` 替换成你实际使用的存储区域，目前 DCS 有三个可用存储区域：us1、ap1 和 eu1。

```shell
$ juicefs format \
	--storage s3 \
	--bucket https://gateway.<region>.storjshare.io/<bucket> \
	--access-key <your-access-key> \
	--secret-key <your-sceret-key> \
	redis://localhost/1 my-jfs
```

## 阿里云 OSS <span id='aliyun-oss'></span>

使用阿里云 OSS 创建 JuiceFS 文件系统时，请先参照 [这篇文档](https://help.aliyun.com/document_detail/38738.html) 了解如何创建 `Access key` 和 `Secret key`。如果你已经创建了  [RAM role](https://help.aliyun.com/document_detail/93689.html) 并指派给了云服务器实例，则在创建文件系统时可以忽略 `--access-key` 和 `--secret-key` 选项。

阿里云也支持使用 [Security Token Service (STS)](https://help.aliyun.com/document_detail/100624.html) 作为 OSS 的临时访问身份验证。如果你要使用 STS，请设置  `ALICLOUD_ACCESS_KEY_ID`、`ALICLOUD_ACCESS_KEY_SECRET` 和 `SECURITY_TOKEN ` 环境变量，不要设置 `--access-key` and `--secret-key` 选项。例如：

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

阿里云 OSS 为每个区域都提供了 `公网` 和 `内网` [endpoint 链接](https://help.aliyun.com/document_detail/31834.html)，你可以根据实际的场景选用。如果你在阿里云的服务器上创建文件系统，则无需在 `--bucket` 选项中设置 endpoint 链接，JuiceFS 会自动帮你设置。例如：

```bash
# Running within Alibaba Cloud
$ ./juicefs format \
    --storage oss \
    --bucket https://<bucket> \
    ... \
    localhost test
```

## 腾讯云 COS <span id='qcloud-cos'></span>

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

## 华为云 OBS <span id='huawei-obs'></span>

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

## 百度 BOS <span id='baidu-bos'></span>

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

## 金山云 KS3 <span id='kingsoft-ks3'></span>

Please follow [this document](https://docs.ksyun.com/documents/1386) to learn how to get access key and secret key.

KS3 provides [multiple endpoints](https://docs.ksyun.com/documents/6761) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage ks3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## 美团云 MMS <span id='meituan-mms'></span>

Please follow [this document](https://www.mtyun.com/doc/api/mss/mss/fang-wen-kong-zhi) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<endpoint>`, replace `<endpoint>` with specific value, e.g. `mtmss.com`. You could find all available endpoints at [here](https://www.mtyun.com/doc/products/storage/mss/index#%E5%8F%AF%E7%94%A8%E5%8C%BA%E5%9F%9F). For example:

```bash
$ ./juicefs format \
    --storage mss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## 网易云 NOS <span id='163-nos'></span>

Please follow [this document](https://www.163yun.com/help/documents/55485278220111872) to learn how to get access key and secret key.

NOS provides [multiple endpoints](https://www.163yun.com/help/documents/67078583131230208) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage nos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## 青云 QingStor <span id='QingStor'></span>

Please follow [this document](https://docs.qingcloud.com/qingstor/api/common/signature.html#%E8%8E%B7%E5%8F%96-access-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.qingstor.com`, replace `<region>` with specific region code, e.g. the region code of Beijing 3-A is `pek3a`. You could find all available regions at [here](https://docs.qingcloud.com/qingstor/#%E5%8C%BA%E5%9F%9F%E5%8F%8A%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D). For example:

```bash
$ ./juicefs format \
    --storage qingstor \
    --bucket https://<bucket>.<region>.qingstor.com \
    ... \
    localhost test
```

## 七牛云 Kodo <span id='kodo'></span>

Please follow [this document](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3-<region>.qiniucs.com`, replace `<region>` with specific region code, e.g. the region code of China East is `cn-east-1`. You could find all available regions at [here](https://developer.qiniu.com/kodo/4088/s3-access-domainname). For example:

```bash
$ ./juicefs format \
    --storage qiniu \
    --bucket https://<bucket>.s3-<region>.qiniucs.com \
    ... \
    localhost test
```

## 新浪云 SCS <span id='sina-scs'></span>

Please follow [this document](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.stor.sinaapp.com`. For example:

```bash
$ ./juicefs format \
    --storage scs \
    --bucket https://<bucket>.stor.sinaapp.com \
    ... \
    localhost test
```

## 天翼云 OOS <span id='ct-oos'></span>

Please follow [this document](https://www.ctyun.cn/help2/10000101/10473683) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.oss-<region>.ctyunapi.cn`, replace `<region>` with specific region code, e.g. the region code of Chengdu is `sccd`. You could find all available regions at [here](https://www.ctyun.cn/help2/10000101/10474062). For example:

```bash
$ ./juicefs format \
    --storage oos \
    --bucket https://<bucket>.oss-<region>.ctyunapi.cn \
    ... \
    localhost test
```

## 移动云 EOS <span id='ecloud-eos'></span>

Please follow [this document](https://ecloud.10086.cn/op-help-center/doc/article/24501) to learn how to get access key and secret key.

ECloud Object Storage provides [multiple endpoints](https://ecloud.10086.cn/op-help-center/doc/article/40956) for each region, depends on your network (e.g. public or internal network), you should use appropriate endpoint. For example:

```bash
$ ./juicefs format \
    --storage eos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## 迅达云 COS <span id='speedy-cos'></span>

待补充

## 优刻得 US3 <span id='ucloud-us3'></span>

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

Then compile JuiceFS for Ceph (ensure you have Go 1.14+ and GCC 5.4+):

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

## 本地磁盘 <span id='local'></span>

待编写......