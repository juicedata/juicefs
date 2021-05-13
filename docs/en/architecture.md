# JuiceFS Architecture

JuiceFS file system consists of three parts: 

1. **JuiceFS Client**: Coordinate the implementation of object storage and metadata storage engines, as well as file system interfaces such as POSIX, Hadoop, Kubernetes, and S3 gateway.
2. **Data Storage**: Store the data itself, support local disk and object storage.
3. **Metadata Engine**: Metadata corresponding to the stored data, supporting multiple engines such as Redis, MySQL, and SQLite;

![](../images/juicefs-arch-new.png?lastModify=1620808685)

As a file system, JuiceFS will process data and its corresponding metadata separately, the data will be stored in the object storage, and the metadata will be stored in the metadata engine.

In terms of **data storage**, JuiceFS supports almost all public cloud object storage services, as well as privatized object storage such as OpenStack Swift, Ceph, and MinIO.

In terms of **metadata storage**, JuiceFS adopts a multi-engine design, and currently supports [Redis](https://redis.io/), MySQL/MariaDB, SQLite as metadata service engines, and will continue to implement more metadata engine. Welcome to [Submit Issue](https://github.com/juicedata/juicefs/issues) to feedback your needs!

In terms of the implementation of **file system interface**:

- With **FUSE**, the JuiceFS file system can be mounted to the server in a POSIX compatible manner, and the massive cloud storage can be used directly as local storage.
- With **Hadoop Java SDK**, the JuiceFS file system can directly replace HDFS, providing Hadoop with low-cost mass storage.
- With **Kubernetes CSI driver**, the JuiceFS file system can directly provide mass storage for Kubernetes.
- Through **S3 Gateway**, applications that use S3 as the storage layer can be directly accessed, and tools such as AWS CLI, s3cmd, and MinIO client can be used to access the JuiceFS file system.

## Go further

Now, you can refer to [Quick Start Guide](quick_start_guide.md) to start using JuiceFS immediately!

You can also learn more about [How JuiceFS stores files](how_juicefs_store_files.md)

