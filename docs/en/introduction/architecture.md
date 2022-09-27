---
sidebar_label: Architecture
sidebar_position: 2
slug: /architecture
---

# Architecture

The JuiceFS file system consists of three parts:

1. **JuiceFS Client**: Coordinates object storage and metadata engine as well as implementation of file system interfaces such as POSIX, Hadoop, Kubernetes CSI Driver, S3 Gateway.
2. **Data Storage**: Stores data, with supports of a variety of data storage media, e.g., local disk, public or private cloud object storage, and HDFS.
3. **Metadata Engine**: Stores the corresponding metadata that contains information of file name, file size, permission group, creation and modification time and directory structure, etc., with supports of different metadata engines, e.g., Redis, MySQL and TiKV.

![image](../images/juicefs-arch-new.png)

As a file system, JuiceFS handles the data and its corresponding metadata separately: the data is stored in object storage and the metadata is stored in metadata engine.

In terms of **data storage**, JuiceFS supports almost all kinds of public cloud object storage as well as other open source object storage that support private deployments, e.g., OpenStack Swift, Ceph, and MinIO.

In terms of **metadata storage**, JuiceFS is designed with multiple engines, and currently supports Redis, TiKV, MySQL/MariaDB, PostgreSQL, SQLite, etc., as metadata service engines. More metadata storage engines will be implemented soon. Welcome to [Submit Issue](https://github.com/juicedata/juicefs/issues) to feedback your requirements.

In terms of **File System Interface** implementation:

- With **FUSE**, JuiceFS file system can be mounted to the server in a POSIX-compatible manner, which allows the massive cloud storage to be used as a local storage.
- With **Hadoop Java SDK**, JuiceFS file system can replace HDFS directly and provide massive storage for Hadoop at a low cost.
- With the **Kubernetes CSI Driver**, JuiceFS file system provides mass storage for Kubernetes.
- With **S3 Gateway**, applications using S3 as the storage layer can directly access JuiceFS file system, and tools such as AWS CLI, s3cmd, and MinIO client are also allowed to be used to access to the JuiceFS file system at the same time.
- With **WebDAV Server**, files in JuiceFS can be operated directly using HTTP protocol.


## How JuiceFS Stores Files

The file system acts as a medium for interaction between user and hard drive, which allows files to be stored on the hard drive properly. As you know, the file systems FAT32 and NTFS are commonly used on Windows, while Ext4, XFS and Btrfs are commonly used on Linux. Each file system has its own unique way of organizing and managing files, which determines the file system features such as storage capacity and performance.

The strong consistency and high performance of JuiceFS is ascribed to its unique file management mode. Unlike the traditional file systems that can only use local disks to store data and the corresponding metadata, JuiceFS formats data first and then store the data in object storage (cloud storage) with the corresponding metadata being stored in databases such as Redis.

Each file stored in JuiceFS is split into **"Chunk"** s at a fixed size with the default upper limit of 64 MiB. Each Chunk is composed of one or more **"Slice"**(s), and the length of the slice varies depending on how the file is written. Each slice is composed of size-fixed **"Block"** s, which are 4 MiB by default. These blocks will be stored in object storage in the end; at the same time, the metadata information of the file and its Chunks, Slices, and Blocks will be stored in metadata engines via JuiceFS.

![](../images/juicefs-storage-format-new.png)

When using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you may notice that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform; instead, there are only a directory of chunks and a bunch of numbered directories and files in the bucket. Don't panic! That's exactly what makes JuiceFS a high-performance file system.

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)
