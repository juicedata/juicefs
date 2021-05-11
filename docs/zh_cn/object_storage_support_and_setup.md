# JuiceFS 支持的对象存储和设置指南

通过阅读 [JuiceFS 的技术架构](architecture.md) 和 [JuiceFS 如何存储文件](how_juicefs_store_files.md)，你会了解到 JuiceFS 被设计成了一种将数据和元数据独立存储的架构，通常来说，数据被存储在以对象存储为主的云存储中，而数据所对应的元数据则被存储在独立的数据库中。

 [JuiceFS 快速上手指南](quick_start_guide.md) 已经详细的介绍了使用 JuiceFS 创建和挂载文件系统的过程，例如使用以下命令创建文件系统：

```shell
$ juicefs format \
	--storage minio \
	--bucket http://127.0.0.1:9000/pics \
	--access-key minioadmin \
	--secret-key minioadmin \
	redis://127.0.0.1:6379/1 \
	pics
```

在上述命令中，通过 `--storage` 指定使用 `minio` 对象存储，通过 `--bucket` 指定 minio 的存储桶地址，通过 `--access-key` 和 `--secret-key` 这两个选项设置访问 minio 存储桶的密钥。

下表列出了 JuiceFS 已经支持的对象存储：

| Name                                       | Value      |
| ------------------------------------------ | ---------- |
| Amazon S3                                  | `s3`       |
| Google Cloud Storage                       | `gs`       |
| Azure Blob Storage                         | `wasb`     |
| Backblaze B2 Cloud Storage                 | `b2`       |
| IBM Cloud Object Storage                   | `ibmcos`   |
| Scaleway Object Storage                    | `scw`      |
| DigitalOcean Spaces Object Storage         | `space`    |
| Wasabi Cloud Object Storage                | `wasabi`   |
| Alibaba Cloud Object Storage Service       | `oss`      |
| Tencent Cloud Object Storage               | `cos`      |
| Huawei Cloud Object Storage Service        | `obs`      |
| Baidu Object Storage                       | `bos`      |
| Kingsoft Cloud Standard Storage Service    | `ks3`      |
| Meituan Storage Service                    | `mss`      |
| NetEase Object Storage                     | `nos`      |
| QingStor Object Storage                    | `qingstor` |
| Qiniu Cloud Object Storage                 | `qiniu`    |
| Sina Cloud Storage                         | `scs`      |
| CTYun Object-Oriented Storage              | `oos`      |
| ECloud (China Mobile Cloud) Object Storage | `eos`      |
| SpeedyCloud Object Storage                 | `speedy`   |
| UCloud US3                                 | `ufile`    |
| Ceph RADOS                                 | `ceph`     |
| Ceph Object Gateway (RGW)                  | `s3`       |
| Swift                                      | `swift`    |
| MinIO                                      | `minio`    |
| HDFS                                       | `hdfs`     |
| Redis                                      | `redis`    |
| Local disk                                 | `file`     |

