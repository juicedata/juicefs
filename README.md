# juicesync

Juicesync is a tool to move your data in object storage between any clouds or regions.

# How it works?

juicesync will scan all the keys from two object stores, and comparing them in ascending order to find out missing or outdated keys, then download them from the source and upload them to the destination in parallel.

# Install

## With Homebrew

```sh
brew install juicedata/tap/juicesync
```

## With go

After installed Go-1.9+

```
go get github.com/juicedata/juicesync
$HOME/go/bin/juicesync
```

We assume your GOPATH is `$HOME/go`. How to set GOPATH? Please visit [The
official document](https://github.com/golang/go/wiki/SettingGOPATH)

# Develop

* If you're using Go 1.13

	```
	go build
	```

* If you're using Go >= 1.11, < 1.13

	```
	export GO111MODULE=on
	go build
	```

* If you're using Go < 1.11, use classic `$GOPATH` + `vendor` to build.

# Upgrade

```
go get -u github.com/juicedata/juicesync
```

# Usage

```
juicesync [options] SRC DST

Options:
  -end string
    	the last keys to sync
  -p int
    	number of concurrent threads (default 50)
  -q	change log level to ERROR
  -start string
    	the start of keys to sync
  -v	turn on debug log
  --help show the usage
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
