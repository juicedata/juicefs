# How to Setup Object Storage

This is a guide about how to setup object storage when format a volume. Different object storage may has different option value. Check the specific object storage for your need.

## Access key and secret key

For authentication, the access key and secret key are needed. You could specify them through `--access-key` and `--secret-key` options. Or you can set `ACCESS_KEY` and `SECRET_KEY` environment variables.

## S3

S3 supports [two style URI](https://docs.aws.amazon.com/AmazonS3/latest/dev/VirtualHosting.html): virtual hosted-style and path-style. The difference between virtual hosted-style and path-style is:

- Virtual hosted-style: `https://<bucket>.s3.<region>.amazonaws.com`
- Path-style: `https://s3.<region>.amazonaws.com/<bucket>`

***Note: For AWS China user, you need add `.cn` to the host, i.e. `amazonaws.com.cn`.***

Currently, JuiceFS only supports virtual hosted-style and maybe support path-style in the future. So when you format a volume, the `--bucket` option should be virtual hosted-style URI. For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    --access-key XXX \
    --secret-key XXX \
    localhost test
```

You can also use S3 storage type to connect S3-compatible storage. But beware that you still need use virtual hosted-style URI. For example:

```bash
$ ./juicefs format \
    --storage s3 \
    --bucket https://<bucket>.<endpoint> \
    --access-key XXX \
    --secret-key XXX \
    localhost test
```

## MinIO

[MinIO](https://min.io) is an open source high performance object storage. It is API compatible with Amazon S3. You need set `--storage` option to `minio`. Currently, JuiceFS supports path-style URI when use MinIO storage. For example (`<endpoint>` may looks like `1.2.3.4:9000`):

```bash
$ ./juicefs format \
    --storage minio \
    --bucket http://<endpoint>/<bucket> \
    --access-key XXX \
    --secret-key XXX \
    localhost test
```
