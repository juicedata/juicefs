# JuiceFS vs. CephFS

## Similarities

Both are highly reliable, high-performance resilient distributed file systems with good POSIX compatibility, and can be tried in a variety of file system scenarios.

## Differences

### System Architecture

Both use an architecture that separates data and metadata, but there are significant differences in component implementation.

#### CephFS

CephFS is a complete and independent system that prefers private cloud deployments; all data and metadata is persisted in Ceph's own storage pool (RADOS Pool).

- Metadata
  - Service Process (MDS): stateless and theoretically horizontally scalable. There are mature master-slave mechanisms, but multi-master deployments still have performance and stability concerns; production environments typically use one-master-multi-slaves or multi-master static isolation.
  - Persistent: independent RADOS storage pools, usually with SSD or higher performance hardware storage
- Data: One or more RADOS storage pools, with different configurations specified by Layout, such as chunk size (default 4 MiB), redundancy (multi-copy, EC), etc.
- Client: kernel client (kcephfs), user state client (ceph-fuse) and SDKs for C++, Python, etc. based on libcephfs; recently the community has also provided a Windows client (ceph-dokan). There is also a VFS object for Samba and a FSAL module for NFS-Ganesha to be considered in the ecosystem.

#### JuiceFS

JuiceFS mainly implements a libjfs library and FUSE client application, Java SDK, etc. It supports interfacing with various metadata engines and object storage and is suitable for deployment in public, private or hybrid cloud environments.

- Metadata: See [database implementation](../reference/how_to_setup_metadata_engine.md) for details, including:
  - Redis and various variants of the Redis-compatible protocol (trader
  - SQL family: MySQL, PostgreSQL, SQLite, etc.
  - Distributed K/V storage: TiKV is supported and Apple FoundationDB is planned to be supported.
  - Self-developed engine: JuiceFS fully managed service for use on the public cloud.
- Data: support for over 30 [object stores](../reference/how_to_setup_object_storage.md) on the public cloud and can also use with MinIO, Ceph RADOS, Ceph RGW, etc.
- Clients: Unix user state mount, Windows mount, Java SDK with full HDFS semantics compatibility, [Python SDK](https://github.com/megvii-research/juicefs-python) and a built-in S3 gateway.

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

CephFS splits files by [`object_size`](https://docs.ceph.com/en/latest/cephfs/file-layouts/#reading-layouts-with-getfattr) (default 4MiB), and each chunk corresponds to a RADOS object, while JuiceFS splits files by 64MiB chunks, and each chunk is further split into one or more logical slice(s) according to the actual situation when writing. Each Slice is further split into one or more logical Blocks when writing to the object store, and each Block corresponds to one object in the object store. When handling overwrite, CephFS needs to modify the corresponding objects directly, which is a complicated process; especially when the redundancy policy is EC or data compression is enabled, it often needs to read part of the object content first, modify it in memory, and then write it, which will bring a great performance overhead. JuiceFS writes the updated data as new objects and modifies the metadata when overwriting, which greatly improves the performance. Any redundant data that occurs during the process is garbage collected asynchronously.

#### [2] Data Compression

Strictly speaking, CephFS does not provide data compression itself, it actually relies on the RADOS layer BlueStore compression. JuiceFS, on the other hand, can compress data once before the Block is uploaded to the object store, in order to reduce the capacity used in the object storage. In other words, if you use JuiceFS to interface with RADOS, you can compress the Block once before and once after it enters RADOS. Also, as mentioned in **File Chunking**, CephFS does not normally enable BlueStore compression due to performance guarantees for overwrite writes.

#### [3] Data Encryption

Ceph **Messenger v2** supports data encryption at the network transport layer, while the storage layer is similar to compression, relying on the encryption provided at OSD creation.

JuiceFS performs encryption and decryption before uploading objects and after downloading, and is completely transparent on the object storage side.
