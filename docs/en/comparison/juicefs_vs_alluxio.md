# JuiceFS vs. Alluxio

[Alluxio](https://www.alluxio.io) (/əˈlʌksio/) is a data access layer in the big data and machine learning ecosystem. Initially as research project "Tachyon", it was created at the University of California, Berkeley's [AMPLab](https://en.wikipedia.org/wiki/AMPLab) as creator's Ph.D. thesis in 2013. Alluxio was open sourced in 2014.

The following table shows difference of main features between Alluxio and JuiceFS.

| Features                  | Alluxio            | JuiceFS |
| --------                  | -------            | ------- |
| Storage format            | Object             | Block   |
| Cache granularity         | 64MiB              | 4MiB    |
| Multi-tier cache          | ✓                  | ✓       |
| Hadoop-compatible         | ✓                  | ✓       |
| S3-compatible             | ✓                  | ✓       |
| Kubernetes CSI Driver     | ✓                  | ✓       |
| Hadoop data locality      | ✓                  | ✓       |
| Fully POSIX-compatible    | ✕                  | ✓       |
| Atomic metadata operation | ✕                  | ✓       |
| Consistency               | ✕                  | ✓       |
| Data compression          | ✕                  | ✓       |
| Data encryption           | ✕                  | ✓       |
| Zero-effort operation     | ✕                  | ✓       |
| Language                  | Java               | Go      |
| Open source license       | Apache License 2.0 | AGPLv3  |
| Open source date          | 2011               | 2021.1  |

### Storage format

The [storage format](how_juicefs_store_files.md) of one file in JuiceFS consists of three levels: chunk, slice and block. A file will be split into multiple blocks, and be compressed and encrypted (optional) store into object storage.

Alluxio stores file as object to UFS. The file doesn't be split info blocks like JuiceFS does.

### Cache granularity

The [default block size](how_juicefs_store_files.md) of JuiceFS is 4MiB, compare to 64MiB of Alluxio, the granularity is smaller. The smaller block size is better for random read (e.g. Parquet and ORC) workload, i.e. cache management will be more efficiency.

### Hadoop-compatible

JuiceFS is [HDFS-compatible](hadoop_java_sdk.md). Not only compatible with Hadoop 2.x and Hadoop 3.x, but also variety of components in Hadoop ecosystem.

### Kubernetes CSI Driver

JuiceFS provides [Kubernetes CSI Driver](https://github.com/juicedata/juicefs-csi-driver) to help people who want to use JuiceFS in Kubernetes. Alluxio provides [Kubernetes CSI Driver](https://github.com/Alluxio/alluxio-csi) too, but this project seems like not active maintained and not official supported by Alluxio.

### Fully POSIX-compatible

JuiceFS is [fully POSIX-compatible](posix_compatibility.md). One pjdfstest from [JD.com](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom) shows that Alluxio didn't pass the POSIX compatibility test, e.g. Alluxio doesn't support symbolic link, truncate, fallocate, append, xattr, mkfifo, mknod and utimes. Besides the things covered by pjdfstest, JuiceFS also provides close-to-open consistency, atomic metadata operation, mmap, fallocate with punch hole, xattr, BSD locks (flock) and POSIX record locks (fcntl).

### Atomic metadata operation

A metadata operation in Alluxio has two steps: the first step is modify state of Alluxio master, the second step is send request to UFS. As you can see, the metadata operation isn't atomic, its state is unpredictable when the operation is executing or any failure occurs. Alluxio relies on UFS to implement metadata operations, for example rename file operation will become copy and delete operations.

Thanks to [Redis transaction](https://redis.io/topics/transactions), **most of metadata operations of JuiceFS are atomic**, e.g. rename file, delete file, rename directory. You don't have to worry about the consistency and performance.

### Consistency

Alluxio loads metadata from the UFS as needed and it doesn't have information about UFS at startup. By default, Alluxio expects that all modifications to UFS occur through Alluxio. If changes are made to UFS directly, you need sync metadata between Alluxio and UFS either manually or periodically. As ["Atomic metadata operation"](#atomic-metadata-operation) section says, the two steps metadata operation may resulting in inconsistency.

JuiceFS provides strong consistency, both metadata and data. **The metadata service of JuiceFS is the single source of truth, not a mirror of UFS.** The metadata service doesn't rely on object storage to obtain metadata. Object storage just be treated as an unlimited block storage. There isn't any inconsistency between JuiceFS and object storage.

### Data compression

JuiceFS supports use [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data. Alluxio doesn't have this feature.

### Data encryption

JuiceFS supports data encryption in transit and at rest. Alluxio community edition doesn't have this feature, but [enterprise edition](https://docs.alluxio.io/ee/user/stable/en/operation/Security.html#end-to-end-data-encryption) has.

### Zero-effort operation

Alluxio's architecture can be divided into 3 components: master, worker and client. A typical cluster consists of a single leading master, standby masters, a job master, standby job masters, workers, and job workers. You need operation these masters and workers by yourself.

JuiceFS uses Redis or [others](databases_for_metadata.md) as the metadata engine. You could use service managed by public cloud provider easily as JuiceFS's metadata engine. There isn't any operation needed.
