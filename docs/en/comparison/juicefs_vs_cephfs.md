# JuiceFS vs. CephFS

## Similarities

Both are highly reliable, high-performance resilient distributed file systems with good POSIX compatibility, and can be tried in a variety of file system scenarios.

## Differences

### System Architecture

Both JuiceFS and CephFS employ an architecture that separates data and metadata, but differ greatly in component implementations.

#### CephFS

CephFS is a complete and independent system mainly designed for private cloud deployments, which stores all data and metadata persistently in its own storage pool (RADOS Pool).

- Metadata
  - Service process (MDS): stateless, and theoretically horizontally scalable. There are mature primary-secondary mechanisms, while performance and stability concerns still exist when deploying with multiple primaries; production environments typically adopt one-primary-multiple-secondary or multi-primary static isolation.
  - Persistent: independent RADOS storage pools, usually being used with SSD or higher performance hardware storage
- Data: stored in one or more RADOS storage pools, with supports of specifying different configurations by _Layout_ such as chunk size (default 4 MiB), redundancy (multi-copy, EC), etc.
- Client: supports kernel client (kcephfs), user state client (ceph-fuse) and libcephfs based SDKs for C++, Python, etc.; recently the community has also provided a Windows client (ceph-dokan). VFS object for Samba and FSAL module for NFS-Ganesha are also available in the ecosystem.

#### JuiceFS

JuiceFS provides a libjfs library, a FUSE client application, Java SDK, etc. It supports communicating with various metadata engines and object storages, and is suitable for deployments on public, private or hybrid cloud environments.

- Metadata: See [supported databases](../reference/how_to_setup_metadata_engine.md) for details, including:
  - Redis and various variants of the Redis-compatible protocol (transaction supports are required)
  - SQL family: MySQL, PostgreSQL, SQLite, etc.
  - Distributed K/V storage: TiKV has been supported, and planned to support Apple FoundationDB
  - Self-developed engine: a fully JuiceFS managed service designed for the public cloud.
- Data: supports for over 30 kinds of [object storages](../reference/how_to_setup_object_storage.md) on the public cloud and can also be used with MinIO, Ceph RADOS, Ceph RGW, etc.
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

CephFS splits files into [`object_size`](https://docs.ceph.com/en/latest/cephfs/file-layouts/#reading-layouts-with-getfattr) (default 4MiB). Each chunk corresponds to a RADOS object. JuiceFS, on the other hand, splits files into 64MiB chunks, and each chunk will be further split into one or more logical slice(s) according to the actual situation when writing. Each Slice will also be further split into one or more logical Blocks when writing to the object store, and each Block corresponds to one object in the object storage. When handling overwrite, CephFS needs to modify the corresponding objects directly, which is a complicated process, especially when the redundancy policy is EC or the data compression is enabled. It is because in this case, part of the object content needs to be read first, and then be modified in memory, and then be written. This costs a great performance overhead. JuiceFS writes the updated data as new objects and modifies the metadata when overwriting, which greatly improves the performance. Any redundant data that occurs during the process will be garbage collected asynchronously.

#### [2] Data Compression

Strictly speaking, CephFS does not provide data compression itself. The BlueStore compression on the RADOS layer actually does. JuiceFS, on the other hand, has already compressed data once before uploading a Block to the object storage to reduce the capacity cost in the object storage. In other words, if you use JuiceFS to interact with RADOS, you compress a Block both before and after it enters RADOS, actually twice in total. Also, as mentioned in **File Chunking**, CephFS does not normally enable BlueStore compression to guarantee the performance of overwrite writes.

#### [3] Data Encryption

On network transport layer, Ceph encrypts data by using **Messenger v2**, while on data storage layer, the data encryption is done at OSD creation, which is similar to data compression.

JuiceFS encrypts objects before uploading and decrypts them after downloading, and is completely transparent to the object storage.
