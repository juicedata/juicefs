
The following object store are supported:

- file: local files
- sftp: FTP via SSH
- s3: Amazon S3
- hdfs: Hadoop File System (HDFS)
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
- oos: CTYun OOS
- scw: Scaleway Object Storage
- minio: MinIO
- scs: Sina Cloud Storage
- eos: ECloud (China Mobile Cloud) Object Storage

they should be specified in the following format:

[NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET[.ENDPOINT][/PREFIX]

Some examples:

- local/path
- user@host:port:path
- file:///Users/me/code/
- hdfs://hdfs@namenode1:9000,namenode2:9000/user/
- s3://my-bucket/
- s3://access-key:secret-key-id@my-bucket/prefix
- wasb://account-name:account-key@my-container/prefix
- gcs://my-bucket.us-west1.googleapi.com/
- oss://test
- cos://test-1234
- obs://my-bucket
- bos://my-bucket
- minio://myip:9000/bucket
- scs://access-key:secret-key-id@my-bucket.sinacloud.net/prefix

Note:

- It's recommended to run it in the target region to have better performance.
- Auto discover endpoint for bucket of S3, OSS, COS, OBS, BOS, `SRC` and `DST` can use format `NAME://[ACCESS_KEY:SECRET_KEY@]BUCKET[/PREFIX]` . `ACCESS_KEY` and `SECRET_KEY` can be provided by corresponding environment variables (see below).
- S3:
  * The access key and secret key for S3 could be provided by `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, or *IAM* role.
- Wasb(Windows Azure Storage Blob)
  * The account name and account key can be provided as [connection string](https://docs.microsoft.com/en-us/azure/storage/common/storage-configure-connection-string#configure-a-connection-string-for-an-azure-storage-account) by `AZURE_STORAGE_CONNECTION_STRING`.
- GCS: The machine should be authorized to access Google Cloud Storage.
- OSS:
  * The credential can be provided by environment variable `ALICLOUD_ACCESS_KEY_ID` and `ALICLOUD_ACCESS_KEY_SECRET` , RAM role, [EMR MetaService](https://help.aliyun.com/document_detail/43966.html).
- COS:
  * The AppID should be part of the bucket name.
  * The credential can be provided by environment variable `COS_SECRETID` and `COS_SECRETKEY`.
- OBS:
  * The credential can be provided by environment variable `HWCLOUD_ACCESS_KEY` and `HWCLOUD_SECRET_KEY` .
- BOS:
  * The credential can be provided by environment variable `BDCLOUD_ACCESS_KEY` and `BDCLOUD_SECRET_KEY` .
- Qiniu:
  The S3 endpoint should be used for Qiniu, for example, abc.cn-north-1-s3.qiniu.com.
  If there are keys starting with "/", the domain should be provided as `QINIU_DOMAIN`.
- sftp: if your target machine uses SSH certificates instead of password, you should pass the path to your private key file to the environment variable `SSH_PRIVATE_KEY_PATH`, like ` SSH_PRIVATE_KEY_PATH=/home/someuser/.ssh/id_rsa juicefs sync [src] [dst]`.
- Scaleway:
  * The credential can be provided by environment variable `SCW_ACCESS_KEY` and `SCW_SECRET_KEY` .
- MinIO:
  * The credential can be provided by environment variable `MINIO_ACCESS_KEY` and `MINIO_SECRET_KEY` .
