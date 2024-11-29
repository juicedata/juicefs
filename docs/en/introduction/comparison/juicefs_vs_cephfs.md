---
slug: /comparison/juicefs_vs_cephfs
description: Ceph is a unified system that provides object storage, block storage and file storage. This article compares the similarities and differences between JuiceFS and Ceph.
---

# JuiceFS vs. CephFS

This document offers a comprehensive comparison between JuiceFS and CephFS. You will learn their similarities and differences in their system architectures and features.

## Similarities

Both are highly reliable, high-performance, resilient distributed file systems with good POSIX compatibility, suitable for various scenarios.

## Differences

### System architecture

Both JuiceFS and CephFS employ an architecture that separates data and metadata, but they differ greatly in implementations.

#### CephFS

CephFS is a complete and independent system used mainly for private cloud deployments. Through CephFS, all file metadata and data are persistently stored in Ceph's distributed object store (RADOS).

- Metadata
  - Metadata Server (MDS): Stateless and theoretically horizontally scalable. There are mature primary-secondary mechanisms, while concerns about performance and stability still exist in multi-primary deployments. Production environments typically adopt one-primary-multiple-secondary or multi-primary static isolation.
  - Persistent: Independent RADOS storage pools, usually used with SSDs or higher-performance hardware storage.
- Data: Stored in one or more RADOS storage pools, supporting different configurations through _Layout_, such as chunk size (default 4 MiB) and redundancy (multi-copy, EC).
- Client: Supports kernel client (`kcephfs`), user-state client (`ceph-fuse`) and libcephfs-based SDKs for C++, Python, etc.; recently the community has also provided a Windows client (`ceph-dokan`). VFS object for Samba and an FSAL module for NFS-Ganesha are also available in the ecosystem.

#### JuiceFS

JuiceFS provides a libjfs library, a FUSE client application, Java SDK, etc. It supports various metadata engines and object storages, and can be deployed in public, private, or hybrid cloud environments.

- Metadata: Supports [various databases](../../reference/how_to_set_up_metadata_engine.md), including:
  - Redis and various variants of the Redis-compatible protocol (transaction supports are required)
  - SQL family: MySQL, PostgreSQL, SQLite, etc.
  - Distributed K/V storage: TiKV, FoundationDB, etcd
  - A self-developed engine: a JuiceFS fully managed service used on the public cloud.
- Data: Supports over 30 types of [object storage](../../reference/how_to_set_up_object_storage.md) on the public cloud and can also be used with MinIO, Ceph RADOS, Ceph RGW, etc.
- Clients: Supports Unix user-state mounting, Windows mounting, Java SDK with full HDFS semantic compatibility, [Python SDK](https://github.com/megvii-research/juicefs-python), and a built-in S3 gateway.

### Features

| Comparison basis                | CephFS                | JuiceFS               |
| ------------------------------- | --------------------- | --------------------- |
| File chunking<sup> [1]</sup>    | ✓                     | ✓                     |
| Metadata transactions           | ✓                     | ✓                     |
| Strong consistency              | ✓                     | ✓                     |
| Kubernetes CSI Driver           | ✓                     | ✓                     |
| Hadoop-compatible               | ✓                     | ✓                     |
| Data compression<sup> [2]</sup> | ✓                     | ✓                     |
| Data encryption<sup> [3]</sup>  | ✓                     | ✓                     |
| Snapshot                        | ✓                     | ✕                     |
| Client data caching             | ✕                     | ✓                     |
| Hadoop data locality            | ✕                     | ✓                     |
| S3-compatible                   | ✕                     | ✓                     |
| Quota                           | Directory level quota | Directory level quota |
| Languages                       | C++                   | Go                    |
| License                         | LGPLv2.1 & LGPLv3     | Apache License 2.0    |

#### [1] File chunking

CephFS splits files by [`object_size`](https://docs.ceph.com/en/latest/cephfs/file-layouts/#reading-layouts-with-getfattr) (default 4MiB). Each chunk corresponds to a RADOS object.  In contrast, JuiceFS splits files into 64MiB chunks and it further divides each chunk into logical slices during writing according to the actual situation. These slices are then split into logical blocks when writing to the object store, with each block corresponding to an object in the object storage. When handling overwrites, CephFS modifies corresponding objects directly, which is a complicated process. Especially, when the redundancy policy is EC or the data compression is enabled, part of the object content needs to be read first, modified in memory, and then written. This leads to great performance overhead. In comparison, JuiceFS handles overwrites by writing the updated data as new objects and modifying the metadata at the same time, which greatly improves the performance. Any redundant data generated during the process will go to garbage collection asynchronously.

#### [2] Data compression

Strictly speaking, CephFS itself does not provide data compression but relies on the BlueStore compression on the RADOS layer. JuiceFS, on the other hand, has already compressed data once before uploading a block to the object storage to reduce the capacity cost in the object storage. In other words, if you use JuiceFS to interact with RADOS, you compress a block both before and after it enters RADOS, twice in total. Also, as mentioned in **File chunking**, to guarantee overwrite performance, CephFS usually does not enable the BlueStore compression.

#### [3] Data encryption

On network transport layer, Ceph encrypts data by using **Messenger v2**, while on data storage layer, the data encryption is done at OSD creation, which is similar to data compression.

JuiceFS encrypts objects before uploading and decrypts them after downloading. This is completely transparent to the object storage.
