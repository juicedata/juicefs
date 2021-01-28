# How to Setup Object Storage

This is a guide about how to setup object storage when format a volume. Different object storage may has different option value. Check the specific object storage for your need.

## Access key and secret key

For authentication, the access key and secret key are needed. You could specify them through `--access-key` and `--secret-key` options. Or you can set `ACCESS_KEY` and `SECRET_KEY` environment variables.

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

## MinIO

[MinIO](https://min.io) is an open source high performance object storage. It is API compatible with Amazon S3. You need set `--storage` option to `minio`. Currently, JuiceFS only supports path-style URI when use MinIO storage. For example (`<endpoint>` may looks like `1.2.3.4:9000`):

```bash
$ ./juicefs format \
    --storage minio \
    --bucket http://<endpoint>/<bucket> \
    ... \
    localhost test
```
