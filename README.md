# juicesync

![build](https://github.com/juicedata/juicesync/workflows/build/badge.svg) ![release](https://github.com/juicedata/juicesync/workflows/release/badge.svg)

Juicesync is a tool to move your data in object storage between any clouds or regions.

# How it works?

Juicesync will scan all the keys from two object stores, and comparing them in ascending order to find out missing or outdated keys, then download them from the source and upload them to the destination in parallel.

# Install

## With Homebrew

```sh
brew install juicedata/tap/juicesync
```

## Download binary release

From [here](https://github.com/juicedata/juicesync/releases)

# Develop

We use go mod to manage modules, if not sure how to use this, refer to [The official document](https://github.com/golang/go/wiki/Modules).

* If you're using Go 1.13

	```
	go build
	```

* If you're using Go >= 1.11, < 1.13

	```
	export GO111MODULE=on
	go build
	```

# Upgrade

* Use Homebrew to upgrade or
* Download a new version from [release page](https://github.com/juicedata/juicesync/releases)

# Usage

```
$ juicesync -h
NAME:
   juicesync - Usage: juicesync [options] SRC DST
    SRC and DST should be [NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET.ENDPOINT[/PREFIX]

USAGE:
   juicesync [global options] command [command options] [arguments...]

VERSION:
   v0.0.5-4-gdd37495

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --start value, -s value    the first key to sync [$JUICESYNC_START]
   --end value, -e value      the last key to sync [$JUICESYNC_END]
   --threads value, -p value  number of concurrent threads (default: 50) [$JUICESYNC_THREADS]
   --http-port value          http port to listen to (default: 6070) [$JUICESYNC_HTTP_PORT]
   --update, -u               update existing file if the source is newer (default: false) [$JUICESYNC_UPDATE]
   --dry                      don't copy file (default: false) [$JUICESYNC_DRY]
   --delete-src, --deleteSrc  delete objects from source after synced (default: false) [$JUICESYNC_DELETE_SRC]
   --delete-dst, --deleteDst  delete extraneous objects from destination (default: false) [$JUICESYNC_DELETE_DST]
   --verbose, -v              turn on debug log (default: false) [$JUICESYNC_VERBOSE]
   --quiet, -q                change log level to ERROR (default: false) [$JUICESYNC_QUIET]
   --help, -h                 show help (default: false)
   --version, -V              print only the version (default: false)
```

SRC and DST must be an URI of the following object storage:

- file: local files
- sftp: FTP via SSH
- s3: Amazon S3
- gcs: Google Cloud Storage
- wasb: Windows Azure Blob Storage
- oss: Aliyun OSS
- cos: Tencent Cloud COS
- ks3: KSYun KS3
- ufile: UCloud UFile
- qingstor: Qingcloud QingStor
- bos: Baidu Cloud Object Storage
- jss: JCloud Object Storage
- qiniu: Qiniu
- b2: Backblaze B2
- space: Digital Ocean Space
- obs: Huawei Object Storage Service

SRC and DST should be in the following format:

[NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET.ENDPOINT[/PREFIX]

Some examples:

- local/path
- user@host:path
- file:///Users/me/code/
- s3://my-bucket.us-east1.amazonaws.com/
- s3://access-key:secret-key-id@my-bucket.us-west2.s3.amazonaws.com/prefix
- gcs://my-bucket.us-west1.googleapi.com/
- oss://test.oss-us-west-1.aliyuncs.com
- cos://test-1234.cos.ap-beijing.myqcloud.com
- obs://test.obs.cn-north-1.myhwclouds.com

Note:

- It's recommended to run juicesync in the target region to have better performance.
- S3: The access key and secret key for S3 could be provided by AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or IAM role.
- COS: The AppID should be part of the bucket name.
- GCS: The machine should be authorized to access Google Cloud Storage.
- Qiniu:
  The S3 endpoint should be used for Qiniu, for example, abc.cn-north-1-s3.qiniu.com.
  If there are keys starting with "/", the domain should be provided as QINIU_DOMAIN.
- sftp: if your target machine uses SSH certificates instead of password, you should pass the path to your private key file to the environment variable `SSH_PRIVATE_KEY_PATH`, like ` SSH_PRIVATE_KEY_PATH=/home/someuser/.ssh/id_rsa juicesync [src] [dst]`, and then leave password empty.