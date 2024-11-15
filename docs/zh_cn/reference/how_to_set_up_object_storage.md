---
title: 如何设置对象存储
sidebar_position: 3
description: JuiceFS 以对象存储作为数据存储，本文介绍 JuiceFS 支持的对象存储以及相应的配置和使用方法。
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

通过阅读 [JuiceFS 的技术架构](../introduction/architecture.md)可以了解到，JuiceFS 是一个数据与元数据分离的分布式文件系统，以对象存储作为主要的数据存储，以 Redis、PostgreSQL、MySQL 等数据库作为元数据存储。

## 存储选项 {#storage-options}

在创建 JuiceFS 文件系统时，设置数据存储一般涉及以下几个选项：

- `--storage` 指定文件系统要使用的存储类型，例如：`--storage s3`。
- `--bucket` 指定存储访问地址，例如：`--bucket https://myjuicefs.s3.us-east-2.amazonaws.com`。
- `--access-key` 和 `--secret-key` 指定访问存储时的身份认证信息。

例如，以下命令使用 Amazon S3 对象存储创建文件系统：

```shell
juicefs format --storage s3 \
    --bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
    --access-key abcdefghijklmn \
    --secret-key nmlkjihgfedAcBdEfg \
    redis://192.168.1.6/1 \
    myjfs
```

## 其他选项 {#other-options}

在执行 `juicefs format` 或 `juicefs mount` 命令时，可以在 `--bucket` 选项中以 URL 参数的形式设置一些特别的选项，比如 `https://myjuicefs.s3.us-east-2.amazonaws.com?tls-insecure-skip-verify=true` 中的 `tls-insecure-skip-verify=true` 即为跳过 HTTPS 请求的证书验证环节。

客户端证书也受支持，因为它们通常用于 mTLS 连接，例如：
`https://myjuicefs.s3.us-east-2.amazonaws.com?ca-certs=./path/to/ca&ssl-cert=./path/to/cert&ssl-key=./path/to/privatekey`

## 配置数据分片（Sharding） {#enable-data-sharding}

创建文件系统时，可以通过 [`--shards`](../reference/command_reference.mdx#format-data-format-options) 选项定义多个 Bucket 作为文件系统的底层存储。这样一来，系统会根据文件名哈希值将文件分散到多个 Bucket 中。数据分片技术可以将大规模数据并发写的负载分散到多个 Bucket 中，从而提高写入性能。

启用数据分片功能需要注意以下事项：

- 只能使用同一种对象存储下的多个 bucket
- `--shards` 选项接受一个 0～256 之间的整数，表示将文件分散到多少个 Bucket 中。默认值为 0，表示不启用数据分片功能。
- 需要使用整型数字通配符 `%d` 或许 `%x` 之类指定用户生成 bucket 的 endpoint 的字符串，例如 `"http://192.168.1.18:9000/myjfs-%d"`，可以按照这样的格式预先创建 bucket，也可以在创建文件系统时由 JuiceFS 客户端自动创建；
- 数据分片在创建时设定，创建完毕不允许修改。不可增加或减少 bucket，也不可以取消 shards 功能。

例如，以下命令创建了一个数据分片为 4 的文件系统：

```shell
juicefs format --storage s3 \
    --shards 4 \
    --bucket "https://myjfs-%d.s3.us-east-2.amazonaws.com" \
    ...
```

执行上述命令后，JuiceFS 客户端会创建 4 个 bucket，分别为 `myjfs-0`、`myjfs-1`、`myjfs-2` 和 `myjfs-3`。

## Access Key 和 Secret Key {#aksk}

一般而言，对象存储通过 Access Key ID 和 Access Key Secret 验证用户身份，对应到 JuiceFS 文件系统就是 `--access-key` 和 `--secret-key` 这两个选项（或者简称为 AK、SK）。

创建文件系统时除了使用 `--access-key` 和 `--secret-key` 两个选项显式指定，更安全的做法是通过 `ACCESS_KEY` 和 `SECRET_KEY` 环境变量传递密钥信息，例如：

```shell
export ACCESS_KEY=abcdefghijklmn
export SECRET_KEY=nmlkjihgfedAcBdEfg
juicefs format --storage s3 \
    --bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
    redis://192.168.1.6/1 \
    myjfs
```

公有云通常允许用户创建 IAM（Identity and Access Management）角色，例如：[AWS IAM 角色](https://docs.aws.amazon.com/zh_cn/IAM/latest/UserGuide/id_roles.html) 或 [阿里云 RAM 角色](https://help.aliyun.com/document_detail/93689.html)，可将角色分配给 VM 实例。如果云服务器实例已经拥有读写对象存储的权限，则无需再指定 `--access-key` 和 `--secret-key`。

## 使用临时访问凭证 {#session-token}

永久访问凭证一般有两个部分：Access Key 和 Secret Key，而临时访问凭证一般包括 3 个部分：Access Key、Secret Key 与 token，并且临时访问凭证具有过期时间，一般在几分钟到几个小时之间。

### 如何获取临时凭证 {#how-to-get-temporary-credentials}

不同云厂商的获取方式不同，一般是需要已具有相应权限用户的 Access Key、Secret Key 以及代表临时访问凭证的权限边界的 ARN 作为参数请求访问云服务厂商的 STS 服务器来获取临时访问凭证。这个过程一般可以由云厂商提供的 SDK 简化操作。比如 Amazon S3 获取临时凭证方式可以参考这个[链接](https://docs.aws.amazon.com/zh_cn/IAM/latest/UserGuide/id_credentials_temp_request.html)，阿里云 OSS 获取临时凭证方式可以参考这个[链接](https://help.aliyun.com/document_detail/100624.html)。

### 如何使用临时访问凭证设置对象存储 {#how-to-set-up-object-storage-with-temporary-access-credentials}

使用临时凭证的方式与使用永久凭证差异不大，在格式化文件系统时，将临时凭证的 Access Key、Secret Key、token 分别通过 `--access-key`、`--secret-key`、`--session-token` 设置值即可。例如：

```bash
juicefs format \
    --storage oss \
    --access-key xxxx \
    --secret-key xxxx \
    --session-token xxxx \
    --bucket https://bucketName.oss-cn-hangzhou.aliyuncs.com \
    redis://localhost:6379/1 \
    test1
```

由于临时凭证很快就会过期，所以关键在于格式化文件系统以后，如何在临时凭证过期前更新 JuiceFS 正在使用的临时凭证。一次凭证更新过程分为两步：

1. 在临时凭证过期前，申请好新的临时凭证；
2. 无需停止正在运行的 JuiceFS，直接使用 `juicefs config Meta-URL --access-key xxxx --secret-key xxxx --session-token xxxx` 命令热更新访问凭证。

新挂载的客户端会直接使用新的凭证，已经在运行的所有客户端也会在一分钟内更新自己的凭证。整个更新过程不会影响正在运行的业务。由于临时凭证过期时间较短，所以以上步骤需要**长期循环执行**才能保证 JuiceFS 服务可以正常访问到对象存储。

## 内网和外网 Endpoint {#internal-and-public-endpoint}

通常情况下，对象存储服务提供统一的 URL 进行访问，但云平台会同时提供内网和外网通信线路，比如满足条件的同平台云服务会自动解析通过内网线路访问对象存储，这样不但时延更低，而且内网通信产生的流量是免费的。

另外，一些云计算平台也区分内外网线路，但没有提供统一访问 URL，而是分别提供内网 Endpoint 和外网 Endpoint 地址。

JuiceFS 对这种区分内网外地址的对象存储服务也做了灵活的支持，对于共享同一个文件系统的场景，在满足条件的服务器上通过内网 Endpoint 访问对象存储，其他计算机通过外网 Endpoint 访问，可以这样使用：

- **创建文件系统时**：`--bucket` 建议使用内网 Endpoint 地址；
- **挂载文件系统时**：对于不满足内网线路的客户端，可以通过 `--bucket` 指定外网 Endpoint 地址。

使用内网 Endpoint 创建文件系统可以确保性能更好、延时更低，对于无法通过内网访问的客户端，可以在挂载文件系统时通过 `--bucket` 指定外网 Endpoint 进行挂载访问。

## 存储类 <VersionAdd>1.1</VersionAdd> {#storage-class}

对象存储通常支持多种存储类，如标准存储、低频访问存储、归档存储。不同的存储类会有不同的价格及服务可用性，你可以在创建 JuiceFS 文件系统时通过 [`--storage-class`](../reference/command_reference.mdx#format-data-storage-options) 选项设置默认的存储类，或者在挂载 JuiceFS 文件系统时通过 [`--storage-class`](../reference/command_reference.mdx#mount-data-storage-options) 选项设置一个新的存储类。请查阅你所使用的对象存储的用户手册了解应该如何设置 `--storage-class` 选项的值（如 [Amazon S3](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html#AmazonS3-PutObject-request-header-StorageClass)）。

:::note 注意
当使用某些存储类（如归档、深度归档）时，数据无法立即访问，需要提前恢复数据并等待一段时间之后才能访问。
:::

:::note 注意
当使用某些存储类（如低频访问）时，会有最小计费单位，读取数据也可能会产生额外的费用，请查阅你所使用的对象存储的用户手册了解详细信息。
:::

## 使用代理 {#using-proxy}

如果客户端所在的网络环境受防火墙策略或其他因素影响需要通过代理访问外部的对象存储服务，使用的操作系统不同，相应的代理设置方法也不同，请参考相应的用户手册进行设置。

以 Linux 为例，可以通过创建 `http_proxy` 和 `https_proxy` 环境变量设置代理：

```shell
export http_proxy=http://localhost:8035/
export https_proxy=http://localhost:8035/
juicefs format \
    --storage s3 \
    ... \
    myjfs
```

## 支持的存储服务 {#supported-object-storage}

如果你希望使用的存储类型不在列表中，欢迎提交需求 [issue](https://github.com/juicedata/juicefs/issues)。

| 名称                                        | 值         |
|:-------------------------------------------:|:----------:|
| [Amazon S3](#amazon-s3)                     | `s3`       |
| [Google 云存储](#google-cloud)              | `gs`       |
| [Azure Blob 存储](#azure-blob-存储)         | `wasb`     |
| [Backblaze B2](#backblaze-b2)               | `b2`       |
| [IBM 云对象存储](#ibm-云对象存储)           | `ibmcos`   |
| [Oracle 云对象存储](#oracle-云对象存储)     | `s3`       |
| [Scaleway](#scaleway)                       | `scw`      |
| [DigitalOcean Spaces](#digitalocean-spaces) | `space`    |
| [Wasabi](#wasabi)                           | `wasabi`   |
| [Storj DCS](#storj-dcs)                     | `s3`       |
| [Vultr 对象存储](#vultr-对象存储)           | `s3`       |
| [Cloudflare R2](#r2)                        | `s3`       |
| [阿里云 OSS](#阿里云-oss)                   | `oss`      |
| [腾讯云 COS](#腾讯云-cos)                   | `cos`      |
| [华为云 OBS](#华为云-obs)                   | `obs`      |
| [百度云 BOS](#百度-bos)                     | `bos`      |
| [火山引擎 TOS](#volcano-engine-tos)         | `tos`      |
| [金山云 KS3](#金山云-ks3)                   | `ks3`      |
| [青云 QingStor](#青云-qingstor)             | `qingstor` |
| [七牛云 Kodo](#七牛云-kodo)                 | `qiniu`    |
| [新浪云 SCS](#新浪云-scs)                   | `scs`      |
| [天翼云 OOS](#天翼云-oos)                   | `oos`      |
| [移动云 EOS](#移动云-eos)                   | `eos`      |
| [京东云 OSS](#京东云-oss)                   | `s3`       |
| [优刻得 US3](#优刻得-us3)                   | `ufile`    |
| [Ceph RADOS](#ceph-rados)                   | `ceph`     |
| [Ceph RGW](#ceph-rgw)                       | `s3`       |
| [Gluster](#gluster)                         | `gluster`  |
| [Swift](#swift)                             | `swift`    |
| [MinIO](#minio)                             | `minio`    |
| [WebDAV](#webdav)                           | `webdav`   |
| [HDFS](#hdfs)                               | `hdfs`     |
| [Apache Ozone](#apache-ozone)               | `s3`       |
| [Redis](#redis)                             | `redis`    |
| [TiKV](#tikv)                               | `tikv`     |
| [etcd](#etcd)                               | `etcd`     |
| [SQLite](#sqlite)                           | `sqlite3`  |
| [MySQL](#mysql)                             | `mysql`    |
| [PostgreSQL](#postgresql)                   | `postgres` |
| [本地磁盘](#本地磁盘)                       | `file`     |
| [SFTP/SSH](#sftp)                           | `sftp`     |
| [NFS](#nfs)                                 | `nfs`      |

### Amazon S3

S3 支持[两种风格的 endpoint URI](https://docs.aws.amazon.com/zh_cn/AmazonS3/latest/userguide/VirtualHosting.html)：「虚拟托管类型」和「路径类型」。区别如下：

- 虚拟托管类型：`https://<bucket>.s3.<region>.amazonaws.com`
- 路径类型：`https://s3.<region>.amazonaws.com/<bucket>`

其中 `<region>` 要替换成实际的区域代码，比如：美国西部（俄勒冈）的区域代码为 `us-west-2`。[点此查看](https://docs.aws.amazon.com/zh_cn/AWSEC2/latest/UserGuide/using-regions-availability-zones.html#concepts-available-regions)所有的区域代码。

:::note 注意
AWS 中国的用户，应使用 `amazonaws.com.cn` 域名。相应的区域代码信息[点此查看](https://docs.amazonaws.cn/aws/latest/userguide/endpoints-arns.html)。
:::

:::note 注意
如果 S3 的桶具有公共访问权限（支持匿名访问），请将 `--access-key` 设置为 `anonymous`。
:::

JuiceFS 中可选择任意一种风格来指定存储桶的地址，例如：

<Tabs groupId="amazon-s3-endpoint">
  <TabItem value="virtual-hosted-style" label="虚拟托管类型">

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    ... \
    myjfs
```

  </TabItem>
  <TabItem value="path-style" label="路径类型">

```bash
juicefs format \
    --storage s3 \
    --bucket https://s3.<region>.amazonaws.com/<bucket> \
    ... \
    myjfs
```

  </TabItem>
</Tabs>

你也可以将 `--storage` 设置为 `s3` 用来连接 S3 兼容的对象存储，比如：

<Tabs groupId="amazon-s3-endpoint">
  <TabItem value="virtual-hosted-style" label="虚拟托管类型">

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

  </TabItem>
  <TabItem value="path-style" label="路径类型">

```bash
juicefs format \
    --storage s3 \
    --bucket https://<endpoint>/<bucket> \
    ... \
    myjfs
```

  </TabItem>
</Tabs>

:::tip 提示
所有 S3 兼容的对象存储服务其 `--bucket` 选项的格式为 `https://<bucket>.<endpoint>` 或者 `https://<endpoint>/<bucket>`，默认的 `region` 为 `us-east-1`，当需要不同的 `region` 的时候，可以通过环境变量 `AWS_REGION` 或者 `AWS_DEFAULT_REGION` 手动设置。
:::

### Google 云存储 {#google-cloud}

Google 云采用 [IAM](https://cloud.google.com/iam/docs/overview) 管理资源的访问权限，通过对[服务账号](https://cloud.google.com/iam/docs/creating-managing-service-accounts#iam-service-accounts-create-gcloud)授权，可以对云服务器、对象存储的访问权限进行精细化的控制。

对于归属于同一服务账号的云服务器和对象存储，只要该账号赋予了相关资源的访问权限，创建 JuiceFS 文件系统时无需提供身份验证信息，云平台会自行完成鉴权。

对于要从谷歌云平台外部访问对象存储的情况，比如要在本地计算机上使用 Google 云存储创建 JuiceFS 文件系统，则需要配置认证信息。由于 Google 云存储并不使用 Access Key ID 和 Access Key Secret，而是通过服务账号的 JSON 密钥文件验证身份。

请参考[「以服务帐号身份进行身份验证」](https://cloud.google.com/docs/authentication/production)为服务账号创建 JSON 密钥文件并下载到本地计算机，通过 `GOOGLE_APPLICATION_CREDENTIALS` 环境变量定义密钥文件的路径，例如：

```shell
export GOOGLE_APPLICATION_CREDENTIALS="$HOME/service-account-file.json"
```

可以把创建环境变量的命令写入 `~/.bashrc` 或 `~/.profile` 让 Shell 在每次启动时自动设置。

配置了传递密钥信息的环境变量以后，在本地和在 Google 云服务器上创建文件系统的命令是完全相同的。例如：

```bash
juicefs format \
    --storage gs \
    --bucket <bucket>[.region] \
    ... \
    myjfs
```

可以看到，命令中无需包含身份验证信息，客户端会通过前面环境变量设置的 JSON 密钥文件完成对象存储的访问鉴权。同时，由于 bucket 名称是 [全局唯一](https://cloud.google.com/storage/docs/naming-buckets#considerations) 的，创建文件系统时，`--bucket` 选项中只需指定 bucket 名称即可。

### Azure Blob 存储

使用 Azure Blob 存储作为 JuiceFS 的数据存储，请先 [查看文档](https://docs.microsoft.com/zh-cn/azure/storage/common/storage-account-keys-manage) 了解如何查看存储帐户的名称和密钥，它们分别对应 `--access-key` 和 `--secret-key` 选项的值。

`--bucket` 选项的设置格式为 `https://<container>.<endpoint>`，请将其中的 `<container>` 替换为实际的 Blob 容器的名称，将 `<endpoint>` 替换为 `core.windows.net`（Azure 全球）或 `core.chinacloudapi.cn`（Azure 中国）。例如：

```bash
juicefs format \
    --storage wasb \
    --bucket https://<container>.<endpoint> \
    --access-key <storage-account-name> \
    --secret-key <storage-account-access-key> \
    ... \
    myjfs
```

除了使用 `--access-key` 和 `--secret-key` 选项之外，你也可以使用 [连接字符串](https://docs.microsoft.com/zh-cn/azure/storage/common/storage-configure-connection-string) 并通过 `AZURE_STORAGE_CONNECTION_STRING` 环境变量进行设定。例如：

```bash
# Use connection string
export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=https;AccountName=XXX;AccountKey=XXX;EndpointSuffix=core.windows.net"
juicefs format \
    --storage wasb \
    --bucket https://<container> \
    ... \
    myjfs
```

:::note 注意
对于 Azure 中国用户，`EndpointSuffix` 的值为 `core.chinacloudapi.cn`。
:::

### Backblaze B2

使用 Backblaze B2 作为 JuiceFS 的数据存储，需要先创建 [application key](https://www.backblaze.com/b2/docs/application_keys.html)，**Application Key ID** 和 **Application Key** 分别对应 Access Key 和 Secret Key。

Backblaze B2 支持两种访问接口：B2 原生 API 和 S3 兼容 API。

#### B2 原生 API

存储类型应设置为 `b2`，`--bucket` 只需设置 bucket 名称。例如：

```bash
juicefs format \
    --storage b2 \
    --bucket <bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    myjfs
```

#### S3 兼容 API

存储类型应设置为 `s3`，`--bucket` 应指定完整的 bucket 地址。例如：

```bash
juicefs format \
    --storage s3 \
    --bucket https://s3.eu-central-003.backblazeb2.com/<bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    myjfs
```

### IBM 云对象存储

使用 IBM 云对象存储创建 JuiceFS 文件系统，你首先需要创建 [API key](https://cloud.ibm.com/docs/account?topic=account-manapikey) 和 [instance ID](https://cloud.ibm.com/docs/key-protect?topic=key-protect-retrieve-instance-ID)。**API key** 和 **instance ID** 分别对应 Access Key 和 Secret Key。

IBM 云对象存储为每一个区域提供了 `公网` 和 `内网` 两种 [endpoint 地址](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints)，你可以根据实际需要选用。例如：

```bash
juicefs format \
    --storage ibmcos \
    --bucket https://<bucket>.<endpoint> \
    --access-key <API-key> \
    --secret-key <instance-ID> \
    ... \
    myjfs
```

### Oracle 云对象存储

Oracle 云对象存储支持 S3 兼容的形式进行访问，详细请参考[官方文档](https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/s3compatibleapi.htm)。

该对象存储的 `endpoint` 格式为：`${namespace}.compat.objectstorage.${region}.oraclecloud.com`，例如：

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

### Scaleway

使用 Scaleway 对象存储作为 JuiceFS 数据存储，请先 [查看文档](https://www.scaleway.com/en/docs/generate-api-keys) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的设置格式为 `https://<bucket>.s3.<region>.scw.cloud`，请将其中的 `<region>` 替换成实际的区域代码，例如：荷兰阿姆斯特丹的区域代码是 `nl-ams`。[点此查看](https://www.scaleway.com/en/docs/object-storage-feature/#-Core-Concepts) 所有可用的区域代码。

```bash
juicefs format \
    --storage scw \
    --bucket https://<bucket>.s3.<region>.scw.cloud \
    ... \
    myjfs
```

### DigitalOcean Spaces

使用 DigitalOcean Spaces 作为 JuiceFS 数据存储，请先 [查看文档](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的设置格式为 `https://<space-name>.<region>.digitaloceanspaces.com`，请将其中的 `<region>` 替换成实际的区域代码，例如：`nyc3`。[点此查看](https://www.digitalocean.com/docs/spaces/#regional-availability) 所有可用的区域代码。

```bash
juicefs format \
    --storage space \
    --bucket https://<space-name>.<region>.digitaloceanspaces.com \
    ... \
    myjfs
```

### Wasabi

使用 Wasabi 作为 JuiceFS 数据存储，请先 [查看文档](https://wasabi-support.zendesk.com/hc/en-us/articles/360019677192-Creating-a-Root-Access-Key-and-Secret-Key) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的设置格式为 `https://<bucket>.s3.<region>.wasabisys.com`，请将其中的  `<region>`  替换成实际的区域代码，例如：US East 1 (N. Virginia) 的区域代码为 `us-east-1`。[点此查看](https://wasabi-support.zendesk.com/hc/en-us/articles/360.15.26031-What-are-the-service-URLs-for-Wasabi-s-different-regions-) 所有可用的区域代码。

```bash
juicefs format \
    --storage wasabi \
    --bucket https://<bucket>.s3.<region>.wasabisys.com \
    ... \
    myjfs
```

:::note 注意
Tokyo (ap-northeast-1) 区域的用户，查看 [这篇文档](https://wasabi-support.zendesk.com/hc/en-us/articles/360039372392-How-do-I-access-the-Wasabi-Tokyo-ap-northeast-1-storage-region-) 了解 endpoint URI 的设置方法。
:::

### Storj DCS

使用 Storj DCS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://docs.storj.io/api-reference/s3-compatible-gateway) 了解如何创建 Access Key 和 Secret Key。

Storj DCS 兼容 AWS S3，存储类型使用 `s3` ，`--bucket` 格式为 `https://gateway.<region>.storjshare.io/<bucket>`。`<region>` 为存储区域，目前 DCS 有三个可用存储区域：us1、ap1 和 eu1。

```shell
juicefs format \
    --storage s3 \
    --bucket https://gateway.<region>.storjshare.io/<bucket> \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

:::caution 特别提示
因为 Storj DCS 的 [ListObjects](https://github.com/storj/gateway-st/blob/main/docs/s3-compatibility.md#listobjects) API 并非完全 S3 兼容（返回结果没有实现排序功能），所以 JuiceFS 的部分功能无法使用，比如 `juicefs gc`，`juicefs fsck`，`juicefs sync`，`juicefs destroy`。另外，使用 `juicefs mount` 时需要关闭[元数据自动备份](../administration/metadata_dump_load.md#backup-automatically)功能，即加上 `--backup-meta 0`。
:::

### Vultr 对象存储

Vultr 的对象存储兼容 S3 API，存储类型使用 `s3`，`--bucket` 格式为 `https://<bucket>.<region>.vultrobjects.com/`。例如：

```shell
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.ewr1.vultrobjects.com/ \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

访问对象存储的 API 密钥可以在 [管理控制台](https://my.vultr.com/objectstorage) 中找到。

### Cloudflare R2 {#r2}

R2 是 Cloudflare 的对象存储服务，提供 S3 兼容的 API，因此用法与 Amazon S3 基本一致。请参照[文档](https://developers.cloudflare.com/r2/data-access/s3-api/tokens)了解如何创建 Access Key 和 Secret Key。

```shell
juicefs format \
    --storage s3 \
    --bucket https://<ACCOUNT_ID>.r2.cloudflarestorage.com/myjfs \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

对于生产环境，建议通过 `ACCESS_KEY` 和 `SECRET_KEY` 环境变量传递密钥信息，例如：

```shell
export ACCESS_KEY=<your-access-key>
export SECRET_KEY=<your-sceret-key>
juicefs format \
    --storage s3 \
    --bucket https://<ACCOUNT_ID>.r2.cloudflarestorage.com/myjfs \
    ... \
    myjfs
```

:::caution 特别提示
因为 Cloudflare R2 的 `ListObjects` API 并非完全 S3 兼容（返回结果没有实现排序功能），所以 JuiceFS 的部分功能无法使用，比如 `juicefs gc`、`juicefs fsck`、`juicefs sync`、`juicefs destroy`。另外，使用 `juicefs mount` 时需要关闭[元数据自动备份](../administration/metadata_dump_load.md#backup-automatically)功能，即加上 `--backup-meta 0`。
:::

### 阿里云 OSS

使用阿里云 OSS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://help.aliyun.com/document_detail/38738.html) 了解如何创建 Access Key 和 Secret Key。如果你已经创建了 [RAM 角色](https://help.aliyun.com/document_detail/93689.html) 并指派给了云服务器实例，则在创建文件系统时可以忽略 `--access-key` 和 `--secret-key` 选项。

阿里云也支持使用 [Security Token Service (STS)](https://help.aliyun.com/document_detail/100624.html) 作为 OSS 的临时访问身份验证。如果你要使用 STS，请设置  `ALICLOUD_ACCESS_KEY_ID`、`ALICLOUD_ACCESS_KEY_SECRET` 和 `SECURITY_TOKEN` 环境变量，不要设置 `--access-key` and `--secret-key` 选项。例如：

```bash
# Use Security Token Service (STS)
export ALICLOUD_ACCESS_KEY_ID=XXX
export ALICLOUD_ACCESS_KEY_SECRET=XXX
export SECURITY_TOKEN=XXX
juicefs format \
    --storage oss \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

阿里云 OSS 为每个区域都提供了 `公网` 和 `内网` [endpoint 链接](https://help.aliyun.com/document_detail/31834.html)，你可以根据实际的场景选用。

如果你是在阿里云的服务器上创建文件系统，可以在 `--bucket` 选项中直接指定 bucket 名称。例如：

```bash
# 在阿里云中运行
juicefs format \
    --storage oss \
    --bucket <bucket> \
    ... \
    myjfs
```

### 腾讯云 COS

使用腾讯云 COS 作为 JuiceFS 数据存储，Bucket 名称格式为 `<bucket>-<APPID>`，即需要在 bucket 名称后面指定 `APPID`，[点此查看](https://cloud.tencent.com/document/product/436/13312) 如何获取  `APPID` 。

`--bucket` 选项的完整格式为 `https://<bucket>-<APPID>.cos.<region>.myqcloud.com`，请将 `<region>` 替换成你实际使用的存储区域，例如：上海的区域代码为 `ap-shanghai`。[点此查看](https://cloud.tencent.com/document/product/436/6224) 所有可用的区域代码。例如：

```bash
juicefs format \
    --storage cos \
    --bucket https://<bucket>-<APPID>.cos.<region>.myqcloud.com \
    ... \
    myjfs
```

如果你是在腾讯云的服务器上创建文件系统，可以在 `--bucket` 选项中直接指定 bucket 名称。例如：

```bash
# 在腾讯云中运行
juicefs format \
    --storage cos \
    --bucket <bucket>-<APPID> \
    ... \
    myjfs
```

### 华为云 OBS

使用华为云 OBS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://support.huaweicloud.com/usermanual-ca/zh-cn_topic_0046606340.html) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的格式为 `https://<bucket>.obs.<region>.myhuaweicloud.com`，请将 `<region>` 替换成你实际使用的存储区域，例如：北京一的区域代码为 `cn-north-1`。[点此查看](https://developer.huaweicloud.com/endpoint?OBS) 所有可用的区域代码。例如：

```bash
juicefs format \
    --storage obs \
    --bucket https://<bucket>.obs.<region>.myhuaweicloud.com \
    ... \
    myjfs
```

如果是你在华为云的服务器上创建文件系统，可以在 `--bucket` 直接指定 bucket 名称。例如：

```bash
# 在华为云中运行
juicefs format \
    --storage obs \
    --bucket <bucket> \
    ... \
    myjfs
```

### 百度 BOS

使用百度云 BOS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://cloud.baidu.com/doc/Reference/s/9jwvz2egb) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的格式为 `https://<bucket>.<region>.bcebos.com`，请将 `<region>` 替换成你实际使用的存储区域，例如：北京的区域代码为 `bj`。[点此查看](https://cloud.baidu.com/doc/BOS/s/Ck1rk80hn#%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D%EF%BC%88endpoint%EF%BC%89) 所有可用的区域代码。例如：

```bash
juicefs format \
    --storage bos \
    --bucket https://<bucket>.<region>.bcebos.com \
    ... \
    myjfs
```

如果你是在百度云的服务器上创建文件系统，可以在 `--bucket` 直接指定 bucket 名称。例如：

```bash
# 在百度云中运行
juicefs format \
    --storage bos \
    --bucket <bucket> \
    ... \
    myjfs
```

### 火山引擎 TOS <VersionAdd>1.0.3</VersionAdd> {#volcano-engine-tos}

使用火山引擎 TOS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://www.volcengine.com/docs/6291/65568) 了解如何创建 Access Key 和 Secret Key。

火山引擎 TOS 为每个区域都提供了公网和内网 [endpoint 链接](https://www.volcengine.com/docs/6349/107356)，你可以根据实际的场景选用。

```bash
juicefs format \
    --storage tos \
    --bucket https://<bucket>.<endpoint>\
    ... \
    myjfs
```

### 金山云 KS3

使用金山云 KS3 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://docs.ksyun.com/documents/1386) 了解如何创建 Access Key 和 Secret Key。

金山云 KS3 为每个区域都提供了公网和内网 [endpoint 链接](https://docs.ksyun.com/documents/6761)，你可以根据实际的场景选用。

```bash
juicefs format \
    --storage ks3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### 青云 QingStor

使用青云 QingStor 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://docsv3.qingcloud.com/storage/object-storage/api/practices/signature/#%E8%8E%B7%E5%8F%96-access-key) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的格式为 `https://<bucket>.<region>.qingstor.com`，请将 `<region>` 替换成你实际使用的存储区域，例如：北京 3-A 的区域代码为 `pek3a`。[点此查看](https://docs.qingcloud.com/qingstor/#%E5%8C%BA%E5%9F%9F%E5%8F%8A%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D) 所有可用的区域代码。例如：

```bash
juicefs format \
    --storage qingstor \
    --bucket https://<bucket>.<region>.qingstor.com \
    ... \
    myjfs
```

:::note 注意
所有 QingStor 兼容的对象存储服务其 `--bucket` 选项的格式为 `http://<bucket>.<endpoint>`。
:::

### 七牛云 Kodo

使用七牛云 Kodo 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的格式为 `https://<bucket>.s3-<region>.qiniucs.com`，请将 `<region>` 替换成你实际使用的存储区域，例如：中国东部的区域代码为 `cn-east-1`。[点此查看](https://developer.qiniu.com/kodo/4088/s3-access-domainname) 所有可用的区域代码。例如：

```bash
juicefs format \
    --storage qiniu \
    --bucket https://<bucket>.s3-<region>.qiniucs.com \
    ... \
    myjfs
```

### 新浪云 SCS

使用新浪云 SCS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项格式为 `https://<bucket>.stor.sinaapp.com`。例如：

```bash
juicefs format \
    --storage scs \
    --bucket https://<bucket>.stor.sinaapp.com \
    ... \
    myjfs
```

### 天翼云 OOS

使用天翼云 OOS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://www.ctyun.cn/help2/10000101/10473683) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的格式为 `https://<bucket>.<endpoint>`，例如：

```bash
juicefs format \
    --storage oos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### 移动云 EOS

使用移动云 EOS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://ecloud.10086.cn/op-help-center/doc/article/24501) 了解如何创建 Access Key 和 Secret Key。

移动云 EOS 为每个区域都提供了 `公网` 和 `内网` [endpoint 链接](https://ecloud.10086.cn/op-help-center/doc/article/40956)，你可以根据实际的场景选用。例如：

```bash
juicefs format \
    --storage eos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### 京东云 OSS

使用京东云 OSS 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://docs.jdcloud.com/cn/account-management/accesskey-management) 了解如何创建 Access Key 和 Secret Key。

`--bucket` 选项的格式为 `https://<bucket>.<region>.jdcloud-oss.com`，请将 `<region>` 替换成你实际使用的存储区域，区域代码[点此查看](https://docs.jdcloud.com/cn/object-storage-service/oss-endpont-list) 。例如：

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<region>.jdcloud-oss.com \
    ... \
    myjfs
```

### 优刻得 US3

使用优刻得 US3 作为 JuiceFS 数据存储，请先参照 [这篇文档](https://docs.ucloud.cn/uai-censor/access/key) 了解如何创建 Access Key 和 Secret Key。

优刻得 US3（原名 UFile）为每个区域都提供了 `公网` 和 `内网` [endpoint 链接](https://docs.ucloud.cn/ufile/introduction/region)，你可以根据实际的场景选用。例如：

```bash
juicefs format \
    --storage ufile \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### Ceph RADOS

:::note
JuiceFS v1.0 使用的 `go-ceph` 库版本为 v0.4.0，其支持的 Ceph 最低版本为 Luminous（v12.2.*）。
JuiceFS v1.1 使用的 `go-ceph` 库版本为 v0.18.0，其支持的 Ceph 最低版本为 Octopus（v15.2.*）。
使用前请确认 JuiceFS 与使用的 Ceph 和 `librados` 版本是否匹配，详见 [`go-ceph`](https://github.com/ceph/go-ceph#supported-ceph-versions)、[`librados`](https://docs.ceph.com/en/quincy/rados/api/librados-intro/)。
:::

[Ceph 存储集群](https://docs.ceph.com/en/latest/rados) 具有消息传递层协议，该协议使客户端能够与 Ceph Monitor 和 Ceph OSD 守护程序进行交互。[`librados`](https://docs.ceph.com/en/latest/rados/api/librados-intro) API 使您可以与这两种类型的守护程序进行交互：

- [Ceph Monitor](https://docs.ceph.com/en/latest/rados/configuration/common/#monitors) 维护群集映射的主副本
- [Ceph OSD Daemon (OSD)](https://docs.ceph.com/en/latest/rados/configuration/common/#osds) 将数据作为对象存储在存储节点上

JuiceFS 支持使用基于 `librados` 的本地 Ceph API。您需要分别安装 `librados` 库并重新编译 `juicefs` 二进制文件。

首先安装 `librados`，建议使用匹配你的 Ceph 版本的 `librados`，例如 Ceph 版本是 Octopus（v15.2.x），那么 `librados` 也建议使用 v15.2.x 版本。

<Tabs>
  <TabItem value="debian" label="Debian 及衍生版本">

```bash
sudo apt-get install librados-dev
```

  </TabItem>
  <TabItem value="centos" label="RHEL 及衍生版本">

```bash
sudo yum install librados2-devel
```

  </TabItem>
</Tabs>

然后为 Ceph 编译 JuiceFS（要求 Go 1.20+ 和 GCC 5.4+）：

```bash
make juicefs.ceph
```

在使用 Ceph 时，原本 JuiceFS 客户端的对象存储参数的含义不太相同：

* `--bucket` 是 Ceph 存储池，格式为 `ceph://<pool-name>`，[存储池](https://docs.ceph.com/zh_CN/latest/rados/operations/pools)是用于存储对象的逻辑分区，使用前需要先创建好
* `--access-key` 选项的值是 Ceph 集群名称，默认集群名称是 `ceph`。
* `--secret-key` 选项的值是 [Ceph 客户端用户名](https://docs.ceph.com/en/latest/rados/operations/user-management)，默认用户名是 `client.admin`。

为了连接到 Ceph Monitor，`librados` 将通过搜索默认位置读取 Ceph 的配置文件，并使用找到的第一个。这些位置是：

- `CEPH_CONF` 环境变量
- `/etc/ceph/ceph.conf`
- `~/.ceph/config`
- 在当前工作目录中的 `ceph.conf`

创建一个文件系统：

```bash
juicefs.ceph format \
    --storage ceph \
    --bucket ceph://<pool-name> \
    --access-key <cluster-name> \
    --secret-key <user-name> \
    ... \
    myjfs
```

### Ceph RGW

[Ceph Object Gateway](https://ceph.io/ceph-storage/object-storage) 是在 `librados` 之上构建的对象存储接口，旨在为应用程序提供访问 Ceph 存储集群的 RESTful 网关。Ceph 对象网关支持 S3 兼容的接口，因此我们可以将 `--storage` 设置为 `s3`。

`--bucket` 选项的格式为 `http://<bucket>.<endpoint>`（虚拟托管类型），例如：

```bash
juicefs format \
    --storage s3 \
    --bucket http://<bucket>.<endpoint> \
    ... \
    myjfs
```

### Gluster

[Gluster](https://github.com/gluster/glusterfs) 是一款开源的软件定义分布式存储，单集群能支持 PiB 级别的数据。JuiceFS 通过 `libgfapi` 库与 Gluster 集群交互，使用前需要单独编译。

首先安装 `libgfapi`（版本范围 6.0 - 10.1, [10.4+ 暂不支持](https://github.com/juicedata/juicefs/issues/4043))：

<Tabs>
  <TabItem value="debian" label="Debian 及衍生版本">

```bash
sudo apt-get install uuid-dev libglusterfs-dev glusterfs-common
```

  </TabItem>
  <TabItem value="centos" label="RHEL 及衍生版本">

```bash
sudo yum install glusterfs glusterfs-api-devel glusterfs-libs
```

  </TabItem>
</Tabs>

然后编译支持 Gluster 的 JuiceFS：

```bash
make juicefs.gluster
```

现在我们可以创建出基于 Gluster 的 JuiceFS volume：

```bash
juicefs format \
    --storage gluster \
    --bucket host1,host2,host3/gv0 \
    ... \
    myjfs
```

其中 `--bucket` 选项格式为 `<host[,host...]>/<volume_name>`。注意这里的 `volume_name` 为 Gluster 中的卷名称，与 JuiceFS volume 自身的名字没有直接关系。

### Swift

[OpenStack Swift](https://github.com/openstack/swift) 是一种分布式对象存储系统，旨在从一台计算机扩展到数千台服务器。Swift 已针对多租户和高并发进行了优化。Swift 广泛适用于备份、Web 和移动内容的理想选择，可以无限量存储任何非结构化数据。

`--bucket` 选项格式为 `http://<container>.<endpoint>`，`container` 用来设定对象的命名空间。

**当前，JuiceFS 仅支持  [Swift V1 authentication](https://www.swiftstack.com/docs/cookbooks/swift_usage/auth.html)。**

`--access-key` 选项的值是用户名，`--secret-key` 选项的值是密码。例如：

```bash
juicefs format \
    --storage swift \
    --bucket http://<container>.<endpoint> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

### MinIO

[MinIO](https://min.io) 是开源的轻量级对象存储，兼容 Amazon S3 API。

使用 Docker 可以很容易地在本地运行一个 MinIO 实例。例如，以下命令通过 `--console-address ":9900"` 为控制台设置并映射了 `9900` 端口，还将 MinIO 的数据路径映射到了当前目录下的 `minio-data` 文件夹中，你可以按需修改这些参数：

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

容器创建成功以后使用以下地址访问：

- **MinIO API**：[http://127.0.0.1:9000](http://127.0.0.1:9000)，这也是 JuiceFS 访问对象存储时所使用的的 API
- **MinIO 管理界面**：[http://127.0.0.1:9900](http://127.0.0.1:9900)，用于管理对象存储本身，与 JuiceFS 无关

对象存储初始的 Access Key 和 Secret Key 均为 `minioadmin`。

使用 MinIO 作为 JuiceFS 的数据存储，`--storage` 选项设置为 `minio`。

```bash
juicefs format \
    --storage minio \
    --bucket http://127.0.0.1:9000/<bucket> \
    --access-key minioadmin \
    --secret-key minioadmin \
    ... \
    myjfs
```

:::note

1. 当前，JuiceFS 仅支持路径风格的 MinIO URI 地址，例如：`http://127.0.0.1:9000/myjfs`
1. `MINIO_REGION` 环境变量可以用于设置 MinIO 的 region，如果不设置，默认为 `us-east-1`
1. 面对多节点 MinIO 集群，考虑在 Endpoint 中使用 DNS 域名，解析到各个 MinIO 节点，作为简易负载均衡，比如 `http://minio.example.com:9000/myjfs`
:::

### WebDAV

[WebDAV](https://en.wikipedia.org/wiki/WebDAV) 是 HTTP 的扩展协议，有利于用户间协同编辑和管理存储在万维网服务器的文档。JuiceFS 0.15+ 支持使用 WebDAV 协议的存储系统作为后端数据存储。

你需要将 `--storage` 设置为 `webdav`，并通过 `--bucket` 来指定访问 WebDAV 的地址。如果存储系统启用了用户验证，用户名和密码可以通过 `--access-key` 和 `--secret-key` 来指定，例如：

```bash
juicefs format \
    --storage webdav \
    --bucket http://<endpoint>/ \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

### HDFS

Hadoop 的文件系统 [HDFS](https://hadoop.apache.org) 也可以作为对象存储供 JuiceFS 使用。

当使用 HDFS 作为 JuiceFS 数据存储，`--access-key` 的值设置为用户名，默认的超级用户通常是 `hdfs`。例如：

```bash
juicefs format \
    --storage hdfs \
    --bucket namenode1:8020 \
    --access-key hdfs \
    ... \
    myjfs
```

如果在创建文件系统时不指定 `--access-key`，JuiceFS 会使用执行 `juicefs mount` 命令的用户身份或通过 Hadoop SDK 访问 HDFS 的用户身份。如果该用户没有 HDFS 的读写权限，则程序会失败挂起，发生 IO 错误。

JuiceFS 会尝试基于 `$HADOOP_CONF_DIR` 或 `$HADOOP_HOME` 为 HDFS 客户端加载配置。如果 `--bucket` 选项留空，将使用在 Hadoop 配置中找到的默认 HDFS。

bucket 参数支持格式如下：

- `[hdfs://]namenode:port[/path]`

对于 HA 集群，bucket 参数可以：

- `[hdfs://]namenode1:port,namenode2:port[/path]`
- `[hdfs://]nameservice[/path]`

对于启用 Kerberos 的 HDFS，可以通过 `KRB5KEYTAB` 和 `KRB5PRINCIPAL` 环境变量来指定 keytab 和 principal。

### Apache Ozone

Apache Ozone 是 Hadoop 的分布式对象存储系统，提供了 S3 兼容的 API。所以可以通过 S3 兼容的模式作为对象存储供 JuiceFS 使用。例如：

```bash
juicefs format \
    --storage s3 \
    --bucket http://<endpoint>/<bucket>\
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

### Redis

Redis 既可以作为 JuiceFS 的元数据存储，也可以作为数据存储，但当使用 Redis 作为数据存储时，建议不要存储大规模数据。

#### 单机模式

`--bucket` 选项格式为 `redis://<host>:<port>/<db>`。`--access-key` 选项的值是用户名，`--secret-key` 选项的值是密码。例如：

```bash
juicefs format \
    --storage redis \
    --bucket redis://<host>:<port>/<db> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

#### Redis Sentinel

Redis Sentinel 模式下，`--bucket` 选项格式为 `redis[s]://MASTER_NAME,SENTINEL_ADDR[,SENTINEL_ADDR]:SENTINEL_PORT[/DB]`。Sentinel 的密码则需要通过 `SENTINEL_PASSWORD_FOR_OBJ` 环境变量来声明。例如：

```bash
export SENTINEL_PASSWORD_FOR_OBJ=sentinel_password
juicefs format \
    --storage redis \
    --bucket redis://masterName,1.2.3.4,1.2.5.6:26379/2  \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

#### Redis 集群

Redis 集群模式下，`--bucket` 选项格式为 `redis[s]://ADDR:PORT,[ADDR:PORT],[ADDR:PORT]`。例如：

```bash
juicefs format \
    --storage redis \
    --bucket redis://127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002  \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

### TiKV

[TiKV](https://tikv.org) 是一个高度可扩展、低延迟且易于使用的键值数据库。它提供原始和符合 ACID 的事务键值 API。

TiKV 既可以用作 JuiceFS 的元数据存储，也可以用于 JuiceFS 的数据存储。

:::note 注意
建议使用独立部署的 TiKV 5.0+ 集群作为 JuiceFS 的数据存储
:::

`--bucket` 选项格式类似 `<host>:<port>,<host>:<port>,<host>:<port>`，其中 `<host>` 是 Placement Driver（PD）的地址。`--access-key` 和 `--secret-key` 选项没有作用，可以省略。例如：

```bash
juicefs format \
    --storage tikv \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>" \
    ... \
    myjfs
```

:::note 注意
不要使用同一个 TiKV 集群来存储元数据和数据，因为 JuiceFS 是使用不同的协议来存储元数据（支持事务的 TxnKV) 和数据 (不支持事务的 RawKV)，TxnKV 的对象名会被编码后存储，即使添加了不同的前缀也可能导致它们的名字冲突。另外，建议启用 [Titan](https://tikv.org/docs/latest/deploy/configure/titan) 来提升存储数据的集群的性能。
:::

#### 设置 TLS

如果需要开启 TLS，可以通过在 Bucket URL 后以添加 query 参数的形式设置 TLS 的配置项，目前支持的配置项：

| 配置项      | 值                                                                                                                                                                                             |
|-------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ca`        | CA 根证书，用于用 TLS 连接 TiKV/PD                                                                                                                                                             |
| `cert`      | 证书文件路径，用于用 TLS 连接 TiKV/PD                                                                                                                                                          |
| `key`       | 私钥文件路径，用于用 TLS 连接 TiKV/PD                                                                                                                                                          |
| `verify-cn` | 证书通用名称，用于验证调用者身份，[详情](https://docs.pingcap.com/zh/tidb/dev/enable-tls-between-components#%E8%AE%A4%E8%AF%81%E7%BB%84%E4%BB%B6%E8%B0%83%E7%94%A8%E8%80%85%E8%BA%AB%E4%BB%BD) |

例子：

```bash
juicefs format \
    --storage tikv \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>?ca=/path/to/ca.pem&cert=/path/to/tikv-server.pem&key=/path/to/tikv-server-key.pem&verify-cn=CN1,CN2" \
    ... \
    myjfs
```

### etcd

[etcd](https://etcd.io) 是一个高可用高可靠的小规模键值数据库，既可以用作 JuiceFS 的元数据存储，也可以用于 JuiceFS 的数据存储。

etcd 默认会[限制](https://etcd.io/docs/latest/dev-guide/limit)单个请求不能超过 1.5MB，需要将 JuiceFS 的分块大小（`--block-size` 选项）改成 1MB 甚至更低。

`--bucket` 选项需要填 etcd 的地址，格式类似 `<host1>:<port>,<host2>:<port>,<host3>:<port>`。`--access-key` 和 `--secret-key` 选项填用户名和密码，当 etcd 没有启用用户认证时可以省略。例如：

```bash
juicefs format \
    --storage etcd \
    --block-size 1024 \  # 这个选项非常重要
    --bucket "<host1>:<port>,<host2>:<port>,<host3>:<port>/prefix" \
    --access-key myname \
    --secret-key mypass \
    ... \
    myjfs
```

#### 设置 TLS

如果需要开启 TLS，可以通过在 Bucket URL 后以添加 query 参数的形式设置 TLS 的配置项，目前支持的配置项：

| 配置项                 | 值           |
|------------------------|--------------|
| `cacert`               | CA 根证书    |
| `cert`                 | 证书文件路径 |
| `key`                  | 私钥文件路径 |
| `server-name`          | 服务器名称   |
| `insecure-skip-verify` | 1            |

例子：

```bash
juicefs format \
    --storage etcd \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>?cacert=/path/to/ca.pem&cert=/path/to/server.pem&key=/path/to/key.pem&server-name=etcd" \
    ... \
    myjfs
```

:::note 注意
证书的路径需要使用绝对路径，并且确保所有需要挂载的机器上能用该路径访问到它们。
:::

### SQLite

[SQLite](https://sqlite.org) 是全球广泛使用的小巧、快速、单文件、可靠、全功能的单文件 SQL 数据库引擎。

使用 SQLite 作为数据存储时只需要指定它的绝对路径即可。

```shell
juicefs format \
    --storage sqlite3 \
    --bucket /path/to/sqlite3.db \
    ... \
    myjfs
```

:::note 注意
由于 SQLite 是一款嵌入式数据库，只有数据库所在的主机可以访问它，不能用于多机共享场景。如果格式化时使用的是相对路径，会导致挂载时出问题，请使用绝对路径。
:::

### MySQL

[MySQL](https://www.mysql.com) 是受欢迎的开源关系型数据库之一，常被作为 Web 应用程序的首选数据库，既可以作为 JuiceFS 的元数据引擎也可以用来存储文件数据。跟 MySQL 兼容的 [MariaDB](https://mariadb.org)、[TiDB](https://github.com/pingcap/tidb) 等都可以用来作为数据存储。

使用 MySQL 作为数据存储时，需要提前创建数据库并添加想要权限，通过 `--bucket` 选项指定访问地址，通过 `--access-key` 选项指定用户名，通过 `--secret-key` 选项指定密码，示例如下：

```shell
juicefs format \
    --storage mysql \
    --bucket (<host>:3306)/<database-name> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

创建文件系统后，JuiceFS 会在该数据库中创建名为 `jfs_blob` 的表用来存储数据。

:::note 注意
不要漏掉 `--bucket` 参数里的括号 `()`。
:::

### PostgreSQL

[PostgreSQL](https://www.postgresql.org) 是功能强大的开源关系型数据库，有完善的生态和丰富的应用场景，既可以作为 JuiceFS 的元数据引擎也可以作为数据存储。其他跟 PostgreSQL 协议兼容的数据库（比如 [CockroachDB](https://github.com/cockroachdb/cockroach) 等) 也可以用来作为数据存储。

创建文件系统时需要先创建好数据库并添加相应读写权限，使用 `--bucket` 选项来指定数据的地址，使用 `--access-key` 选项指定用户名，使用 `--secret-key` 选项指定密码，示例如下：

```shell
juicefs format \
    --storage postgres \
    --bucket <host>:<port>/<db>[?parameters] \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

创建文件系统后，JuiceFS 会在该数据库中创建名为 `jfs_blob` 的表用来存储数据。

#### 故障排除

JuiceFS 客户端默认采用 SSL 加密连接 PostgreSQL，如果连接时报错 `pq: SSL is not enabled on the server` 说明数据库没有启用 SSL。可以根据业务场景为 PostgreSQL 启用 SSL 加密，也可以在 bucket URL 中添加参数 `sslmode=disable` 禁用加密验证。

### 本地磁盘

在创建 JuiceFS 文件系统时，如果没有指定任何存储类型，会默认使用本地磁盘作为数据存储，root 用户默认存储路径为 `/var/jfs`，普通用户默认存储路径为 `~/.juicefs/local`。

例如，以下命令使用本地的 Redis 数据库和本地磁盘创建了一个名为 `myfs` 的文件系统：

```shell
juicefs format redis://localhost:6379/1 myjfs
```

本地存储通常仅用于了解和体验 JuiceFS 的基本功能，创建的 JuiceFS 存储无法被网络内的其他客户端挂载，只能单机使用。

### SFTP/SSH {#sftp}

SFTP 全称 Secure File Transfer Protocol 即安全文件传输协议，它并不是文件存储。准确来说，JuiceFS 是通过 SFTP/SSH 这种文件传输协议对远程主机上的磁盘进行连接和读写，从而让任何启用了 SSH 服务的操作系统都可以作为 JuiceFS 的数据存储来使用。

例如，以下命令使用 SFTP 协议连接远程服务器 `192.168.1.11` ，在用户 `tom` 的 `$HOME` 目录下创建 `myjfs/` 文件夹作为文件系统的数据存储。

```shell
juicefs format  \
    --storage sftp \
    --bucket 192.168.1.11:myjfs/ \
    --access-key tom \
    --secret-key 123456 \
    ...
    redis://localhost:6379/1 myjfs
```

#### 注意事项

- `--bucket` 用来设置服务器的地址及存储路径，格式为 `[sftp://]<IP/Domain>:[port]:<Path>`。注意，目录名应该以 `/` 结尾，端口号为可选项默认为 `22`，例如 `192.168.1.11:22:myjfs/`。
- `--access-key` 用来设置远程服务器的用户名
- `--secret-key` 用来设置远程服务器的密码

### NFS {#nfs}

NFS - Network File System，即网络文件系统，是类 Unix 操作系统中很常用的文件共享服务，它可以让网络内的计算机能够像访问本地文件一样访问远程文件。

JuiceFS 支持使用 NFS 作为底层存储来构建文件系统，提供两种使用方式：本地挂载和直连模式。

#### 本地挂载

JuiceFS v1.1 及之前的版本仅支持本地挂载的方式使用 NFS 作为底层存储，这种方式需要先在本地挂载 NFS 服务器上的目录，然后以本地磁盘的方式使用它来创建 JuiceFS 文件系统。

例如，先把远程 NFS 服务器 `192.168.1.11` 上的 `/srv/data` 目录挂载到本地的 `/mnt/data` 目录，然后再使用 `file` 模式访问。

```shell
$ sudo mount -t nfs 192.168.1.11:/srv/data /mnt/data
$ sudo juicefs format \
    --storage file \
    --bucket /mnt/data \
    ...
    redis://localhost:6379/1 myjfs
```

从 JuiceFS 的角度来看，本地挂载的 NFS 仍然是本地磁盘，所以 `--storage` 选项设置为 `file`。

同理，由于底层存储只能在挂载的设备上访问，所以要在多台设备上共享访问，则需要在每台设备上分别挂载 NFS 共享，或通过 WebDAV、S3 Gateway 等基于网络的方式来提供外部访问。

#### 直连模式

JuiceFS v1.2 及以上版本支持直连模式使用 NFS 作为底层存储，这种方式不需要在本地挂载预先挂载 NFS 目录，而是直接通过 JuiceFS 客户端内置的 NFS 协议访问共享目录。

例如，远程服务器 `/etc/exports` 配置文件导出了下面的 NFS 共享：

```
/srv/data    192.168.1.0/24(rw,sync,no_subtree_check)
```

可以直接使用 JuiceFS 客户端连接 NFS 服务器上的 `/srv/data` 目录来创建文件系统：

```shell
$ sudo juicefs format  \
    --storage nfs \
    --bucket 192.168.1.11:/srv/data \
    ...
    redis://localhost:6379/1 myjfs
```

在直连模式下，`--storage` 选项设置为 `nfs`，`--bucket` 选项设置为 NFS 服务器的地址和共享目录，JuiceFS 客户端会直接连接 NFS 服务器上的目录来读写数据。

**几个注意事项：**

1. JuiceFS 直连 NFS 模式目前仅支持 NFSv3 协议
2. JuiceFS 客户端需要有访问 NFS 共享目录的权限
3. NFS 默认会启用 `root_squash` 功能，当以 root 身份访问 NFS 共享时默认会被挤压成 nobody 用户。为了避免无权 NFS 共享的问题，可以将共享目录的所有者设置为 `nobody:nogroup`，或者为 NFS 共享配置 `no_root_squash` 选项来关闭权限挤压。
