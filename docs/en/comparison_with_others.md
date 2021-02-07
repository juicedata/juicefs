# Comparison with Others

## Alluxio

***Note: Part of following paragraphs are extracted from Alluxio official documentation. It may outdated, subject to latest version of the official documentation.***

[Alluxio](https://www.alluxio.io) (/əˈlʌksio/) is a data access layer in the big data and machine learning ecosystem. Initially as research project "Tachyon", it was created at the University of California, Berkeley's [AMPLab](https://en.wikipedia.org/wiki/AMPLab) as creator's Ph.D. thesis in 2013. Alluxio was open sourced in 2014.

Alluxio's architecture can be divided into 3 components: master, worker and client.

Master is responsible for managing the global metadata of the system. This includes file system metadata (e.g. the file system inode tree), block metadata (e.g. block locations), and worker capacity metadata (free and used space). Master can be divided into 4 types: leading master, standby master, secondary master and job master. Each type of master plays different role.

Worker is responsible for managing user-configurable local resources allocated to Alluxio (e.g. memory, SSDs, HDDs). Worker stores data as blocks and serve client requests that read or write data by reading or creating new blocks within their local resources. The default block size is 64MiB, i.e. local cache granularity is 64MiB.

Client provides users a gateway to interact with the Alluxio servers. It initiates communication with the leading master to carry out metadata operations and with workers to read and write data that is stored in Alluxio. Client supports APIs in multiple ways including: Java, Go, Python, RESTful, HDFS, S3 and POSIX.

One key concept of Alluxio is UFS (Under File Storage). UFS is represented as space which is not managed by Alluxio. It may come from an external file system, e.g. HDFS, S3. Alluxio acting as a cache layer in one or more UFSs. It loads metadata from the UFS as needed and it doesn't have information about UFS at startup. By default, Alluxio expects that all modifications to UFS occur through Alluxio. If changes are made to UFS directly, you need sync metadata between Alluxio and UFS either manually or periodically.

**In contrast, JuiceFS is a distributed file system instead of data access layer.** JuiceFS's [architecture](../../README.md#architecture) can be divided into 2 components: metadata service and client. Similarly, metadata service is responsible for managing metadata of the entire file system. **But it's the single source of truth, not a mirror of UFS.** The metadata service doesn't rely on object storage to obtain metadata. Object storage just be treated as an unlimited block storage. There isn't any inconsistency between JuiceFS and object storage. JuiceFS supports almost every object storage, see [the list](how_to_setup_object_storage.md#supported-object-storage) for more information.

The JuiceFS client is responsible for reading and writing data. Client also supports APIs in multiple ways including: POSIX, HDFS and S3. JuiceFS is [fully POSIX-compatible](../../README.md#posix-compatibility). One pjdfstest from [JD.com](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom) shows that Alluxio didn't pass the POSIX compatibility test, e.g. Alluxio doesn't support symbolic link, truncate, fallocate, append, xattr, mkfifo, mknod and utimes. Besides the things covered by pjdfstest, JuiceFS also provides close-to-open consistency, atomic metadata operation, mmap, fallocate with punch hole, xattr, BSD locks (flock) and POSIX record locks (fcntl).

JuiceFS is also [Hadoop-compatible](hadoop_java_sdk.md). Not only compatible with Hadoop 2.x and Hadoop 3.x, but also variety of components in Hadoop ecosystem.

The [default block size](../../README.md#architecture) of JuiceFS is 4MiB, compare to 64MiB of Alluxio, the granularity is smaller. The smaller block size is more cache friendly, i.e. cache management will be more efficiency.

JuiceFS also provides [Kubernetes CSI driver](https://github.com/juicedata/juicefs-csi-driver) to help people who want to use JuiceFS in Kubernetes. Alluxio provides [K8s CSI driver](https://github.com/Alluxio/alluxio-csi) too, but this project seems like not active maintained and not official supported by Alluxio.

By default JuiceFS uses [LZ4](https://lz4.github.io/lz4) to compress all your data. And will support encryption in the future. Alluxio doesn't have these features.

In the last, JuiceFS is written in Go and Alluxio is written in Java.
