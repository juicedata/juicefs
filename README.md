<p align="center"><a href="https://github.com/juicedata/juicefs"><img alt="JuiceFS Logo" src="docs/en/images/juicefs-logo.png" width="50%" /></a></p>
<p align="center">
    <a href="https://travis-ci.com/juicedata/juicefs"><img alt="Build Status" src="https://travis-ci.com/juicedata/juicefs.svg?token=jKSPwswpc2ph4uMtwpHa&branch=main" /></a>
    <a href="https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-YD7e0JxWdesSEa9vY_f_DA"><img alt="Join Slack" src="https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack" /></a>
    <a href="https://goreportcard.com/report/github.com/juicedata/juicefs"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/juicedata/juicefs" /></a>
    <a href="https://juicefs.com/docs/community/introduction/"><img alt="English doc" src="https://img.shields.io/badge/docs-Doc%20Center-brightgreen" /></a>
    <a href="README_CN.md"><img alt="中文手册" src="https://img.shields.io/badge/docs-%E4%B8%AD%E6%96%87%E6%89%8B%E5%86%8C-brightgreen" /></a>
</p>

**JuiceFS** is a high-performance [POSIX](https://en.wikipedia.org/wiki/POSIX) file system released under Apache License 2.0, particularly designed for the cloud-native environment. The data, stored via JuiceFS, will be persisted in object storage (e.g. Amazon S3), and the corresponding metadata can be persisted in various database engines such as Redis, MySQL, and SQLite based on the scenarios and requirements.

With JuiceFS, massive cloud storage can be directly connected to big data, machine learning, artificial intelligence, and various application platforms in production environments. Without modifying code, the massive cloud storage can be used as efficiently as local storage. 

📺 **Video**: [What is JuiceFS?](https://www.youtube.com/watch?v=8RdZoBG-D6Y)

📖 **Document**: [Quick start guide](https://juicefs.com/docs/community/quick_start_guide)

## Highlighted Features

1. **Fully POSIX-compatible**: Use as a local file system, seamlessly docking with existing applications without breaking business workflow.
2. **Fully Hadoop-compatible**: JuiceFS' [Hadoop Java SDK](docs/en/deployment/hadoop_java_sdk.md) is compatible with Hadoop 2.x and Hadoop 3.x as well as a variety of components in the Hadoop ecosystems.
3. **S3-compatible**:  JuiceFS' [S3 Gateway](docs/en/deployment/s3_gateway.md) provides an S3-compatible interface.
4. **Cloud Native**: A [Kubernetes CSI driver](docs/en/deployment/how_to_use_on_kubernetes.md) is provided for easily using JuiceFS in Kubernetes.
5. **Shareable**: JuiceFS is a shared file storage that can be read and written by thousands of clients.
6. **Strong Consistency**: The confirmed modification will be immediately visible on all the servers mounted with the same file system.
7. **Outstanding Performance**: The latency can be as low as a few milliseconds, and the throughput can be expanded nearly unlimitedly (depending on the size of the object storage). [Test results](docs/en/benchmark/benchmark.md)
8. **Data Encryption**: Supports data encryption in transit and at rest (please refer to [the guide](docs/en/security/encrypt.md) for more information).
9. **Global File Locks**: JuiceFS supports both BSD locks (flock) and POSIX record locks (fcntl).
10. **Data Compression**: JuiceFS supports [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data.

---

[Architecture](#architecture) | [Getting Started](#getting-started) | [Advanced Topics](#advanced-topics) | [POSIX Compatibility](#posix-compatibility) | [Performance Benchmark](#performance-benchmark) | [Supported Object Storage](#supported-object-storage) | [Who is using](#who-is-using) | [Roadmap](#roadmap) | [Reporting Issues](#reporting-issues) | [Contributing](#contributing) | [Community](#community) | [Usage Tracking](#usage-tracking) | [License](#license) | [Credits](#credits) | [FAQ](#faq)

---

## Architecture

JuiceFS consists of three parts:

1. **JuiceFS Client**: Coordinates object storage and metadata storage engine as well as implementation of file system interfaces such as POSIX, Hadoop, Kubernetes, and S3 gateway.
2. **Data Storage**: Stores data, with supports of a variety of data storage media, e.g., local disk, public or private cloud object storage, and HDFS.
3. **Metadata Engine**: Stores the corresponding metadata that contains information of file name, file size, permission group, creation and modification time and directory structure, etc., with supports of different metadata engines, e.g., Redis, MySQL, SQLite and TiKV.

![JuiceFS Architecture](docs/en/images/juicefs-arch-new.png)

JuiceFS can store the metadata of file system on Redis, which is a fast, open-source, in-memory key-value data storage, particularly suitable for storing metadata; meanwhile, all the data will be stored in object storage through JuiceFS client. [Learn more](docs/en/introduction/architecture.md)

![JuiceFS Storage Format](docs/en/images/juicefs-storage-format-new.png)

Each file stored in JuiceFS is split into **"Chunk"** s at a fixed size with the default upper limit of 64 MiB. Each Chunk is composed of one or more **"Slice"**(s), and the length of the slice varies depending on how the file is written. Each slice is composed of size-fixed **"Block"** s, which are 4 MiB by default. These blocks will be stored in object storage in the end; at the same time, the metadata information of the file and its Chunks, Slices, and Blocks will be stored in metadata engines via JuiceFS. [Learn more](docs/en/reference/how_juicefs_store_files.md)

![How JuiceFS stores your files](docs/en/images/how-juicefs-stores-files-new.png)

When using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, the source files stored in JuiceFS cannot be found in the file browser of the object storage platform; instead, there are only a chunks directory and a bunch of digitally numbered directories and files in the bucket. Don't panic! This is just the secret of the high-performance operation of JuiceFS!

## Getting Started

Before you begin, make sure you have:

1. Redis database for metadata storage
2. Object storage for storing data blocks
3. [JuiceFS Client](https://juicefs.com/docs/community/installation) downloaded and installed

Please refer to [Quick Start Guide](https://juicefs.com/docs/community/quick_start_guide) in the community doc (or doc in [this repo](docs/en/getting-started/for_local.md)) to start using JuiceFS right away!

### Command Reference

Check out all the command line options in [command reference](docs/en/reference/command_reference.md).

### Kubernetes

It is also very easy to use JuiceFS on Kubernetes. Please find more information [here](docs/en/deployment/how_to_use_on_kubernetes.md).

### Hadoop Java SDK

If you wanna use JuiceFS in Hadoop, check [Hadoop Java SDK](docs/en/deployment/hadoop_java_sdk.md).

## Advanced Topics

- [Redis Best Practices](docs/en/administration/metadata/redis_best_practices.md)
- [How to Setup Object Storage](docs/en/reference/how_to_setup_object_storage.md)
- [Cache Management](docs/en/administration/cache_management.md)
- [Fault Diagnosis and Analysis](docs/en/administration/fault_diagnosis_and_analysis.md)
- [FUSE Mount Options](docs/en/reference/fuse_mount_options.md)
- [Using JuiceFS on Windows](docs/en/juicefs_on_windows.md)
- [S3 Gateway](docs/en/deployment/s3_gateway.md)

Please refer to [JuiceFS User Manual](docs/en/README.md) for more information.

## POSIX Compatibility

JuiceFS has passed all of the compatibility tests (8813 in total) in the latest [pjdfstest](https://github.com/pjd/pjdfstest) .

```
All tests successful.

Test Summary Report
-------------------
/root/soft/pjdfstest/tests/chown/00.t          (Wstat: 0 Tests: 1323 Failed: 0)
  TODO passed:   693, 697, 708-709, 714-715, 729, 733
Files=235, Tests=8813, 233 wallclock secs ( 2.77 usr  0.38 sys +  2.57 cusr  3.93 csys =  9.65 CPU)
Result: PASS
```

Aside from the POSIX features covered by pjdfstest, JuiceFS also provides:

- Close-to-open consistency. Once a file is written and closed, it is guaranteed to view the written data in the following open and read. Within the same mount point, all the written data can be read immediately.
- Rename and all other metadata operations are atomic, which are guaranteed by Redis transaction.
- Opened files remain accessible after unlink from same mount point.
- Mmap (tested with FSx).
- Fallocate with punch hole support.
- Extended attributes (xattr).
- BSD locks (flock).
- POSIX record locks (fcntl).

## Performance Benchmark

### Basic benchmark

JuiceFS provides a subcommand that can run a few basic benchmarks to help you understand how it works in your environment:

![JuiceFS Bench](docs/en/images/juicefs-bench.png)

### Throughput

A sequential read/write benchmark has also been performed on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [fio](https://github.com/axboe/fio). 

![Sequential Read Write Benchmark](docs/en/images/sequential-read-write-benchmark.svg)

Above result figure shows that JuiceFS can provide 10X more throughput than the other two (see [more details](docs/en/benchmark/fio.md)).

### Metadata IOPS

A simple mdtest benchmark has been performed on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [mdtest](https://github.com/hpc/ior).

![Metadata Benchmark](docs/en/images/metadata-benchmark.svg)

The result shows that JuiceFS can provide significantly more metadata IOPS than the other two (see [more details](docs/en/benchmark/mdtest.md)).

### Analyze performance

There is a virtual file called `.accesslog` in the root of JuiceFS to show all the details of file system operations and the time they take, for example:

```bash
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

The last number on each line is the time (in seconds) that the current operation takes. You can directly use this to debug and analyze performance issues, or try `./juicefs profile /jfs` to monitor real time statistics. Please run `./juicefs profile -h` or refer to [here](docs/en/benchmark/operations_profiling.md) to learn more about this subcommand.

## Supported Object Storage

- Amazon S3
- Google Cloud Storage
- Azure Blob Storage
- Alibaba Cloud Object Storage Service (OSS)
- Tencent Cloud Object Storage (COS)
- QingStor Object Storage
- Ceph RGW
- MinIO
- Local disk
- Redis

JuiceFS supports almost all object storage services. [Learn more](docs/en/reference/how_to_setup_object_storage.md#supported-object-storage).

## Who is using

JuiceFS is still in beta quality, and the core storage format is not stabilized yet. Thus, please do a careful and thorough evaluation before using JuiceFS in a production environment. If you are interested, feel free to do tests and give us [feedback](https://github.com/juicedata/juicefs/discussions).

You are also welcome to share your experience of using JuiceFS with us and others. Additionally, we have collected a summary list in [ADOPTERS.md](ADOPTERS.md), which includes other open source projects used with JuiceFS.

## Roadmap

- Stabilize storage format
- Support FoundationDB as metadata engine
- User and group quotas 
- Directory quotas
- Snapshot
- Write once read many (WORM)

## Reporting Issues

We use [GitHub Issues](https://github.com/juicedata/juicefs/issues) to track community reported issues. You can also [contact](#community) the community for any questions.

## Contributing

Thank you for your contribution! Please refer to the [CONTRIBUTING.md](CONTRIBUTING.md) for more information.

## Community

Welcome to join the [Discussions](https://github.com/juicedata/juicefs/discussions) and the [Slack channel](https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-YD7e0JxWdesSEa9vY_f_DA) to connect with JuiceFS team members and other users.

## Usage Tracking

JuiceFS collects **anonymous** usage data by default to help us better understand how the community is using JuiceFS. Only core metrics (e.g. version number) will be reported, and user data and any other sensitive data will not be included. The related code can be viwed [here](pkg/usage/usage.go). 

You could also disable reporting easily by command line option `--no-usage-report`:

```bash
$ ./juicefs mount --no-usage-report
```

## License

JuiceFS is open-sourced under Apache License 2.0, see [LICENSE](LICENSE).

## Credits

The design of JuiceFS was inspired by [Google File System](https://research.google/pubs/pub51), [HDFS](https://hadoop.apache.org) and [MooseFS](https://moosefs.com). Thanks for their great work!

## FAQ

### Why doesn't JuiceFS support XXX object storage?

JuiceFS supports many object storage. Please check out [this list](docs/en/reference/how_to_setup_object_storage.md#supported-object-storage) first. If the object storage you want to use is compatible with S3, you could treat it as S3. Otherwise, try reporting issue.

### Can I use Redis cluster?

The simple answer is no. JuiceFS uses [Redis transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, which is not well supported by cluster mode. For this, sentinal or other Redis HA solution are needed.

See ["Redis Best Practices"](docs/en/administration/metadata/redis_best_practices.md) for more information.

### What's the difference between JuiceFS and XXX?

See ["Comparison with Others"](docs/en/comparison) for more information.

For more FAQs, please see the [full list](docs/en/faq.md).



## Stargazers over time

[![Stargazers over time](https://starchart.cc/juicedata/juicefs.svg)](https://starchart.cc/juicedata/juicefs)
