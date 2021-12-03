---
sidebar_label: Architecture
sidebar_position: 2
slug: /architecture
---

# Architecture

JuiceFS file system consists of three parts:

1. **JuiceFS Client**: Coordinate the implementation of object storage and metadata storage engines, as well as file system interfaces such as POSIX, Hadoop, Kubernetes, and S3 gateway.
2. **Data Storage**: Store the data itself, support local disk and object storage.
3. **Metadata Engine**: Metadata corresponding to the stored data, supporting multiple engines such as Redis, MySQL, and TiKV;

![](../images/juicefs-arch-new.png)

As a file system, JuiceFS will process data and its corresponding metadata separately, the data will be stored in the object storage, and the metadata will be stored in the metadata engine.

In terms of **data storage**, JuiceFS supports almost all public cloud object storage services, as well as privatized object storage such as OpenStack Swift, Ceph, and MinIO.

In terms of **metadata storage**, JuiceFS adopts a multi-engine design, and currently supports [Redis](https://redis.io/), MySQL/MariaDB, TiKV as metadata service engines, and will continue to implement more metadata engine. Welcome to [Submit Issue](https://github.com/juicedata/juicefs/issues) to feedback your needs!

In terms of the implementation of **file system interface**:

- With **FUSE**, the JuiceFS file system can be mounted to the server in a POSIX compatible manner, and the massive cloud storage can be used directly as local storage.
- With **Hadoop Java SDK**, the JuiceFS file system can directly replace HDFS, providing Hadoop with low-cost mass storage.
- With **Kubernetes CSI driver**, the JuiceFS file system can directly provide mass storage for Kubernetes.
- Through **S3 Gateway**, applications that use S3 as the storage layer can be directly accessed, and tools such as AWS CLI, s3cmd, and MinIO client can be used to access the JuiceFS file system.

## How JuiceFS Stores Files

The file system acts as a medium for interaction between the user and the hard drive, which allows files to be stored on the hard drive properly. As you know, Windows commonly used file systems are FAT32, NTFS, Linux commonly used file systems are Ext4, XFS, Btrfs, etc., each file system has its own unique way of organizing and managing files, which determines the file system Features such as storage capacity and performance.

As a file system, JuiceFS is no exception. Its strong consistency and high performance are inseparable from its unique file management mode.

Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS will format the data and store it in object storage (cloud storage), and store the metadata corresponding to the data in databases such as Redis. .

Any file stored in JuiceFS will be split into fixed-size **"Chunk"**, and the default upper limit is 64 MiB. Each Chunk is composed of one or more **"Slice"**. The length of the slice is not fixed, depending on the way the file is written. Each slice will be further split into fixed-size **"Block"**, which is 4 MiB by default. Finally, these blocks will be stored in the object storage. At the same time, JuiceFS will store each file and its Chunks, Slices, Blocks and other metadata information in metadata engines.

![](../images/juicefs-storage-format-new.png)

Using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you will find that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform. There is a chunks directory and a bunch of digitally numbered directories and files in the bucket. Don't panic, this is the secret of the high-performance operation of the JuiceFS file system!

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

## Go further

Now, you can refer to [Quick Start Guide](../getting-started/quick_start_guide.md) to start using JuiceFS immediately!

You can also learn more about [How JuiceFS stores files](../reference/how_juicefs_store_files.md)
