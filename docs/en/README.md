# JuiceFS User Manual

[![license](https://img.shields.io/badge/license-Apache%20v2.0-blue)](https://github.com/juicedata/juicefs/blob/main/LICENSE) [![Go Report](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/badge/github.com/juicedata/juicefs) [![Join Slack](https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack)](https://go.juicefs.com/slack)

![JuiceFS LOGO](images/juicefs-logo.svg)

**JuiceFS** is a high-performance [POSIX](https://en.wikipedia.org/wiki/POSIX) file system released under Apache License 2.0, particularly designed for the cloud-native environment. The data, stored via JuiceFS, will be persisted in object storage (e.g. Amazon S3), and the corresponding metadata can be persisted in various database engines such as Redis, MySQL, and SQLite based on the scenarios and requirements.

With JuiceFS, massive cloud storage can be directly connected to big data, machine learning, artificial intelligence, and various application platforms in production environments. Without modifying code, the massive cloud storage can be used as efficiently as local storage.


## Highlighted Features

1. **Fully POSIX-compatible**: Use as a local file system, seamlessly docking with existing applications without breaking business workflow.
2. **Fully Hadoop-compatible**: JuiceFS' [Hadoop Java SDK](deployment/hadoop_java_sdk.md) is compatible with Hadoop 2.x and Hadoop 3.x as well as a variety of components in the Hadoop ecosystem.
3. **S3-compatible**:  JuiceFS' [S3 Gateway](deployment/s3_gateway.md) provides an S3-compatible interface.
4. **Cloud Native**:  A [Kubernetes CSI driver](deployment/how_to_use_on_kubernetes.md) is provided for easily using JuiceFS in Kubernetes.
5. **Shareable**: JuiceFS is a shared file storage that can be read and written by thousands of clients.
6. **Strong Consistency**: The confirmed modification will be immediately visible on all the servers mounted with the same file system.
7. **Outstanding Performance**: The latency can be as low as a few milliseconds, and the throughput can be expanded nearly unlimitedly (depending on the size of the object storage). [Test results](benchmark/benchmark.md)
8. **Data Encryption**: Supports data encryption in transit and at rest (please refer to [the guide](security/encrypt.md) for more information).
9. **Global File Locks**: JuiceFS supports both BSD locks (flock) and POSIX record locks (fcntl).
10. **Data Compression**: JuiceFS supports [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data.
