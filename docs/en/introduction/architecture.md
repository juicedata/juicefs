---
sidebar_label: Architecture
sidebar_position: 2
slug: /architecture
---

# Architecture

The JuiceFS file system consists of three parts:

1. **JuiceFS Client**: coordinating object storage and metadata engine, and implementation of file system interfaces such as POSIX, Hadoop, Kubernetes CSI Driver, S3 Gateway, etc..
2. **Data Storage**: storage of the data itself, supporting media such as local disk, public or private cloud object storage, HDFS, etc.
3. **Metadata Engine**: storage data corresponding metadata contains file name, file size, permission group, creation and modification time and directory structure, etc., supporting Redis, MySQL, TiKV and other engines.

![image](../images/juicefs-arch-new.png)

As a file system, JuiceFS handles the data and its corresponding metadata separately, with the data being stored in the object store and the metadata being stored in the metadata engine.

In terms of **data storage**, JuiceFS supports almost all public cloud object stores, as well as OpenStack Swift, Ceph, MinIO and other open source object stores that support private deployments.

In terms of **metadata storage**, JuiceFS is designed with multiple engines, and currently supports Redis, TiKV, MySQL/MariaDB, PostgreSQL, SQLite, etc. as metadata service engines, and will implement more multiple data storage engines one after another. Welcome to [Submit Issue](https://github.com/juicedata/juicefs/issues) to feedback your requirements.

In terms of **File System Interface** implementation:

- With **FUSE**, the JuiceFS file system can be mounted to the server in a POSIX-compatible manner to use massive cloud storage directly as local storage.
- With **Hadoop Java SDK**, JuiceFS file system can directly replace HDFS and provide low-cost mass storage for Hadoop.
- With the **Kubernetes CSI Driver**, the JuiceFS file system can directly provide mass storage for Kubernetes.
- With **S3 Gateway**, applications using S3 as the storage layer can directly access the JuiceFS file system and use tools such as AWS CLI, s3cmd, and MinIO client.

## How JuiceFS Stores Files

The file system acts as a medium for interaction between the user and the hard drive, which allows files to be stored on the hard drive properly. As you know, Windows commonly used file systems are FAT32, NTFS, Linux commonly used file systems are Ext4, XFS, Btrfs, etc., each file system has its own unique way of organizing and managing files, which determines the file system Features such as storage capacity and performance.

As a file system, JuiceFS is no exception. Its strong consistency and high performance are inseparable from its unique file management mode.

Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS will format the data and store it in object storage (cloud storage), and store the metadata corresponding to the data in databases such as Redis. .

Any file stored in JuiceFS will be split into fixed-size **"Chunk"**, and the default upper limit is 64 MiB. Each Chunk is composed of one or more **"Slice"**. The length of the slice is not fixed, depending on the way the file is written. Each slice will be further split into fixed-size **"Block"**, which is 4 MiB by default. Finally, these blocks will be stored in the object storage. At the same time, JuiceFS will store each file and its Chunks, Slices, Blocks and other metadata information in metadata engines.

![](../images/juicefs-storage-format-new.png)

Using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you will find that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform. There is a chunks directory and a bunch of digitally numbered directories and files in the bucket. Don't panic, this is the secret of the high-performance operation of the JuiceFS file system!

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)
