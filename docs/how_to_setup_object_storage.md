# How to Setup Object Storage

This is a guide about how to setup object storage when format a volume. Different object storage may has different option value. Check the specific object storage for your need.

## Supported Object Storage

This table lists all JuiceFS supported object storage, when you format a volume you need specify storage type through `--storage` option, e.g. for Amazon S3 the value of `--storage` is `s3`.

| Name                                    | Value      |
| ----                                    | -----      |
| Amazon S3                               | `s3`       |
| Google Cloud Storage                    | `gs`       |
| Azure Blob Storage                      | `wasb`     |
| Backblaze B2 Cloud Storage              | `b2`       |
| IBM Cloud Object Storage                | `ibmcos`   |
| Scaleway Object Storage                 | `scw`      |
| DigitalOcean Spaces Object Storage      | `space`    |
| Wasabi Cloud Object Storage             | `wasabi`   |
| Alibaba Cloud Object Storage Service    | `oss`      |
| Tencent Cloud Object Storage            | `cos`      |
| Huawei Cloud Object Storage Service     | `obs`      |
| Baidu Object Storage                    | `bos`      |
| Kingsoft Cloud Standard Storage Service | `ks3`      |
| Meituan Storage Service                 | `mss`      |
| NetEase Object Storage                  | `nos`      |
| QingStor Object Storage                 | `qingstor` |
| Qiniu Cloud Object Storage              | `qiniu`    |
| CTYun Object-Oriented Storage           | `oos`      |
| Sina Cloud Storage                      | `scs`      |
| SpeedyCloud Object Storage              | `speedy`   |
| UCloud US3                              | `ufile`    |
| Ceph RGW                                | `ceph`     |
| Swift                                   | `swift`    |
| MinIO                                   | `minio`    |
| HDFS                                    | `hdfs`     |
| Redis                                   | `redis`    |
| Local disk                              | `file`     |

## Access key and secret key

For authorization, the access key and secret key are needed. You could specify them through `--access-key` and `--secret-key` options. Or you can set `ACCESS_KEY` and `SECRET_KEY` environment variables.

Public cloud provider usually allow user create IAM (Identity and Access Management) role (e.g. [AWS IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html)) or similar thing (e.g. [Alibaba Cloud RAM role](https://help.aliyun.com/document_detail/93689.html)), then assign the role to VM instance. If your VM instance already have permission to access object storage, then you could omit `--access-key` and `--secret-key` options.

## S3

S3 supports [two style URI](https://docs.aws.amazon.com/AmazonS3/latest/dev/VirtualHosting.html): virtual hosted-style and path-style. The difference between them is:

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

You can also use S3 storage type to connect S3-compatible storage. But beware that you still need use virtual hosted-style URI. For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    ... \
    localhost test
```

## Google Cloud Storage

Cause Google Cloud doesn't have access key and secret key, the `--access-key` and `--secret-key` options can be omitted. Please follow Google Cloud document to know how [authentication](https://cloud.google.com/docs/authentication) and [authorization](https://cloud.google.com/iam/docs/overview) work. Typically, when you running inside Google Cloud, you already have permission to access the storage.

And because bucket name is [globally unique](https://cloud.google.com/storage/docs/naming-buckets#considerations), when you specify the `--bucket` option could just provide its name. For example:

```bash
$ ./juicefs format \
    --storage gs \
    --bucket gs://<bucket> \
    ... \
    localhost test
```

## Azure Blob Storage

Besides provide authorization information through `--access-key` and `--secret-key` options, you could also create a [connection string](https://docs.microsoft.com/en-us/azure/storage/common/storage-configure-connection-string) and set `AZURE_STORAGE_CONNECTION_STRING` environment variable.

## Backblaze B2 Cloud Storage

You need first creating [application key](https://www.backblaze.com/b2/docs/application_keys.html). The "Application Key ID" and "Application Key" are the equivalent of access key and secret key respectively.

## IBM Cloud Object Storage

You need first creating [API key](https://cloud.ibm.com/docs/account?topic=account-manapikey) and retrieving [instance ID](https://cloud.ibm.com/docs/key-protect?topic=key-protect-retrieve-instance-ID). The "API key" and "instance ID" are the equivalent of access key and secret key respectively.

## Scaleway Object Storage

Please follow [this document](https://www.scaleway.com/en/docs/generate-api-keys) to learn how to get access key and secret key.

## DigitalOcean Spaces Object Storage

Please follow [this document](https://www.digitalocean.com/community/tutorials/how-to-create-a-digitalocean-space-and-api-key) to learn how to get access key and secret key.

## Wasabi Cloud Object Storage

Please follow [this document](https://wasabi-support.zendesk.com/hc/en-us/articles/360019677192-Creating-a-Root-Access-Key-and-Secret-Key) to learn how to get access key and secret key.

## Alibaba Cloud Object Storage Service

Please follow [this document](https://help.aliyun.com/document_detail/38738.html) to learn how to get access key and secret key.

## Tencent Cloud Object Storage

The naming rule of bucket in Tencent Cloud is `<bucket>-<APPID>`, so you must append `APPID` to the bucket name. Please follow [this document](https://cloud.tencent.com/document/product/436/13312) to learn how to get `APPID`. The example command is:

```bash
$ ./juicefs format \
    --storage cos \
    --bucket https://<bucket>-<APPID>.<endpoint> \
    ... \
    localhost test
```

## Huawei Cloud Object Storage Service

Please follow [this document](https://support.huaweicloud.com/usermanual-ca/zh-cn_topic_0046606340.html) to learn how to get access key and secret key.

## Baidu Object Storage

Please follow [this document](https://cloud.baidu.com/doc/Reference/s/9jwvz2egb) to learn how to get access key and secret key.

## Kingsoft Cloud Standard Storage Service

Please follow [this document](https://docs.ksyun.com/documents/1386) to learn how to get access key and secret key.

## Meituan Storage Service

Please follow [this document](https://www.mtyun.com/doc/api/mss/mss/fang-wen-kong-zhi) to learn how to get access key and secret key.

## NetEase Object Storage

Please follow [this document](https://www.163yun.com/help/documents/55485278220111872) to learn how to get access key and secret key.

## QingStor Object Storage

Please follow [this document](https://docs.qingcloud.com/qingstor/api/common/signature.html#%E8%8E%B7%E5%8F%96-access-key) to learn how to get access key and secret key.

## Qiniu Cloud Object Storage

Please follow [this document](https://developer.qiniu.com/af/kb/1479/how-to-access-or-locate-the-access-key-and-secret-key) to learn how to get access key and secret key.

## CTYun Object-Oriented Storage

Please follow [this document](http://oos.ctyunapi.cn/downfile/helpcenter/%E5%AF%B9%E8%B1%A1%E5%AD%98%E5%82%A8%E7%94%A8%E6%88%B7%E4%BD%BF%E7%94%A8%E6%8C%87%E5%8D%97.pdf) to learn how to get access key and secret key.

## Sina Cloud Storage

Please follow [this document](https://scs.sinacloud.com/doc/scs/guide/quick_start#accesskey) to learn how to get access key and secret key.

## UCloud US3

Please follow [this document](https://docs.ucloud.cn/uai-censor/access/key) to learn how to get access key and secret key.

## MinIO

[MinIO](https://min.io) is an open source high performance object storage. It is API compatible with Amazon S3. You need set `--storage` option to `minio`. Currently, JuiceFS only supports path-style URI when use MinIO storage. For example (`<endpoint>` may looks like `1.2.3.4:9000`):

```bash
$ ./juicefs format \
    --storage minio \
    --bucket http://<endpoint>/<bucket> \
    ... \
    localhost test
```
