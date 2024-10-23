---
title: How to Set Up Object Storage
sidebar_position: 3
description: This article introduces the object storages supported by JuiceFS and how to configure and use it.
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

As you can learn from [JuiceFS Technical Architecture](../introduction/architecture.md), JuiceFS is a distributed file system with data and metadata stored separately. JuiceFS uses object storage as the main data storage and uses databases such as Redis, PostgreSQL and MySQL as metadata storage.

## Storage options {#storage-options}

When creating a JuiceFS file system, there are following options to set up the storage:

- `--storage`: Specify the type of storage to be used by the file system, e.g. `--storage s3`
- `--bucket`: Specify the storage access address, e.g. `--bucket https://myjuicefs.s3.us-east-2.amazonaws.com`
- `--access-key` and `--secret-key`: Specify the authentication information when accessing the storage

For example, the following command uses Amazon S3 object storage to create a file system:

```shell
juicefs format --storage s3 \
    --bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
    --access-key abcdefghijklmn \
    --secret-key nmlkjihgfedAcBdEfg \
    redis://192.168.1.6/1 \
    myjfs
```

## Other options {#other-options}

When executing the `juicefs format` or `juicefs mount` command, you can set some special options in the form of URL parameters in the `--bucket` option, such as `tls-insecure-skip-verify=true` in `https://myjuicefs.s3.us-east-2.amazonaws.com?tls-insecure-skip-verify=true` is to skip the certificate verification of HTTPS requests.

Client certificates are also supported as they are commonly used for mTLS connections, for example:
`https://myjuicefs.s3.us-east-2.amazonaws.com?ca-certs=./path/to/ca&ssl-cert=./path/to/cert&ssl-key=./path/to/privatekey`

## Enable data sharding {#enable-data-sharding}

When creating a file system, multiple buckets can be defined as the underlying storage of the file system through the [`--shards`](../reference/command_reference.mdx#format-data-format-options) option. In this way, the system will distribute the files to multiple buckets based on the hashed value of the file name. Data sharding technology can distribute the load of concurrent writing of large-scale data to multiple buckets, thereby improving the writing performance.

The following are points to note when using the data sharding function:

- The `--shards` option accepts an integer between 0 and 256, indicating how many Buckets the files will be scattered into. The default value is 0, indicating that the data sharding function is not enabled.
- Only multiple buckets under the same object storage can be used.
- The integer wildcard `%d` needs to be used to specify the buckets, for example, `"http://192.168.1.18:9000/myjfs-%d"`. Buckets can be created in advance in this format, or automatically created by the JuiceFS client when creating a file system.
- The data sharding is set at the time of creation and cannot be modified after creation. You cannot increase or decrease the number of buckets, nor cancel the shards function.

For example, the following command creates a file system with 4 shards.

```shell
juicefs format --storage s3 \
    --shards 4 \
    --bucket "https://myjfs-%d.s3.us-east-2.amazonaws.com" \
    ...
```

After executing the above command, the JuiceFS client will create 4 buckets named `myjfs-0`, `myjfs-1`, `myjfs-2`, and `myjfs-3`.

## Access Key and Secret Key {#aksk}

In general, object storages are authenticated with Access Key ID and Access Key Secret. For JuiceFS file system, they are provided by options `--access-key` and `--secret-key` (or AK, SK for short).

It is more secure to pass credentials via environment variables `ACCESS_KEY` and `SECRET_KEY` instead of explicitly specifying the options `--access-key` and `--secret-key` in the command line when creating a filesystem, e.g.,

```shell
export ACCESS_KEY=abcdefghijklmn
export SECRET_KEY=nmlkjihgfedAcBdEfg
juicefs format --storage s3 \
    --bucket https://myjuicefs.s3.us-east-2.amazonaws.com \
    redis://192.168.1.6/1 \
    myjfs
```

Public clouds typically allow users to create IAM (Identity and Access Management) roles, such as [AWS IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html) or [Alibaba Cloud RAM role](https://www.alibabacloud.com/help/doc-detail/110376.htm), which can be assigned to VM instances. If the cloud server instance already has read and write access to the object storage, there is no need to specify `--access-key` and `--secret-key`.

## Use temporary access credentials {#session-token}

Permanent access credentials generally have two parts, Access Key, Secret Key, while temporary access credentials generally include three parts, Access Key, Secret Key and token, and temporary access credentials have an expiration time, usually between a few minutes and a few hours.

### How to get temporary credentials {#how-to-get-temporary-credentials}

Different cloud vendors have different acquisition methods. Generally, the Access Key, Secret Key and ARN representing the permission boundary of the temporary access credential are required as parameters to request access to the STS server of the cloud service vendor to obtain the temporary access credential. This process can generally be simplified by the SDK provided by the cloud vendor. For example, Amazon S3 can refer to this [link](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_temp_request.html) to obtain temporary credentials, and Alibaba Cloud OSS can refer to this [link](https://www.alibabacloud.com/help/en/object-storage-service/latest/use-a-temporary-credential-provided-by-sts-to-access-oss).

### How to set up object storage with temporary access credentials {#how-to-set-up-object-storage-with-temporary-access-credentials}

The way of using temporary credentials is not much different from using permanent credentials. When formatting the file system, pass the Access Key, Secret Key, and token of the temporary credentials through `--access-key`, `--secret-key`, `--session-token` can set the value. E.g:

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

Since temporary credentials expire quickly, the key is how to update the temporary credentials that JuiceFS uses after `format` the file system before the temporary credentials expire. The credential update process is divided into two steps:

1. Before the temporary certificate expires, apply for a new temporary certificate;
2. Without stopping the running JuiceFS, use the `juicefs config Meta-URL --access-key xxxx --secret-key xxxx --session-token xxxx` command to hot update the access credentials.

Newly mounted clients will use the new credentials directly, and all clients already running will also update their credentials within a minute. The entire update process will not affect the running business. Due to the short expiration time of the temporary credentials, the above steps need to **be executed in a long-term loop** to ensure that the JuiceFS service can access the object storage normally.

## Internal and public endpoint {#internal-and-public-endpoint}

Typically, object storage services provide a unified URL for access, but the cloud platform usually provides both internal and external endpoints. For example, the platform cloud services that meet the criteria will automatically resolve requests to the internal endpoint of the object storage. This offers you a lower latency, and internal network traffic is free.

Some cloud computing platforms also distinguish between internal and public networks, but instead of providing a unified access URL, they provide separate internal Endpoint and public Endpoint addresses.

JuiceFS also provides flexible support for this object storage service that distinguishes between internal and public addresses. For scenarios where the same file system is shared, the object storage is accessed through internal Endpoint on the servers that meet the criteria, and other computers are accessed through public Endpoint, which can be used as follows:

- **When creating a file system**: It is recommended to use internal Endpoint address for `--bucket`
- **When mounting a file system**: For clients that do not satisfy the internal line, you can specify a public Endpoint address to `--bucket`.

Creating a file system using an internal Endpoint ensures better performance and lower latency, and for clients that cannot be accessed through an internal address, you can specify a public Endpoint to mount with the option `--bucket`.

## Storage class <VersionAdd>1.1</VersionAdd> {#storage-class}

Object storage usually supports multiple storage classes, such as standard storage, infrequent access storage, and archive storage. Different storage classes will have different prices and availability, you can set the default storage class with the [`--storage-class`](../reference/command_reference.mdx#format-data-storage-options) option when creating the JuiceFS file system, or set a new storage class with the [`--storage-class`](../reference/command_reference.mdx#mount-data-storage-options) option when mounting the JuiceFS file system. Please refer to the user manual of the object storage you are using to see how to set the value of the `--storage-class` option (such as [Amazon S3](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html#AmazonS3-PutObject-request-header-StorageClass)).

:::note
When using certain storage classes (such as archive and deep archive), the data cannot be accessed immediately, and the data needs to be restored in advance and accessed after a period of time.
:::

:::note
When using certain storage classes (such as infrequent access), there are minimum bill units, and additional charges may be incurred for reading data. Please refer to the user manual of the object storage you are using for details.
:::

## Using proxy {#using-proxy}

If the network environment where the client is located is affected by firewall policies or other factors that require access to external object storage services through a proxy, the corresponding proxy settings are different for different operating systems. Please refer to the corresponding user manual for settings.

On Linux, for example, the proxy can be set by creating `http_proxy` and `https_proxy` environment variables.

```shell
export http_proxy=http://localhost:8035/
export https_proxy=http://localhost:8035/
juicefs format \
    --storage s3 \
    ... \
    myjfs
```

## Supported object storage {#supported-object-storage}

If you wish to use a storage system that is not listed, feel free to submit a requirement [issue](https://github.com/juicedata/juicefs/issues).

| Name                                                        | Value      |
|:-----------------------------------------------------------:|:----------:|
| [Amazon S3](#amazon-s3)                                     | `s3`       |
| [Google Cloud Storage](#google-cloud)                       | `gs`       |
| [Azure Blob Storage](#azure-blob-storage)                   | `wasb`     |
| [Backblaze B2](#backblaze-b2)                               | `b2`       |
| [IBM Cloud Object Storage](#ibm-cloud-object-storage)       | `ibmcos`   |
| [Oracle Cloud Object Storage](#oracle-cloud-object-storage) | `s3`       |
| [Scaleway Object Storage](#scaleway-object-storage)         | `scw`      |
| [DigitalOcean Spaces](#digitalocean-spaces)                 | `space`    |
| [Wasabi](#wasabi)                                           | `wasabi`   |
| [Telnyx Cloud Storage](#telnyx)                             | `s3`       |
| [Storj DCS](#storj-dcs)                                     | `s3`       |
| [Vultr Object Storage](#vultr-object-storage)               | `s3`       |
| [Cloudflare R2](#r2)                                        | `s3`       |
| [Bunny Storage](#bunny)                                     | `bunny`    |
| [Alibaba Cloud OSS](#alibaba-cloud-oss)                     | `oss`      |
| [Tencent Cloud COS](#tencent-cloud-cos)                     | `cos`      |
| [Huawei Cloud OBS](#huawei-cloud-obs)                       | `obs`      |
| [Baidu Object Storage](#baidu-object-storage)               | `bos`      |
| [Volcano Engine TOS](#volcano-engine-tos)                   | `tos`      |
| [Kingsoft Cloud KS3](#kingsoft-cloud-ks3)                   | `ks3`      |
| [QingStor](#qingstor)                                       | `qingstor` |
| [Qiniu](#qiniu)                                             | `qiniu`    |
| [Sina Cloud Storage](#sina-cloud-storage)                   | `scs`      |
| [CTYun OOS](#ctyun-oos)                                     | `oos`      |
| [ECloud Object Storage](#ecloud-object-storage)             | `eos`      |
| [JD Cloud OSS](#jd-cloud-oss)                               | `s3`       |
| [UCloud US3](#ucloud-us3)                                   | `ufile`    |
| [Ceph RADOS](#ceph-rados)                                   | `ceph`     |
| [Ceph RGW](#ceph-rgw)                                       | `s3`       |
| [Gluster](#gluster)                                         | `gluster`  |
| [Swift](#swift)                                             | `swift`    |
| [MinIO](#minio)                                             | `minio`    |
| [WebDAV](#webdav)                                           | `webdav`   |
| [HDFS](#hdfs)                                               | `hdfs`     |
| [Apache Ozone](#apache-ozone)                               | `s3`       |
| [Redis](#redis)                                             | `redis`    |
| [TiKV](#tikv)                                               | `tikv`     |
| [etcd](#etcd)                                               | `etcd`     |
| [SQLite](#sqlite)                                           | `sqlite3`  |
| [MySQL](#mysql)                                             | `mysql`    |
| [PostgreSQL](#postgresql)                                   | `postgres` |
| [Local disk](#local-disk)                                   | `file`     |
| [SFTP/SSH](#sftp)                                           | `sftp`     |

### Amazon S3

S3 supports [two styles of endpoint URI](https://docs.aws.amazon.com/AmazonS3/latest/dev/VirtualHosting.html): virtual hosted-style and path-style. The difference is:

- Virtual-hosted-style: `https://<bucket>.s3.<region>.amazonaws.com`
- Path-style: `https://s3.<region>.amazonaws.com/<bucket>`

The `<region>` should be replaced with specific region code, e.g. the region code of US East (N. Virginia) is `us-east-1`. All the available region codes can be found [here](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-regions-availability-zones.html#concepts-available-regions).

:::note
For AWS users in China, you need add `.cn` to the host, i.e. `amazonaws.com.cn`, and check [this document](https://docs.amazonaws.cn/en_us/aws/latest/userguide/endpoints-arns.html) for region code.
:::

:::note
If the S3 bucket has public access (anonymous access is supported), please set `--access-key` to `anonymous`.
:::

In JuiceFS both the two styles are supported to specify the bucket address, for example:

<Tabs groupId="amazon-s3-endpoint">
  <TabItem value="virtual-hosted-style" label="Virtual-hosted-style">

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    ... \
    myjfs
```

  </TabItem>
  <TabItem value="path-style" label="Path-style">

```bash
juicefs format \
    --storage s3 \
    --bucket https://s3.<region>.amazonaws.com/<bucket> \
    ... \
    myjfs
```

  </TabItem>
</Tabs>

You can also set `--storage` to `s3` to connect to S3-compatible object storage, e.g.:

<Tabs groupId="amazon-s3-endpoint">
  <TabItem value="virtual-hosted-style" label="Virtual-hosted-style">

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

  </TabItem>
  <TabItem value="path-style" label="Path-style">

```bash
juicefs format \
    --storage s3 \
    --bucket https://<endpoint>/<bucket> \
    ... \
    myjfs
```

  </TabItem>
</Tabs>

:::tip
The format of the option `--bucket` for all S3 compatible object storage services is `https://<bucket>.<endpoint>` or `https://<endpoint>/<bucket>`. The default `region` is `us-east-1`. When a different `region` is required, it can be set manually via the environment variable `AWS_REGION` or `AWS_DEFAULT_REGION`.
:::

### Google Cloud Storage {#google-cloud}

Google Cloud uses [IAM](https://cloud.google.com/iam/docs/overview) to manage permissions for accessing resources. Through authorizing [service accounts](https://cloud.google.com/iam/docs/creating-managing-service-accounts#iam-service-accounts-create-gcloud), you can have a fine-grained control of the access rights of cloud servers and object storage.

For cloud servers and object storage that belong to the same service account, as long as the account grants access to the relevant resources, there is no need to provide authentication information when creating a JuiceFS file system, and the cloud platform will automatically complete authentication.

For cases where you want to access the object storage from outside the Google Cloud Platform, for example, to create a JuiceFS file system on your local computer using Google Cloud Storage, you need to configure authentication information. Since Google Cloud Storage does not use Access Key ID and Access Key Secret, but rather the JSON key file of the service account to authenticate the identity.

Please refer to ["Authentication as a service account"](https://cloud.google.com/docs/authentication/production) to create JSON key file for the service account and download it to the local computer, and define the path to the key file via the environment variable `GOOGLE_APPLICATION_ CREDENTIALS`, e.g.:

```shell
export GOOGLE_APPLICATION_CREDENTIALS="$HOME/service-account-file.json"
```

You can write the command to create environment variables to `~/.bashrc` or `~/.profile` and have the shell set it automatically every time you start.

Once you have configured the environment variables for passing key information, the commands to create a file system locally and on Google Cloud Server are identical. For example,

```bash
juicefs format \
    --storage gs \
    --bucket <bucket>[.region] \
    ... \
    myjfs
```

As you can see, there is no need to include authentication information in the command, and the client will authenticate the access to the object storage through the JSON key file set in the previous environment variable. Also, since the bucket name is [globally unique](https://cloud.google.com/storage/docs/naming-buckets#considerations), when creating a file system, you only need to specify the bucket name in the option `--bucket`.

### Azure Blob Storage

To use Azure Blob Storage as data storage of JuiceFS, please [check the documentation](https://docs.microsoft.com/en-us/azure/storage/common/storage-account-keys-manage) to learn how to view the storage account name and access key, which correspond to the values ​​of the `--access-key` and `--secret-key` options, respectively.

The `--bucket` option is set in the format `https://<container>.<endpoint>`, please replace `<container>` with the name of the actual blob container and `<endpoint>` with `core.windows.net` (Azure Global) or `core.chinacloudapi.cn` (Azure China). For example:

```bash
juicefs format \
    --storage wasb \
    --bucket https://<container>.<endpoint> \
    --access-key <storage-account-name> \
    --secret-key <storage-account-access-key> \
    ... \
    myjfs
```

In addition to providing authorization information through the options `--access-key` and `--secret-key`, you could also create a [connection string](https://docs.microsoft.com/en-us/azure/storage/common/storage-configure-connection-string) and set the environment variable `AZURE_STORAGE_CONNECTION_STRING`. For example:

```bash
# Use connection string
export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=https;AccountName=XXX;AccountKey=XXX;EndpointSuffix=core.windows.net"
juicefs format \
    --storage wasb \
    --bucket https://<container> \
    ... \
    myjfs
```

:::note
For Azure users in China, the value of `EndpointSuffix` is `core.chinacloudapi.cn`.
:::

### Backblaze B2

To use Backblaze B2 as a data storage for JuiceFS, you need to create [application key](https://www.backblaze.com/b2/docs/application_keys.html) first. **Application Key ID** and **Application Key** corresponds to Access Key and Secret Key, respectively.

Backblaze B2 supports two access interfaces: the B2 native API and the S3-compatible API.

#### B2 native API

The storage type should be set to `b2`, and only the bucket name needs to be set in the option `--bucket`. For example:

```bash
juicefs format \
    --storage b2 \
    --bucket <bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    myjfs
```

#### S3-compatible API

The storage type should be set to `s3`, and the full bucket address in the option `bucket` needs to be specified. For example:

```bash
juicefs format \
    --storage s3 \
    --bucket https://s3.eu-central-003.backblazeb2.com/<bucket> \
    --access-key <application-key-ID> \
    --secret-key <application-key> \
    ... \
    myjfs
```

### IBM Cloud Object Storage

When creating JuiceFS file system using IBM Cloud Object Storage, you first need to create an [API key](https://cloud.ibm.com/docs/account?topic=account-manapikey) and an [instance ID](https://cloud.ibm.com/docs/key-protect?topic=key-protect-retrieve-instance-ID). The "API key" and "instance ID" are the equivalent of access key and secret key, respectively.

IBM Cloud Object Storage provides [multiple endpoints](https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints) for each region, depending on your network (e.g. public or private). Thus, please choose an appropriate endpoint. For example:

```bash
juicefs format \
    --storage ibmcos \
    --bucket https://<bucket>.<endpoint> \
    --access-key <API-key> \
    --secret-key <instance-ID> \
    ... \
    myjfs
```

### Oracle Cloud Object Storage

Oracle Cloud Object Storage supports S3 compatible access. Please refer to [official documentation](https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/s3compatibleapi.htm) for more information.

The `endpoint` format for this object storage is: `${namespace}.compat.objectstorage.${region}.oraclecloud.com`, for example:

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

### Scaleway Object Storage

Please follow [this document](https://www.scaleway.com/en/docs/generate-api-keys) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.scw.cloud`. Remember to replace `<region>` with specific region code, e.g. the region code of "Amsterdam, The Netherlands" is `nl-ams`. All available region codes can be found [here](https://www.scaleway.com/en/docs/object-storage-feature/#-Core-Concepts). For example:

```bash
juicefs format \
    --storage scw \
    --bucket https://<bucket>.s3.<region>.scw.cloud \
    ... \
    myjfs
```

### DigitalOcean Spaces

Please follow [this document](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<space-name>.<region>.digitaloceanspaces.com`. Please replace `<region>` with specific region code, e.g. `nyc3`. All available region codes can be found [here](https://www.digitalocean.com/docs/spaces/#regional-availability). For example:

```bash
juicefs format \
    --storage space \
    --bucket https://<space-name>.<region>.digitaloceanspaces.com \
    ... \
    myjfs
```

### Wasabi

Please follow [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360019677192-Creating-a-Root-Access-Key-and-Secret-Key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3.<region>.wasabisys.com`, replace `<region>` with specific region code, e.g. the region code of US East 1 (N. Virginia) is `us-east-1`. All available region codes can be found [here](https://wasabi-support.zendesk.com/hc/en-us/articles/360.15.26031-What-are-the-service-URLs-for-Wasabi-s-different-regions-). For example:

```bash
juicefs format \
    --storage wasabi \
    --bucket https://<bucket>.s3.<region>.wasabisys.com \
    ... \
    myjfs
```

:::note
For users in Tokyo (ap-northeast-1) region, please refer to [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360039372392-How-do-I-access-the-Wasabi-Tokyo-ap-northeast-1-storage-region-) to learn how to get appropriate endpoint URI.***
:::

### Telnyx

Prerequisites

- A [Telnyx account](https://telnyx.com/sign-up)
- [API key](https://portal.telnyx.com/#/app/api-keys) – this will be used as both `access-key` and `secret-key`

Set up JuiceFS:

```bash
juicefs format \
    --storage s3 \
    --bucket https://<regional-endpoint>.telnyxstorage.com/<bucket> \
    --access-key <api-key> \
    --secret-key <api-key> \
    ... \
    myjfs
```

Available regional endpoints are [here](https://developers.telnyx.com/docs/cloud-storage/api-endpoints).

### Storj DCS

Please refer to [this document](https://docs.storj.io/api-reference/s3-compatible-gateway) to learn how to create access key and secret key.

Storj DCS is an S3-compatible storage, using `s3` for option `--storage`. The setting format of the option `--bucket` is `https://gateway.<region>.storjshare.io/<bucket>`, and please replace `<region>` with the corresponding region code you need. There are currently three available regions: `us1`, `ap1` and `eu1`. For example:

```shell
juicefs format \
    --storage s3 \
    --bucket https://gateway.<region>.storjshare.io/<bucket> \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

:::caution
Storj DCS [ListObjects](https://github.com/storj/gateway-st/blob/main/docs/s3-compatibility.md#listobjects) API is not fully S3 compatible (result list is not sorted), so some features of JuiceFS do not work. For example, `juicefs gc`, `juicefs fsck`, `juicefs sync`, `juicefs destroy`. And when using `juicefs mount`, you need to disable [automatic-backup](../administration/metadata_dump_load.md#backup-automatically) function by adding `--backup-meta 0`.
:::

### Vultr Object Storage

Vultr Object Storage is an S3-compatible storage, using `s3` for `--storage` option. The format of the option `--bucket` is `https://<bucket>.<region>.vultrobjects.com/`. For example:

```shell
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.ewr1.vultrobjects.com/ \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

Please find the access and secret keys for object storage [in the customer portal](https://my.vultr.com/objectstorage).

### Cloudflare R2 {#r2}

R2 is Cloudflare's object storage service and provides an S3-compatible API, so usage is the same as Amazon S3. Please refer to [Documentation](https://developers.cloudflare.com/r2/data-access/s3-api/tokens) to learn how to create Access Key and Secret Key.

```shell
juicefs format \
    --storage s3 \
    --bucket https://<ACCOUNT_ID>.r2.cloudflarestorage.com/myjfs \
    --access-key <your-access-key> \
    --secret-key <your-sceret-key> \
    ... \
    myjfs
```

For production, it is recommended to pass key information via the `ACCESS_KEY` and `SECRET_KEY` environment variables, e.g.

```shell
export ACCESS_KEY=<your-access-key>
export SECRET_KEY=<your-sceret-key>
juicefs format \
    --storage s3 \
    --bucket https://<ACCOUNT_ID>.r2.cloudflarestorage.com/myjfs \
    ... \
    myjfs
```

:::caution
Cloudflare R2 `ListObjects` API is not fully S3 compatible (result list is not sorted), so some features of JuiceFS do not work. For example, `juicefs gc`, `juicefs fsck`, `juicefs sync`, `juicefs destroy`. And when using `juicefs mount`, you need to disable [automatic-backup](../administration/metadata_dump_load.md#backup-automatically) function by adding `--backup-meta 0`.
:::

### Bunny Storage {#bunny}

Bunny Storage offers a non-S3 compatible object storage with multiple performance tiers and many storage regions. It uses [it uses a custom API](https://docs.bunny.net/reference/storage-api).

This is not included by default, please build it with tag `bunny`

#### Usage

Create a Storage Zone and use the Zone Name with the Hostname of the Location seperated by a dot as Bucket name and the `Write Password` as Secret Key.

```shell
juicefs format \
    --storage bunny \
    --secret-key "write-password" \
    --bucket "https://uk.storage.bunnycdn.com/myzone" \ # https://<Endpoint>/<Zonename>
    myjfs
```

### Alibaba Cloud OSS

Please follow [this document](https://www.alibabacloud.com/help/doc-detail/125558.htm) to learn how to get access key and secret key. If you have already created [RAM role](https://www.alibabacloud.com/help/doc-detail/110376.htm) and assigned it to a VM instance, you could omit the options `--access-key` and `--secret-key`.

Alibaba Cloud also supports using [Security Token Service (STS)](https://www.alibabacloud.com/help/doc-detail/100624.htm) to authorize temporary access to OSS. If you wanna use STS, you should omit the options `--access-key` and `--secret-key` and set environment variables `ALICLOUD_ACCESS_KEY_ID`, `ALICLOUD_ACCESS_KEY_SECRET` and `SECURITY_TOKEN`instead, for example:

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

OSS provides [multiple endpoints](https://www.alibabacloud.com/help/doc-detail/31834.htm) for each region, depending on your network (e.g. public or internal network). Please choose an appropriate endpoint.

If you are creating a file system on AliCloud's server, you can specify the bucket name directly in the option `--bucket`. For example.

```bash
# Running within Alibaba Cloud
juicefs format \
    --storage oss \
    --bucket <bucket> \
    ... \
    myjfs
```

### Tencent Cloud COS

The naming rule of bucket in Tencent Cloud is `<bucket>-<APPID>`, so you must append `APPID` to the bucket name. Please follow [this document](https://intl.cloud.tencent.com/document/product/436/13312) to learn how to get `APPID`.

The full format of `--bucket` option is `https://<bucket>-<APPID>.cos.<region>.myqcloud.com`, and please replace `<region>` with specific region code. E.g. the region code of Shanghai is `ap-shanghai`. You could find all available region codes [here](https://intl.cloud.tencent.com/document/product/436/6224). For example:

```bash
juicefs format \
    --storage cos \
    --bucket https://<bucket>-<APPID>.cos.<region>.myqcloud.com \
    ... \
    myjfs
```

If you are creating a file system on Tencent Cloud's server, you can specify the bucket name directly in the option `--bucket`. For example.

```bash
# Running within Tencent Cloud
juicefs format \
    --storage cos \
    --bucket <bucket>-<APPID> \
    ... \
    myjfs
```

### Huawei Cloud OBS

Please follow [this document](https://support.huaweicloud.com/usermanual-ca/zh-cn_topic_0046606340.html) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.obs.<region>.myhuaweicloud.com`, and please replace `<region>` with specific region code. E.g. the region code of Beijing 1 is `cn-north-1`. You could find all available region codes [here](https://developer.huaweicloud.com/endpoint?OBS). For example:

```bash
juicefs format \
    --storage obs \
    --bucket https://<bucket>.obs.<region>.myhuaweicloud.com \
    ... \
    myjfs
```

If you are creating a file system on Huawei Cloud's server, you can specify the bucket name directly in the option `--bucket`. For example,

```bash
# Running within Huawei Cloud
juicefs format \
    --storage obs \
    --bucket <bucket> \
    ... \
    myjfs
```

### Baidu Object Storage

Please follow [this document](https://cloud.baidu.com/doc/Reference/s/9jwvz2egb) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.bcebos.com`, and please replace `<region>` with specific region code. E.g. the region code of Beijing is `bj`. You could find all available region codes [here](https://cloud.baidu.com/doc/BOS/s/Ck1rk80hn#%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D%EF%BC%88endpoint%EF%BC%89). For example:

```bash
juicefs format \
    --storage bos \
    --bucket https://<bucket>.<region>.bcebos.com \
    ... \
    myjfs
```

If you are creating a file system on Baidu Cloud's server, you can specify the bucket name directly in the option `--bucket`. For example,

```bash
# Running within Baidu Cloud
juicefs format \
    --storage bos \
    --bucket <bucket> \
    ... \
    myjfs
```

### Volcano Engine TOS <VersionAdd>1.0.3</VersionAdd> {#volcano-engine-tos}

Please follow [this document](https://www.volcengine.com/docs/6291/65568) to learn how to get access key and secret key.

The TOS provides [multiple endpoints](https://www.volcengine.com/docs/6349/107356) for each region, depending on your network (e.g. public or internal). Please choose an appropriate endpoint. For example:

```bash
juicefs format \
    --storage tos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### Kingsoft Cloud KS3

Please follow [this document](https://docs.ksyun.com/documents/1386) to learn how to get access key and secret key.

KS3 provides [multiple endpoints](https://docs.ksyun.com/documents/6761) for each region, depending on your network (e.g. public or internal). Please choose an appropriate endpoint. For example:

```bash
juicefs format \
    --storage ks3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### QingStor

Please follow [this document](https://docsv3.qingcloud.com/storage/object-storage/api/practices/signature/#%E8%8E%B7%E5%8F%96-access-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.qingstor.com`, replace `<region>` with specific region code. E.g. the region code of Beijing 3-A is `pek3a`. You could find all available region codes [here](https://docs.qingcloud.com/qingstor/#%E5%8C%BA%E5%9F%9F%E5%8F%8A%E8%AE%BF%E9%97%AE%E5%9F%9F%E5%90%8D). For example:

```bash
juicefs format \
    --storage qingstor \
    --bucket https://<bucket>.<region>.qingstor.com \
    ... \
    myjfs
```

:::note
The format of `--bucket` option for all QingStor compatible object storage services is `http://<bucket>.<endpoint>`.
:::

### Qiniu

Please follow [this document](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.s3-<region>.qiniucs.com`, replace `<region>` with specific region code. E.g. the region code of China East is `cn-east-1`. You could find all available region codes [here](https://developer.qiniu.com/kodo/4088/s3-access-domainname). For example:

```bash
juicefs format \
    --storage qiniu \
    --bucket https://<bucket>.s3-<region>.qiniucs.com \
    ... \
    myjfs
```

### Sina Cloud Storage

Please follow [this document](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.stor.sinaapp.com`. For example:

```bash
juicefs format \
    --storage scs \
    --bucket https://<bucket>.stor.sinaapp.com \
    ... \
    myjfs
```

### CTYun OOS

Please follow [this document](https://www.ctyun.cn/help2/10000101/10473683) to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<endpoint>`,  For example:

```bash
juicefs format \
    --storage oos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### ECloud Object Storage

Please follow [this document](https://ecloud.10086.cn/op-help-center/doc/article/24501) to learn how to get access key and secret key.

ECloud Object Storage provides [multiple endpoints](https://ecloud.10086.cn/op-help-center/doc/article/40956) for each region, depending on your network (e.g. public or internal). Please choose an appropriate endpoint. For example:

```bash
juicefs format \
    --storage eos \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### JD Cloud OSS

Please follow [this document](https://docs.jdcloud.com/cn/account-management/accesskey-management)  to learn how to get access key and secret key.

The `--bucket` option format is `https://<bucket>.<region>.jdcloud-oss.com`，and please replace `<region>` with specific region code. You could find all available region codes [here](https://docs.jdcloud.com/cn/object-storage-service/oss-endpont-list). For example:

```bash
juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<region>.jdcloud-oss.com \
    ... \
    myjfs
```

### UCloud US3

Please follow [this document](https://docs.ucloud.cn/uai-censor/access/key) to learn how to get access key and secret key.

US3 (formerly UFile) provides [multiple endpoints](https://docs.ucloud.cn/ufile/introduction/region) for each region, depending on your network (e.g. public or internal). Please choose an appropriate endpoint. For example:

```bash
juicefs format \
    --storage ufile \
    --bucket https://<bucket>.<endpoint> \
    ... \
    myjfs
```

### Ceph RADOS

:::note
JuiceFS v1.0 uses `go-ceph` v0.4.0, which supports Ceph Luminous (v12.2.x) and above.
JuiceFS v1.1 uses `go-ceph` v0.18.0, which supports Ceph Octopus (v15.2.x) and above.
Make sure that JuiceFS matches your Ceph and `librados` version, see [`go-ceph`](https://github.com/ceph/go-ceph#supported-ceph-versions).
:::

The [Ceph Storage Cluster](https://docs.ceph.com/en/latest/rados) has a messaging layer protocol that enables clients to interact with a Ceph Monitor and a Ceph OSD Daemon. The [`librados`](https://docs.ceph.com/en/latest/rados/api/librados-intro) API enables you to interact with the two types of daemons:

- The [Ceph Monitor](https://docs.ceph.com/en/latest/rados/configuration/common/#monitors), which maintains a master copy of the cluster map.
- The [Ceph OSD Daemon (OSD)](https://docs.ceph.com/en/latest/rados/configuration/common/#osds), which stores data as objects on a storage node.

JuiceFS supports the use of native Ceph APIs based on `librados`. You need to install `librados` library and build `juicefs` binary separately.

First, install a `librados` that matches the version of your Ceph installation, For example, if Ceph version is Octopus (v15.2.x), then it is recommended to use `librados` v15.2.x.

<Tabs>
  <TabItem value="debian" label="Debian and derivatives">

```bash
sudo apt-get install librados-dev
```

  </TabItem>
  <TabItem value="centos" label="RHEL and derivatives">

```bash
sudo yum install librados2-devel
```

  </TabItem>
</Tabs>

Then compile JuiceFS for Ceph (make sure you have Go 1.20+ and GCC 5.4+ installed):

```bash
make juicefs.ceph
```

When using with Ceph, the JuiceFS Client object storage related options are interpreted differently:

* `--bucket` stands for the Ceph storage pool, the format is `ceph://<pool-name>`. A [pool](https://docs.ceph.com/en/latest/rados/operations/pools) is a logical partition for storing objects. Create a pool before use.
* `--access-key` stands for the Ceph cluster name, the default value is `ceph`.
* `--secret-key` option is [Ceph client user name](https://docs.ceph.com/en/latest/rados/operations/user-management), the default user name is `client.admin`.

In order to reach Ceph Monitor, `librados` reads Ceph configuration file by searching default locations and the first found will be used. The locations are:

- `CEPH_CONF` environment variable
- `/etc/ceph/ceph.conf`
- `~/.ceph/config`
- `ceph.conf` in the current working directory

Since these additional Ceph configuration files are needed during the mount, CSI Driver users need to [upload them to Kubernetes, and map to the mount pod](https://juicefs.com/docs/csi/guide/pv/#mount-pod-extra-files).

To format a volume, run:

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

[Ceph Object Gateway](https://ceph.io/ceph-storage/object-storage) is an object storage interface built on top of `librados` to provide applications with a RESTful gateway to Ceph Storage Clusters. Ceph Object Gateway supports S3-compatible interface, so we could set `--storage` to `s3` directly.

The `--bucket` option format is `http://<bucket>.<endpoint>` (virtual hosted-style). For example:

```bash
juicefs format \
    --storage s3 \
    --bucket http://<bucket>.<endpoint> \
    ... \
    myjfs
```

### Gluster

[Gluster](https://github.com/gluster/glusterfs) is a software defined distributed storage that can scale to several petabytes. JuiceFS communicates with Gluster via the `libgfapi` library, so it needs to be built separately before used.

First, install `libgfapi` (version 6.0 - 10.1, [10.4+ is not supported yet](https://github.com/juicedata/juicefs/issues/4043))

<Tabs>
  <TabItem value="debian" label="Debian and derivatives">

```bash
sudo apt-get install uuid-dev libglusterfs-dev glusterfs-common
```

  </TabItem>
  <TabItem value="centos" label="RHEL and derivatives">

```bash
sudo yum install glusterfs glusterfs-api-devel glusterfs-libs
```

  </TabItem>
</Tabs>

Then compile JuiceFS supporting Gluster:

```bash
make juicefs.gluster
```

Now we can create a JuiceFS volume on Gluster:

```bash
juicefs format \
    --storage gluster \
    --bucket host1,host2,host3/gv0 \
    ... \
    myjfs
```

The format of `--bucket` option is `<host[,host...]>/<volume_name>`. Please note the `volume_name` here is the name of Gluster volume, and has nothing to do with the name of JuiceFS volume.

### Swift

[OpenStack Swift](https://github.com/openstack/swift) is a distributed object storage system designed to scale from a single machine to thousands of servers. Swift is optimized for multi-tenancy and high concurrency. Swift is ideal for backups, web and mobile content, and any other unstructured data that can grow without bound.

The `--bucket` option format is `http://<container>.<endpoint>`. A container defines a namespace for objects.

**Currently, JuiceFS only supports [Swift V1 authentication](https://www.swiftstack.com/docs/cookbooks/swift_usage/auth.html).**

The value of `--access-key` option is username. The value of `--secret-key` option is password. For example:

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

[MinIO](https://min.io) is an open source lightweight object storage, compatible with Amazon S3 API.

It is easy to run a MinIO instance locally using Docker. For example, the following command sets and maps port `9900` for the console with `--console-address ":9900"` and also maps the data path for the MinIO to the `minio-data` folder in the current directory, which can be modified if needed.

```shell
sudo docker run -d --name minio \
    -p 9000:9000 \
    -p 9900:9900 \
    -e "MINIO_ROOT_USER=minioadmin" \
    -e "MINIO_ROOT_PASSWORD=minioadmin" \
    -v $PWD/minio-data:/data \
    --restart unless-stopped \
    minio/minio server /data --console-address ":9900"
```

After container is up and running, you can access:

- **MinIO API**: [http://127.0.0.1:9000](http://127.0.0.1:9000), this is the object storage service address used by JuiceFS
- **MinIO UI**: [http://127.0.0.1:9900](http://127.0.0.1:9900), this is used to manage the object storage itself, not related to JuiceFS

The initial Access Key and Secret Key of the object storage are both `minioadmin`.

When using MinIO as data storage for JuiceFS, set the option `--storage` to `minio`.

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

1. Currently, JuiceFS only supports path-style MinIO URI addresses, e.g., `http://127.0.0.1:9000/myjfs`.
1. The `MINIO_REGION` environment variable can be used to set the region of MinIO, if not set, the default is `us-east-1`.
1. When using Multi-Node MinIO deployment, consider setting using a DNS address in the service endpoint, resolving to all MinIO Node IPs, as a simple load-balancer, e.g. `http://minio.example.com:9000/myjfs`
:::

### WebDAV

[WebDAV](https://en.wikipedia.org/wiki/WebDAV) is an extension of the Hypertext Transfer Protocol (HTTP)
that facilitates collaborative editing and management of documents stored on the WWW server among users.
From JuiceFS v0.15+, JuiceFS can use a storage that speaks WebDAV as a data storage.

You need to set `--storage` to `webdav`, and `--bucket` to the endpoint of WebDAV. If basic authorization is enabled, username and password should be provided as `--access-key` and `--secret-key`, for example:

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

[HDFS](https://hadoop.apache.org) is the file system for Hadoop, which can be used as the object storage for JuiceFS.

When HDFS is used, `--access-key` can be used to specify the `username`, and `hdfs` is usually the default superuser. For example:

```bash
juicefs format \
    --storage hdfs \
    --bucket namenode1:8020 \
    --access-key hdfs \
    ... \
    myjfs
```

When `--access-key` is not specified on formatting, JuiceFS will use the current user of `juicefs mount` or Hadoop SDK to access HDFS. It will hang and fail with IO error eventually, if the current user don't have enough permission to read/write the blocks in HDFS.

JuiceFS will try to load configurations for HDFS client based on `$HADOOP_CONF_DIR` or `$HADOOP_HOME`. If an empty value is provided to `--bucket`, the default HDFS found in Hadoop configurations will be used.

bucket format:

- `[hdfs://]namenode:port[/path]`

for HA cluster:

- `[hdfs://]namenode1:port,namenode2:port[/path]`
- `[hdfs://]nameservice[/path]`

For HDFS which enable Kerberos, `KRB5KEYTAB` and `KRB5PRINCIPAL` environment var can be used to set keytab and principal.

### Apache Ozone

Apache Ozone is a scalable, redundant, and distributed object storage for Hadoop. It supports S3-compatible interface, so we could set `--storage` to `s3` directly.

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

[Redis](https://redis.io) can be used as both metadata storage for JuiceFS and as data storage, but when using Redis as a data storage, it is recommended not to store large-scale data.

#### Standalone

The `--bucket` option format is `redis://<host>:<port>/<db>`. The value of `--access-key` option is username. The value of `--secret-key` option is password. For example:

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

In Redis Sentinel mode, the format of the `--bucket` option is `redis[s]://MASTER_NAME,SENTINEL_ADDR[,SENTINEL_ADDR]:SENTINEL_PORT[/DB]`. Sentinel's password needs to be declared through the `SENTINEL_PASSWORD_FOR_OBJ` environment variable. For example:

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

#### Redis Cluster

In Redis Cluster mode, the format of `--bucket` option is `redis[s]://ADDR:PORT,[ADDR:PORT],[ADDR:PORT]`. For example:

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

[TiKV](https://tikv.org) is a highly scalable, low latency, and easy to use key-value database. It provides both raw and ACID-compliant transactional key-value API.

TiKV can be used as both metadata storage and data storage for JuiceFS.

:::note
It's recommended to use dedicated TiKV 5.0+ cluster as the data storage for JuiceFS.
:::

The `--bucket` option format is `<host>:<port>,<host>:<port>,<host>:<port>`, and `<host>` is the address of Placement Driver (PD). The options `--access-key` and `--secret-key` have no effect and can be omitted. For example:

```bash
juicefs format \
    --storage tikv \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>" \
    ... \
    myjfs
```

:::note
Don't use the same TiKV cluster for both metadata and data, because JuiceFS uses non-transactional protocol (RawKV) for objects and transactional protocol (TnxKV) for metadata. The TxnKV protocol has special encoding for keys, so they may overlap with keys even they has different prefixes. BTW, it's recommmended to enable [Titan](https://tikv.org/docs/latest/deploy/configure/titan) in TiKV for data cluster.
:::

#### Set up TLS

If you need to enable TLS, you can set the TLS configuration item by adding the query parameter after the bucket URL. Currently supported configuration items:

| Name        | Value                                                                                                                                                   |
|-------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ca`        | CA root certificate, used to connect TiKV/PD with TLS                                                                                                   |
| `cert`      | certificate file path, used to connect TiKV/PD with TLS                                                                                                 |
| `key`       | private key file path, used to connect TiKV/PD with TLS                                                                                                 |
| `verify-cn` | verify component caller's identity, [reference link](https://docs.pingcap.com/tidb/dev/enable-tls-between-components#verify-component-callers-identity) |

For example:

```bash
juicefs format \
    --storage tikv \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>?ca=/path/to/ca.pem&cert=/path/to/tikv-server.pem&key=/path/to/tikv-server-key.pem&verify-cn=CN1,CN2" \
    ... \
    myjfs
```

### etcd

[etcd](https://etcd.io) is a small-scale key-value database with high availability and reliability, which can be used as both the metadata storage of JuiceFS and the data storage of JuiceFS.

etcd will [limit](https://etcd.io/docs/latest/dev-guide/limit) a single request to no more than 1.5MB by default, you need to change the block size (`--block-size` option) of JuiceFS to 1MB or even lower.

The `--bucket` option needs to fill in the etcd address, the format is similar to `<host1>:<port>,<host2>:<port>,<host3>:<port>`. The `--access-key` and `--secret-key` options are filled with username and password, which can be omitted when etcd does not enable user authentication. E.g:

```bash
juicefs format \
    --storage etcd \
    --block-size 1024 \  # This option is very important
    --bucket "<host1>:<port>,<host2>:<port>,<host3>:<port>/prefix" \
    --access-key myname \
    --secret-key mypass \
    ... \
    myjfs
```

#### Set up TLS

If you need to enable TLS, you can set the TLS configuration item by adding the query parameter after the bucket URL. Currently supported configuration items:

| Name                   | Value                 |
|------------------------|-----------------------|
| `cacert`               | CA root certificate   |
| `cert`                 | certificate file path |
| `key`                  | private key file path |
| `server-name`          | name of server        |
| `insecure-skip-verify` | 1                     |

For example:

```bash
juicefs format \
    --storage etcd \
    --bucket "<host>:<port>,<host>:<port>,<host>:<port>?cacert=/path/to/ca.pem&cert=/path/to/server.pem&key=/path/to/key.pem&server-name=etcd" \
    ... \
    myjfs
```

:::note
The path to the certificate needs to be an absolute path, and make sure that all machines that need to mount can use this path to access them.
:::

### SQLite

[SQLite](https://sqlite.org) is a small, fast, single-file, reliable, full-featured single-file SQL database engine widely used around the world.

When using SQLite as a data store, you only need to specify its absolute path.

```shell
juicefs format \
    --storage sqlite3 \
    --bucket /path/to/sqlite3.db \
    ... \
    myjfs
```

:::note
Since SQLite is an embedded database, only the host where the database is located can access it, and cannot be used in multi-machine sharing scenarios. If a relative path is used when formatting, it will cause problems when mounting, please use an absolute path.
:::

### MySQL

[MySQL](https://www.mysql.com) is one of the popular open source relational databases, often used as the database of choice for web applications, both as a metadata engine for JuiceFS and for storing files data. MySQL-compatible [MariaDB](https://mariadb.org), [TiDB](https://github.com/pingcap/tidb), etc. can be used as data storage.

When using MySQL as a data storage, you need to create a database in advance and add the desired permissions, specify the access address through the `--bucket` option, specify the user name through the `--access-key` option, and specify the password through the `--secret-key` option. An example is as follows:

```shell
juicefs format \
    --storage mysql \
    --bucket (<host>:3306)/<database-name> \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

After the file system is created, JuiceFS creates a table named `jfs_blob` in the database to store the data.

:::note
Don't miss the parentheses `()` in the `--bucket` parameter.
:::

### PostgreSQL

[PostgreSQL](https://www.postgresql.org) is a powerful open source relational database with a complete ecology and rich application scenarios. It can be used as both the metadata engine of JuiceFS and the data storage. Other databases compatible with the PostgreSQL protocol (such as [CockroachDB](https://github.com/cockroachdb/cockroach), etc.) can also be used as data storage.

When creating a file system, you need to create a database and add the corresponding read and write permissions. Use the `--bucket` option to specify the address of the data, use the `--access-key` option to specify the username, and use the `--secret-key` option to specify the password. An example is as follows:

```shell
juicefs format \
    --storage postgres \
    --bucket <host>:<port>/<db>[?parameters] \
    --access-key <username> \
    --secret-key <password> \
    ... \
    myjfs
```

After the file system is created, JuiceFS creates a table named `jfs_blob` in the database to store the data.

#### Troubleshooting

The JuiceFS client uses SSL encryption to connect to PostgreSQL by default. If the connection error `pq: SSL is not enabled on the server` indicates that the database does not have SSL enabled. You can enable SSL encryption for PostgreSQL according to your business scenario, or you can add the parameter `sslmode=disable` to the bucket URL to disable encryption verification.

### Local disk

When creating JuiceFS storage, if no storage type is specified, the local disk will be used to store data by default. The default storage path for root user is `/var/jfs`, and `~/.juicefs/local` is for ordinary users.

For example, using the local Redis database and local disk to create a JuiceFS storage named `test`:

```shell
juicefs format redis://localhost:6379/1 test
```

Local storage is usually only used to help users understand how JuiceFS works and to give users an experience on the basic features of JuiceFS. The created JuiceFS storage cannot be mounted by other clients within the network and can only be used on a single machine.

### SFTP/SSH {#sftp}

SFTP - Secure File Transfer Protocol, It is not a type of storage. To be precise, JuiceFS reads and writes to disks on remote hosts via SFTP/SSH, thus allowing any SSH-enabled operating system to be used as a data storage for JuiceFS.

For example, the following command uses the SFTP protocol to connect to the remote server `192.168.1.11` and creates the `myjfs/` folder in the `$HOME` directory of user `tom` as the data storage of JuiceFS.

```shell
juicefs format  \
    --storage sftp \
    --bucket 192.168.1.11:myjfs/ \
    --access-key tom \
    --secret-key 123456 \
    ...
    redis://localhost:6379/1 myjfs
```

#### Notes

- `--bucket` is used to set the server address and storage path in the format `[sftp://]<IP/Domain>:[port]:<Path>`. Note that the directory name should end with `/`, and the port number is optionally defaulted to `22`, e.g. `192.168.1.11:22:myjfs/`.
- `--access-key` set the username of the remote server
- `--secret-key` set the password of the remote server

### NFS {#nfs}

NFS - Network File System, is a commonly used file-sharing service in Unix-like operating systems. It allows computers within a network to access remote files as if they were local files.

JuiceFS supports using NFS as the underlying storage to build a file system, offering two usage methods: local mount and direct mode.

#### Local Mount

JuiceFS v1.1 and earlier versions only support using NFS as underlying storage via local mount. This method requires mounting the directory on the NFS server locally first, and then using it as a local disk to create the JuiceFS file system.

For example, first mount the `/srv/data` directory from the remote NFS server `192.168.1.11` to the local `/mnt/data` directory, and then access it in `file` mode.

```shell
$ sudo mount -t nfs 192.168.1.11:/srv/data /mnt/data
$ sudo juicefs format \
    --storage file \
    --bucket /mnt/data \
    ... \
    redis://localhost:6379/1 myjfs
```

From JuiceFS's perspective, the locally mounted NFS is still a local disk, so the `--storage` option is set to `file`.

Similarly, because the underlying storage can only be accessed on the mounted device, to share access across multiple devices, you need to mount the NFS share on each device separately, or provide external access through network-based methods such as WebDAV or S3 Gateway.

#### Direct Mode

JuiceFS v1.2 and later versions support using NFS as the underlying storage in direct mode. This method does not require pre-mounting the NFS directory locally but accesses the shared directory directly through the built-in NFS protocol in the JuiceFS client.

For example, the remote server's `/etc/exports` configuration file exports the following NFS share:

```
/srv/data    192.168.1.0/24(rw,sync,no_subtree_check)
```

You can directly use the JuiceFS client to connect to the `/srv/data` directory on the NFS server to create the file system:

```shell
$ sudo juicefs format  \
    --storage nfs \
    --bucket 192.168.1.11:/srv/data \
    ... \
    redis://localhost:6379/1 myjfs
```

In direct mode, the `--storage` option is set to `nfs`, and the `--bucket` option is set to the NFS server address and shared directory. The JuiceFS client will directly connect to the directory on the NFS server to read and write data.

**A few considerations:**

1. JuiceFS direct mode currently only supports the NFSv3 protocol.
2. The JuiceFS client needs permission to access the NFS shared directory.
3. NFS by default enables the `root_squash` feature, which maps root access to the NFS share to the `nobody` user by default. To avoid permission issues with NFS shares, you can set the owner of the shared directory to `nobody:nogroup` or configure the NFS share with the `no_root_squash` option to disable permission squashing.
