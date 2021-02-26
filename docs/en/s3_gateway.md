# S3 Gateway

JuiceFS S3 Gateway is a service which provides S3-compatible interface. It means you could interact with JuiceFS by existing tools, e.g. AWS CLI, s3cmd, MinIO client (`mc`). The JuiceFS S3 Gateway is based on [MinIO S3 Gateway](https://docs.min.io/docs/minio-gateway-for-s3.html).

## Prerequisites

Before running the gateway, you need first formatting a volume. Please [follow the steps](../../README.md#getting-started) in README.

JuiceFS S3 Gateway is a feature introduced in v0.11.0, please ensure you have latest version of JuiceFS.

## Quickstart

Use `juicefs gateway` command to run the gateway, [most options](command_reference.md#juicefs-gateway) of this command are same as `juicefs mount`, except following options:

```
--access-log value  path for JuiceFS access log
--no-banner         disable MinIO startup information (default: false)
```

The `--access-log` option controls where to store [access log](fault_diagnosis_and_analysis.md#access-log) of JuiceFS. By default access log will not be stored. The `--no-banner` option controls if disable logs from MinIO.

MinIO S3 Gateway requires two environment variables been configured before startup: `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD`. You can set them with any value, but must meet the length requirements. `MINIO_ROOT_USER` length should be at least 3, and `MINIO_ROOT_PASSWORD` length at least 8 characters.

The following command shows how to run a gateway. The Redis address is `localhost:6379`, and the gateway is listening on `localhost:9000`.

```bash
$ export MINIO_ROOT_USER=admin
$ export MINIO_ROOT_PASSWORD=12345678
$ juicefs gateway redis://localhost:6379 localhost:9000
```

If the gateway is running successfully, you could visit [http://localhost:9000](http://localhost:9000) in the browser:

![MinIO browser](../images/minio-browser.png)

## Use AWS CLI

Install AWS CLI from [https://aws.amazon.com/cli](https://aws.amazon.com/cli). Then you need configure it:

```bash
$ aws configure
AWS Access Key ID [None]: admin
AWS Secret Access Key [None]: 12345678
Default region name [None]:
Default output format [None]:
```

The Access Key ID is same as `MINIO_ROOT_USER`, and Secret Access Key is same as `MINIO_ROOT_PASSWORD`. Region name and output format could be empty.

After that, you could use `aws s3` command to access the gateway, for example:

```bash
# List buckets
$ aws --endpoint-url http://localhost:9000 s3 ls

# List objects in bucket
$ aws --endpoint-url http://localhost:9000 s3 ls s3://<bucket>
```

## Use MinIO Client

Install MinIO client from [https://docs.min.io/docs/minio-client-complete-guide.html](https://docs.min.io/docs/minio-client-complete-guide.html). Then add a new host called `juicefs`:

```bash
$ mc alias set juicefs http://localhost:9000 admin 12345678 --api S3v4
```

After that, you could use `mc` command to access the gateway, for example:

```bash
# List buckets
$ mc ls juicefs

# List objects in bucket
$ mc ls juicefs/<bucket>
```
