# What is JuiceFS

[![license](https://img.shields.io/badge/license-AGPL%20V3-blue)](https://github.com/juicedata/juicefs/blob/main/LICENSE) [![Go Report](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/badge/github.com/juicedata/juicefs) [![Join Slack](https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack)](https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-0bJojPaql8cfFgwerDQJgA)

![JuiceFS LOGO](../images/juicefs-logo.png)

JuiceFS is a high-performance [POSIX](https://en.wikipedia.org/wiki/POSIX) file system released under GNU Affero General Public License v3.0. It is specially optimized for the cloud-native environment. Using the JuiceFS file system to store data, the data itself will be persisted in object storage (e.g. AWS S3), and the metadata corresponding to the data will be persisted in high-performance databases such as Redis. 

JuiceFS can simply and conveniently connect massive cloud storage directly to big data, machine learning, artificial intelligence, and various application platforms that have been put into production environment, without modifying the code, you can use massive cloud storage as efficiently as using local storage. 



## Highlighted Features

1. **Fully POSIX-compatible**: Use like a local file system, seamlessly docking with existing applications, no business intrusion.
2. **Fully Hadoop-compatible**: JuiceFS [Hadoop Java SDK](hadoop_java_sdk.md) is compatible with Hadoop 2.x and Hadoop 3.x. As well as variety of components in Hadoop ecosystem.
3. **S3-compatible**:  JuiceFS [S3 Gateway](s3_gateway.md) provides S3-compatible interface.
4. **Cloud Native**: JuiceFS provides [Kubernetes CSI driver](how_to_use_on_kubernetes.md) to help people who want to use JuiceFS in Kubernetes.
5. **Sharing**: JuiceFS is a shared file storage that can be read and written by thousands clients.
6. **Strong Consistency**: The confirmed modification will be immediately visible on all servers mounted with the same file system .
7. **Outstanding Performance**: The latency can be as low as a few milliseconds and the throughput can be expanded to nearly unlimited. 
8. **Data Encryption**: Supports data encryption in transit and at rest, read [the guide](encrypt.md) for more information.
9. **Global File Locks**: JuiceFS supports both BSD locks (flock) and POSIX record locks (fcntl).
10. **Data Compression**: JuiceFS supports use [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data.

## Architecture

JuiceFS file system consists of three parts: 

1. **JuiceFS Client**: Coordinate the implementation of object storage and metadata storage engines, as well as file system interfaces such as POSIX, Hadoop, Kubernetes, and S3 gateway.
2. **Data Storage**: Store the data itself, support local disk and object storage.
3. **Metadata Engine**: Store the metadata corresponding to the data, support multiple engines such as Redis.

![](../images/juicefs-arch-new.png)

As a file system, JuiceFS will process data and its corresponding metadata separately, the data will be stored in the object storage, and the metadata will be stored in the metadata engine.

In terms of **data storage**, JuiceFS supports almost all public cloud object storage services, as well as privatized object storage such as OpenStack Swift, Ceph, and MinIO.

In terms of **metadata storage**, JuiceFS adopts a multi-engine design, and currently supports [Redis](https://redis.io/) as a metadata engine, and it will also implement TiKV, PostgreSQL, MariaDB, MySQL , Oracle and more engines.

In terms of the implementation of **file system interface**:

- With **FUSE**, the JuiceFS file system can be mounted to the server in a POSIX compatible manner, and the massive cloud storage can be used directly as local storage.
- With **Hadoop Java SDK**, the JuiceFS file system can directly replace HDFS, providing Hadoop with low-cost mass storage.
- With **Kubernetes CSI driver**, the JuiceFS file system can directly provide mass storage for Kubernetes.
- Through **S3 Gateway**, applications that use S3 as the storage layer can be directly accessed, and tools such as AWS CLI, s3cmd, and MinIO client can be used to access the JuiceFS file system.

## How JuiceFS stores files

The `file system` acts as a medium for interaction between the user and the hard drive, which allows files to be stored on the hard drive properly. As you know, Windows commonly used file systems are FAT32, NTFS, Linux commonly used file systems are Ext4, XFS, BTRFS, etc., each file system has its own unique way of organizing and managing files, which determines the file system Features such as storage capacity and performance.

As a file system, JuiceFS is no exception. Its strong consistency and high performance are inseparable from its unique file management mode.

Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS will format the data and store it in object storage (cloud storage), and store the metadata corresponding to the data in databases such as Redis. .

Any file stored in JuiceFS will be split into fixed-size **"Chunk"**, and the default upper limit is 64 MiB. Each Chunk is composed of one or more **"Slice"**. The length of the slice is not fixed, depending on the way the file is written. Each slice will be further split into fixed-size **"Block"**, which is 4 MiB by default. Finally, these blocks will be stored in the object storage. At the same time, JuiceFS will store each file and its Chunks, Slices, Blocks and other metadata information in metadata engines.

![JuiceFS storage format](../images/juicefs-storage-format-new.png)

Using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you will find that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform. There is a chunks directory and a bunch of digitally numbered directories and files in the bucket. Don't panic, this is the secret of the high-performance operation of the JuiceFS file system!

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

## POSIX Compatibility 

JuiceFS passed all of the 8813 tests in latest [pjdfstest](https://github.com/pjd/pjdfstest).

```
All tests successful.

Test Summary Report
-------------------
/root/soft/pjdfstest/tests/chown/00.t          (Wstat: 0 Tests: 1323 Failed: 0)
  TODO passed:   693, 697, 708-709, 714-715, 729, 733
Files=235, Tests=8813, 233 wallclock secs ( 2.77 usr  0.38 sys +  2.57 cusr  3.93 csys =  9.65 CPU)
Result: PASS
```

Besides the things covered by pjdfstest, JuiceFS provides:

- Close-to-open consistency. Once a file is closed, the following open and read can see the data written before close. Within same mount point, read can see all data written before it.
- Rename and all other metadata operations are atomic guaranteed by Redis transaction.
- Open files remain accessible after unlink from same mount point.
- Mmap is supported (tested with FSx).
- Fallocate with punch hole support.
- Extended attributes (xattr).
- BSD locks (flock).
- POSIX record locks (fcntl).

## Performance Comparison

### Throughput

Performed a sequential read/write benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [fio](https://github.com/axboe/fio), here is the result:

[![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)](../images/sequential-read-write-benchmark.svg)

It shows JuiceFS can provide 10X more throughput than the other two, read [more details](fio.md).

### Metadata IOPS

Performed a simple mdtest benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [mdtest](https://github.com/hpc/ior), here is the result:

[![Metadata Benchmark](../images/metadata-benchmark.svg)](../images/metadata-benchmark.svg)

It shows JuiceFS can provide significantly more metadata IOPS than the other two, read [more details](../en/mdtest.md).

## Quick Start

Now, you can refer to the [Quick Start Guide](https://github.com/juicedata/juicefs/blob/simple-docs/docs/zh_cn/quick_start_guide.md) to start using JuiceFS immediately! 😊