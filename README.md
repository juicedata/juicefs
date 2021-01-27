<p align="center"><a href="https://github.com/juicedata/juicefs"><img alt="JuiceFS Logo" src="https://github.com/juicedata/juicefs/raw/main/docs/images/juicefs-logo.png" width="50%" /></a></p>
<p align="center">
    <a href="https://travis-ci.com/juicedata/juicefs"><img alt="Build Status" src="https://travis-ci.com/juicedata/juicefs.svg?token=jKSPwswpc2ph4uMtwpHa&branch=main" /></a>
    <a href="https://join.slack.com/t/juicefs/shared_invite/zt-kjbre7de-K8jeTMouDZE8nKEZVHLAMQ"><img alt="Join Slack" src="https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack" /></a>
    <a href="https://goreportcard.com/report/github.com/juicedata/juicefs"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/juicedata/juicefs?dummy=unused" /></a>
    <a href="README_CN.md"><img alt="Chinese Docs" src="https://img.shields.io/badge/docs-%E4%B8%AD%E6%96%87-informational" /></a>
</p>

**JuiceFS** is an open-source POSIX file system built on top of [Redis](https://redis.io) and object storage (e.g. Amazon S3), designed and optimized for cloud native environment. By using the widely adopted Redis and S3 as the persistent storage, JuiceFS serves as a stateless middleware to enable many applications to share data easily.

The highlighted features are:

- **Fully POSIX-compatible**: JuiceFS is a fully POSIX-compatible file system. Existing applications can work with it without any change. See [pjdfstest result](#posix-compatibility) below.
- **Outstanding Performance**: The latency can be as low as a few milliseconds and the throughput can be expanded to nearly unlimited. See [benchmark result](#performance-benchmark) below.
- **Cloud Native**: By utilizing cloud object storage, you can scale storage and compute independently, a.k.a. disaggregated storage and compute architecture.
- **Sharing**: JuiceFS is a shared file storage that can be read and written by many clients.
- **Global File Locks**: JuiceFS supports both BSD locks (flock) and POSIX record locks (fcntl).
- **Data Compression**: By default JuiceFS uses [LZ4](https://lz4.github.io/lz4) to compress all your data, you could also use [Zstandard](https://facebook.github.io/zstd) instead.

---

[Architecture](#architecture) | [Getting Started](#getting-started) | [POSIX Compatibility](#posix-compatibility) | [Performance Benchmark](#performance-benchmark) | [Supported Object Storage](#supported-object-storage) | [Status](#status) | [Roadmap](#roadmap) | [Reporting Issues](#reporting-issues) | [Contributing](#contributing) | [Community](#community) | [Usage Tracking](#usage-tracking) | [License](#license) | [Credits](#credits) | [FAQ](#faq)

---

## Architecture

![JuiceFS Architecture](docs/images/juicefs-arch.png)

JuiceFS relies on Redis to store file system metadata. Redis is a fast, open-source, in-memory key-value data store and very suitable for storing the metadata. All the data will store into object storage through JuiceFS client.

![JuiceFS Storage Format](docs/images/juicefs-storage-format.png)

The storage format of one file in JuiceFS consists of three levels. The first level called **"Chunk"**. Each chunk has fixed size, currently it is 64MiB and cannot be changed. The second level called **"Slice"**. The slice size is variable. A chunk may have multiple slices. The third level called **"Block"**. Like chunk, its size is fixed. By default one block is 4MiB and you could modify it when formatting a volume (see following section). At last, the block will be compressed and encrypted (optional) store into object storage.

## Getting Started

### Precompiled binaries

You can download precompiled binaries from [releases page](https://github.com/juicedata/juicefs/releases).

### Building from source

You need install [Go](https://golang.org) first, then run following commands:

```bash
$ git clone https://github.com/juicedata/juicefs.git
$ cd juicefs
$ make
```

### Dependency

A Redis server (>= 2.2) is needed for metadata, please follow [Redis Quick Start](https://redis.io/topics/quickstart).

[macFUSE](https://osxfuse.github.io/) is also needed for macOS.

The last one you need is object storage. There are many options for object storage, local disk is the easiest one to get started.

### Format a volume

Assume you have a Redis server running locally, we can create a volume called `test` using it to store metadata:

```bash
$ ./juicefs format localhost test
```

It will create a volume with default settings. If there Redis server is not running locally, the address could be specified using URL, for example, `redis://user:password@host:6379/1`, the password can also be specified by environment variable `REDIS_PASSWORD` to hide it from command line options.

As JuiceFS relies on object storage to store data, you can specify a object storage using `--storage`, `--bucket`, `--access-key` and `--secret-key`. By default, it uses a local directory to serve as an object store, for all the options, please see `./juicefs format -h`.

To use MinIO as object store, it could be specified as:

```bash
$ ./juicefs format --storage minio \
   --bucket http://1.2.3.4:9000/mybucket \
   --access-key XXX \
   --secret-key XXX \
   localhost test
```

### Mount a volume

Once a volume is formatted, you can mount it to a directory, which is called *mount point*.

```bash
$ ./juicefs mount -d localhost ~/jfs
```

After that you can access the volume just like a local directory.

To get all options, just run `./juicefs mount -h`.

### Command Reference

There is a [command reference](docs/command_reference.md) to see all options of the subcommand.

### Kubernetes

There is a [Kubernetes CSI driver](https://github.com/juicedata/juicefs-csi-driver) to use JuiceFS in Kubernetes easily.

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

## Performance Benchmark

### Throughput

Performed a sequential read/write benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [fio](https://github.com/axboe/fio), here is the result:

![Sequential Read Write Benchmark](docs/images/sequential-read-write-benchmark.svg)

It shows JuiceFS can provide 10X more throughput than the other two, read [more details](docs/fio.md).

### Metadata IOPS

Performed a simple mdtest benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [mdtest](https://github.com/hpc/ior), here is the result:

![Metadata Benchmark](docs/images/metadata-benchmark.svg)

It shows JuiceFS can provide significantly more metadata IOPS than the other two, read [more details](docs/mdtest.md).

### Analyze performance

There is a virtual file called `.accesslog` in the root of JuiceFS to show all the operations and the time they takes, for example:

```bash
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

The last number on each line is the time (in seconds) current operation takes. We can use this to debug and analyze performance issues. We will provide more tools to analyze it.

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

For the detailed list, see [juicesync](https://github.com/juicedata/juicesync).

## Status

It's considered as beta quality, the storage format is not stabilized yet. It's not recommended to deploy it into production environment. Please test it with your use cases and give us feedback.

## Roadmap

- Stabilize storage format
- S3 gateway
- Windows client
- Encryption at rest
- Other databases for metadata

## Reporting Issues

We use [GitHub Issues](https://github.com/juicedata/juicefs/issues) to track community reported issues. You can also [contact](#community) the community for getting answers.

## Contributing

Thank you for your contribution! Please refer to the [CONTRIBUTING.md](CONTRIBUTING.md) for more information.

## Community

Welcome to join the [Discussion](https://github.com/juicedata/juicefs/discussions) and the [Slack channel](https://join.slack.com/t/juicefs/shared_invite/zt-kjbre7de-K8jeTMouDZE8nKEZVHLAMQ) to connect with JuiceFS team members and other users.

## Usage Tracking

JuiceFS by default collects **anonymous** usage data. It only collects core metrics (e.g. version number), no user or any sensitive data will be collected. You could review related code [here](cmd/usage.go).

These data help us understand how the community is using this project. You could disable reporting easily by command line option `--no-usage-report`:

```bash
$ ./juicefs mount --no-usage-report
```

## License

JuiceFS is open-sourced under GNU AGPL v3.0, see [LICENSE](LICENSE).

## Credits

The design of JuiceFS was inspired by [Google File System](https://research.google/pubs/pub51), [HDFS](https://hadoop.apache.org) and [MooseFS](https://moosefs.com), thanks to their great work.

## FAQ

### Why doesn't JuiceFS support XXX object storage?

JuiceFS already supported many object storage, please check [the list](#supported-object-storage) first. If this object storage is compatible with S3, you could treat it as S3. Otherwise, try reporting issue to [juicesync](https://github.com/juicedata/juicesync).

### Can I use Redis cluster?

The simple answer is no. JuiceFS uses [transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, which is not well supported in cluster mode. Sentinal or other HA solution for Redis are needed.
