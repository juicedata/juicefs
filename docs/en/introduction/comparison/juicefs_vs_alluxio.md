---
slug: /comparison/juicefs_vs_alluxio
---

# JuiceFS vs. Alluxio

[Alluxio](https://www.alluxio.io) (/əˈlʌksio/) is a data access layer in the big data and machine learning ecosystem. Initially as research project "Tachyon", it was created at the University of California, Berkeley's [AMPLab](https://en.wikipedia.org/wiki/AMPLab) as creator's Ph.D. thesis in 2013. Alluxio was open sourced in 2014.

The following table shows the differences of main features between Alluxio and JuiceFS.

| Features                  | Alluxio            | JuiceFS            |
| --------                  | -------            | -------            |
| Storage format            | Object             | Block              |
| Cache granularity         | 64MiB              | 4MiB               |
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

### Storage format

A single file is [stored](../../reference/how_juicefs_store_files.md) in JuiceFS in three levels: chunk, slice and block. A file will be split into multiple blocks, and be compressed and encrypted (optional) before it is stored into object storage.

Alluxio stores files as _objects_ into UFS. It doesn't split files info blocks like what JuiceFS does.

### Cache granularity

The [default block size](../../reference/how_juicefs_store_files.md) of JuiceFS is 4MiB, and thus its granularity is smaller compared to 64MiB of Alluxio. Smaller block size is better for random read (e.g. Parquet and ORC) workload, i.e. cache management will be more efficiency.

### Hadoop-compatible

JuiceFS is [HDFS-compatible](../../deployment/hadoop_java_sdk.md). Not only compatible with Hadoop 2.x and Hadoop 3.x, but also compatible for various components in Hadoop ecosystem.

### Kubernetes CSI Driver

JuiceFS provides [Kubernetes CSI Driver](https://github.com/juicedata/juicefs-csi-driver) to help people who want to use JuiceFS in Kubernetes. Alluxio provides [Kubernetes CSI Driver](https://github.com/Alluxio/alluxio-csi) too, but this project seems not active and is not officially supported by Alluxio.

### Fully POSIX-compatible

JuiceFS is [fully POSIX-compatible](../../reference/posix_compatibility.md). A pjdfstest from [JD.com](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom) shows that Alluxio didn't pass the POSIX compatibility test, e.g. Alluxio doesn't support symbolic link, truncate, fallocate, append, xattr, mkfifo, mknod and utimes. Besides the things covered by pjdfstest, JuiceFS also provides close-to-open consistency, atomic metadata operation, mmap, fallocate with punch hole, xattr, BSD locks (flock) and POSIX record locks (fcntl).

### Atomic metadata operation

A metadata operation in Alluxio consists of two steps: the first step is to modify the state of Alluxio master, and the second step is to send request to UFS. Thus, we can see that the metadata operation isn't atomic. The state is unpredictable when the operation is being executed or any failures happen. Alluxio requires UFS to implement metadata operations, for example, rename file operation will become copy and delete operations.

Thanks to [Redis transaction](https://redis.io/topics/transactions), **most of the metadata operations of JuiceFS are atomic**, e.g. rename file, delete file, rename directory. You don't have to worry about the consistency and performance.

### Consistency

Alluxio loads metadata from the UFS as needed. It doesn't have information about UFS at startup. By default, Alluxio expects that all modifications on UFS are completed through Alluxio. If changes are made directly on UFS, you'll need to sync metadata between Alluxio and UFS either manually or periodically. As we have mentioned in ["Atomic metadata operation"](#atomic-metadata-operation) section, the two-step metadata operation may result in inconsistency.

JuiceFS provides strong consistency for both metadata and data. **The metadata service of JuiceFS is the single source of truth, not a mirror of UFS.** The metadata service doesn't rely on object storage to obtain metadata, and object storage is just treated as unlimited block storage. Thus, there will not be any inconsistency between JuiceFS and object storage.

### Data compression

JuiceFS supports to use [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data. Alluxio doesn't provide this feature.

### Data encryption

JuiceFS supports data encryption in transit and at rest. Alluxio community edition doesn't provide this feature, while [enterprise edition](https://docs.alluxio.io/ee/user/stable/en/operation/Security.html#end-to-end-data-encryption) does.

### Zero-effort operation

Alluxio's architecture can be divided into 3 components: master, worker and client. A typical cluster consists of a single leading master, standby masters, a job master, standby job masters, workers, and job workers. You need to maintain all these masters and workers by yourself.

JuiceFS uses Redis or [others](../../guide/how_to_set_up_metadata_engine.md) as the metadata engine. You could easily use the service managed by public cloud provider as JuiceFS's metadata engine without the need for operation.
