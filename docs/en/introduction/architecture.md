---
title: Architecture
sidebar_position: 2
slug: /architecture
description: This article introduces the technical architecture of JuiceFS and its technical advantages.
---

The JuiceFS file system consists of three parts:

![](../images/juicefs-arch-new.png)

**JuiceFS Client**: All file I/O happens in JuiceFS Client, this even includes background jobs like data compaction and trash file expiration. So obviously, JuiceFS Client talk to both object storage and metadata service. A variety of implementations are supported:

- **FUSE**, JuiceFS file system can be mounted on host in a POSIX-compatible manner, allowing the massive cloud storage to be used as a local storage.
- **Hadoop Java SDK**, JuiceFS can replace HDFS and provide massive storage for Hadoop at a significantly lower cost.
- **Kubernetes CSI Driver**, use JuiceFS CSI Driver in Kubernetes to provide shared storage for containers.
- With **S3 Gateway**, applications using S3 as the storage layer can directly access JuiceFS file system, and tools such as AWS CLI, s3cmd, and MinIO client are also allowed to be used to access to the JuiceFS file system at the same time.
- With **WebDAV Server**, files in JuiceFS can be operated directly using HTTP protocol.

**Data Storage**: File data will be split into chunks and stored in object storage, you can use object storage provided by public cloud services, or self-hosted, JuiceFS supports virtually all types of object storage, including typical self-hosted ones like OpenStack Swift, Ceph, and MinIO.

**Metadata Engine**: Metadata Engine stores file metadata, which contains:

- Common file system metadata: file name, size, permission information, creation and modification time, directory structure, file attribute, symbolic link, file lock.
- JuiceFS specific metadata: file inode, chunk and slice mapping, client session, etc.

JuiceFS supports a variety of common databases as metadata engine, like Redis, TiKV, MySQL/MariaDB, PostgreSQL, SQLite...and the list is still expanding. [Submit an issue](https://github.com/juicedata/juicefs/issues) if your favorite database isn't supported.

## How JuiceFS Stores Files {#how-juicefs-store-files}

Traditional file systems use local disks to store both file data and metadata, JuiceFS formats data first and then stores them in object storage, with the corresponding metadata being stored in the metadata engine.

"Slice" is the data structure designed for writes, with a maximum size of 64M. Every `flush` operation creates a new slice, so if a file is written via a single `flush`, then it'll contain just a single slice. `flush` can be explicitly invoked by user, but if not, JuiceFS Client will automatically carry out `flush` to avoid buffer being written to full.

![](../images/data-structure-diagram.svg)

After creation, file may be edited, then new writes will form new slices, stacked on top of existing slices. If file is changed on multiple regions again and again, then a large number of slices will be created. When file is read, imagine using the below diagram, you can see how JuiceFS Client will have to look for "every slice that contains the latest file data" in order to perform a correct read, and too many slices will affect read performance. That's why JuiceFS performs compaction in the background, which will merge overlapping slices into one.

Slice is a data structure solely aimed to improve write performance, files are further divided into 64M "Chunk"s, so that large files may enjoy better read performance, through a divide-and-conquer strategy. One chunk will reference one or more slices, and [mark the valid data offsets in the reference relationship](../development/internals.md#sliceref).

Chunk and slice are all logical data structures, when it comes to physical storage, slice is further divided into "Block"s (use a default max size of 4M), the most basic storage unit in the system, and uploaded to object storage. Block is also the basic storage unit in disk cache.

![](../images/how-juicefs-stores-files-new.png)

Hence, you cannot find the original files directly in the object storage, instead there's only a `chunks` folder and a bunch of numbered directories and files in the bucket, don't panic, this is exactly how JuiceFS formats and stores data. At the same time, file and its relationship with chunks, slices, blocks will be stored in the metadata engine. This decoupled design is what makes JuiceFS a high-performance file system.

Some other technical aspects of JuiceFS storage design:

* Files (any size) are not merged and stored. This is for performance considerations and to avoid read amplification.
* Provides strong consistency guarantee, but can be tuned for performance in different scenarios, e.g. deliberately adjust metadata cache policies, to trade consistency for performance. Learn more at [Metadata cache](../guide/cache_management.md#metadata-cache).
* Support ["Trash"](../security/trash.md) functionality, and enabled by default. Deleted files are kept for a specified amount of time, to help you avoid data loss caused by accidental deletion.
