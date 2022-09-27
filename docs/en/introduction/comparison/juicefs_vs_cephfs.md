---
slug: /comparison/juicefs_vs_cephfs
---

# JuiceFS vs. CephFS

## Similarities

Both are highly reliable, high-performance resilient distributed file systems with good POSIX compatibility, and can be tried in a variety of file system scenarios.

## Differences

### System Architecture

Both JuiceFS and CephFS employ an architecture that separates data and metadata, but differ greatly in component implementations.

#### CephFS

CephFS is a complete and independent system used mainly for private cloud deployments. Through CephFS, all file metadata and data are persistently stored in Ceph's distributed object store (RADOS).

- Metadata
  - Metadata Server (MDS): stateless, and theoretically horizontally scalable. There are mature primary-secondary mechanisms, while performance and stability concerns still exist when deploying with multiple primaries; production environments typically adopt one-primary-multiple-secondary or multi-primary static isolation.
  - Persistent: independent RADOS storage pools, usually being used with SSD or higher performance hardware storage
- Data: stored in one or more RADOS storage pools, with supports of specifying different configurations by _Layout_ such as chunk size (default 4 MiB), redundancy (multi-copy, EC), etc.
- Client: supports kernel client (kcephfs), user state client (ceph-fuse) and libcephfs based SDKs for C++, Python, etc.; recently the community has also provided a Windows client (ceph-dokan). VFS object for Samba and FSAL module for NFS-Ganesha are also available in the ecosystem.

#### JuiceFS

JuiceFS provides a libjfs library, a FUSE client application, Java SDK, etc. It supports various metadata engines and object storages, and can be deployed in public, private or hybrid cloud environments.

- Metadata: See [supported databases](../../guide/how_to_set_up_metadata_engine.md) for details, including:
  - Redis and various variants of the Redis-compatible protocol (transaction supports are required)
  - SQL family: MySQL, PostgreSQL, SQLite, etc.
  - Distributed K/V storage: TiKV (Apple FoundationDB will be supported in the future)
  - Self-developed engine: a JuiceFS fully managed service used on the public cloud.
- Data: supports for over 30 kinds of [object storages](../../guide/how_to_set_up_object_storage.md) on the public cloud and can also be used with MinIO, Ceph RADOS, Ceph RGW, etc.
- Clients: supports Unix user state mounting, Windows mounting, Java SDK with full HDFS semantic compatibility, [Python SDK](https://github.com/megvii-research/juicefs-python) and a built-in S3 gateway.

### Features

|                                 | CephFS                | JuiceFS            |
| -----------------------         | ----------            | -------------      |
| File chunking<sup> [1]</sup>    | ✓                     | ✓                  |
| Metadata transactions           | ✓                     | ✓                  |
| Strong consistency              | ✓                     | ✓                  |
| Kubernetes CSI Driver           | ✓                     | ✓                  |
| Hadoop-compatible               | ✓                     | ✓                  |
| Data compression<sup> [2]</sup> | ✓                     | ✓                  |
| Data encryption<sup> [3]</sup>  | ✓                     | ✓                  |
| Snapshot                        | ✓                     | ✕                  |
| Client data caching             | ✕                     | ✓                  |
| Hadoop data Locality            | ✕                     | ✓                  |
| S3-compatible                   | ✕                     | ✓                  |
| Quota                           | Directory level quota | Volume level quota |
| Languages                       | C++                   | Go                 |
| License                         | LGPLv2.1 & LGPLv3     | Apache License 2.0             |

#### [1] File Chunking

CephFS splits files by [`object_size`](https://docs.ceph.com/en/latest/cephfs/file-layouts/#reading-layouts-with-getfattr) (default 4MiB). Each chunk corresponds to a RADOS object. JuiceFS, on the other hand, splits files into 64MiB chunks, and each chunk will be further split into one or more logical slice(s) according to the actual situation when writing. Each Slice will then be further split into one or more logical Block(s) when writing to the object store, and each Block corresponds to one object in the object storage. When handling overwrites, CephFS needs to modify the corresponding objects directly, which is a complicated process. Especially, when the redundancy policy is EC or the data compression is enabled, part of the object content needs to be read first, modified in memory, and then written, which costs a great performance overhead. In comparison, JuiceFS handles overwrites by writing the updated data as new objects and modifying the metadata at the same time, which greatly improves the performance
; also, any redundant data that is generated during the process will go to garbage collection asynchronously.

#### [2] Data Compression

Strictly speaking, CephFS itself does not provide data compression but relies on the BlueStore compression on the RADOS layer. JuiceFS, on the other hand, has already compressed data once before uploading a Block to the object storage to reduce the capacity cost in the object storage. In other words, if you use JuiceFS to interact with RADOS, you compress a Block both before and after it enters RADOS, twice in total. Also, as mentioned in **File Chunking**, to guarantee the overwrite performance, CephFS usually does not enable the BlueStore compression.

#### [3] Data Encryption

On network transport layer, Ceph encrypts data by using **Messenger v2**, while on data storage layer, the data encryption is done at OSD creation, which is similar to data compression.

JuiceFS encrypts objects before uploading and decrypts them after downloading, and is completely transparent to the object storage.
