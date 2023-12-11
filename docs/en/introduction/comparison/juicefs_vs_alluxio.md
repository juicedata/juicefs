---
slug: /comparison/juicefs_vs_alluxio
description: This article compares the main features of Alluxio and JuiceFS.
---

# JuiceFS vs. Alluxio

Alluxio (/əˈlʌksio/) is a data access layer in the big data and machine learning ecosystem. Initially as the research project "Tachyon," it was created at the University of California, Berkeley's [AMPLab](https://en.wikipedia.org/wiki/AMPLab) as creator's Ph.D. thesis in 2013. Alluxio was open sourced in 2014.

The following table compares the main features of Alluxio and JuiceFS.

| Features                  | Alluxio            | JuiceFS            |
| --------                  | -------            | -------            |
| Storage format            | Object             | Block              |
| Cache granularity         | 64 MiB             | 4 MiB               |
| Multi-tier cache          | ✓                  | ✓                  |
| Hadoop-compatible         | ✓                  | ✓                  |
| S3-compatible             | ✓                  | ✓                  |
| Kubernetes CSI Driver     | ✓                  | ✓                  |
| Hadoop data locality      | ✓                  | ✓                  |
| Fully POSIX-compatible    | ✕                  | ✓                  |
| Atomic metadata operation | ✕                  | ✓                  |
| Consistency               | ✕                  | ✓                  |
| Data compression          | ✕                  | ✓                  |
| Data encryption           | ✕                  | ✓                  |
| Zero-effort operation     | ✕                  | ✓                  |
| Language                  | Java               | Go                 |
| Open source license       | Apache License 2.0 | Apache License 2.0 |
| Open source date          | 2014               | 2021.1             |

## Storage format

JuiceFS has its own storage format, where files are divided into blocks, and they can be optionally encrypted and compressed before being uploaded to the object storage. For more details, see [How JuiceFS stores files](../architecture.md#how-juicefs-store-files).

In contrast, Alluxio stores files as _objects_ into UFS and does not split them into blocks like JuiceFS does.

## Cache granularity

JuiceFS has a smaller [default block size](../architecture.md#how-juicefs-store-files) of 4 MiB, which results in a finer granularity compared to Alluxio's 64 MiB. The smaller block size of JuiceFS is more beneficial for workloads involving random reads (e.g., Parquet and ORC), as it improves cache management efficiency.

## Hadoop-compatible

JuiceFS is [HDFS-compatible](../../deployment/hadoop_java_sdk.md), supporting not only Hadoop 2.x and Hadoop 3.x, but also various components in the Hadoop ecosystem.

## Kubernetes CSI Driver

JuiceFS provides [Kubernetes CSI Driver](https://github.com/juicedata/juicefs-csi-driver) for easy integration with Kubernetes environments. While Alluxio also offers [Kubernetes CSI Driver](https://github.com/Alluxio/alluxio-csi), it seems to have limited activity and lacks official support from Alluxio.

## Fully POSIX-compatible

JuiceFS is [fully POSIX-compatible](../../reference/posix_compatibility.md). A pjdfstest from [JD.com](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom) shows that Alluxio did not pass the POSIX compatibility test. For example, Alluxio does not support symbolic links, truncate, fallocate, append, xattr, mkfifo, mknod and utimes. Besides the things covered by pjdfstest, JuiceFS also provides close-to-open consistency, atomic metadata operations, mmap, fallocate with punch hole, xattr, BSD locks (flock), and POSIX record locks (fcntl).

## Atomic metadata operation

In Alluxio, a metadata operation involves two steps: modifying the state of the Alluxio master and sending a request to the UFS. This process is not atomic, and the state is unpredictable during execution or in case of failures. Additionally, Alluxio relies on UFS to implement metadata operations. For example, rename file operations will become copy and delete operations.

Thanks to [Redis transactions](https://redis.io/topics/transactions), **most metadata operations in JuiceFS are atomic**, for example, file renaming, file deletion, and directory renaming. You do not have to worry about the consistency and performance.

## Consistency

Alluxio loads metadata from the UFS as needed. It lacks information about UFS at startup. By default, Alluxio expects all modifications on UFS to be completed through Alluxio. If changes are made directly on UFS, you need to sync metadata between Alluxio and UFS either manually or periodically. As we have mentioned in [Atomic metadata operation](#atomic-metadata-operation) section, the two-step metadata operation may result in inconsistency.

JuiceFS provides strong consistency for both metadata and data. **The metadata service of JuiceFS is the single source of truth, not a mirror of UFS.** The metadata service does not rely on object storage to obtain metadata, and object storage is just treated as unlimited block storage. This ensures there are no inconsistencies between JuiceFS and object storage.

## Data compression

JuiceFS supports data compression using [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) for all your data, while Alluxio does not offer this feature.

## Data encryption

JuiceFS supports data encryption both in transit and at rest. Alluxio community edition lacks this feature, while it is available in the [enterprise edition](https://docs.alluxio.io/ee/user/stable/en/operation/Security.html#end-to-end-data-encryption).

## Zero-effort operations

Alluxio's architecture can be divided into three components: master, worker and client. A typical cluster consists of a single leading master, standby masters, a job master, standby job masters, workers, and job workers. You need to maintain all these masters and workers by yourself.

JuiceFS uses Redis or [other databases](../../reference/how_to_set_up_metadata_engine.md) as the metadata engine. You could easily use the service managed by a public cloud provider as JuiceFS' metadata engine, without any operational overhead.
