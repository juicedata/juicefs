# juicesync

Sync object storage between clouds.

# Install

After installed Go-1.9+

```
go get github.com/juicedata/juicesync
```

# Usage

```
juicesync [options] SRC DST
```

SRC and DST must be an URI of the following object storage:

- file: local files
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

Some examples:

- file://Users/me/code/
- s3://my-bucket.us-east1.amazonaws.com/
- s3://access-key:secret-key-id@my-bucket.us-west2.s3.amazonaws.com/prefix
- gcs://my-bucket.us-west1.googleapi.com/
- oss://test.oss-us-west-1.aliyuncs.com
- cos://test-1234.cos.ap-beijing.myqcloud.com
