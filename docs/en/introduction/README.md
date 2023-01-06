---
title: Introduction to JuiceFS
sidebar_position: 1
slug: .
pagination_next: introduction/architecture
---

**JuiceFS** is an open source, high-performance distributed file system designed for the cloud, released under the Apache License 2.0. It provides full [POSIX](https://en.wikipedia.org/wiki/POSIX) compatibility, allowing almost all kinds of object storage to be used locally as massive local disks and to be mounted and read on different cross-platform and cross-region hosts at the same time.

JuiceFS separates "data" and "metadata" storage, files are split into chunks and stored in [object storage](../guide/how_to_set_up_object_storage.md#supported-object-storage) like Amazon S3, and the corresponding metadata can be stored in various [databases](../guide/how_to_set_up_metadata_engine.md) such as Redis, MySQL, TiKV, SQLite, etc., based on the scenarios and requirements.

JuiceFS provides rich APIs for various forms of data management, analysis, archiving, and backup. It can seamlessly interface with big data, machine learning, artificial intelligence and other application platforms without modifying code, and provide massive, elastic and high-performance storage at low cost. With JuiceFS, you do not need to worry about availability, disaster recovery, monitoring and expansion, and thus maintainence work can be greatly reduced, perfect for DevOps.

## Features

1. **POSIX Compatible** JuiceFS can be used like a local file system, making it easy to integrate with existing applications.
2. **HDFS Compatible**: JuiceFS is fully compatible with [HDFS API](../deployment/hadoop_java_sdk.md), which can enhance metadata performance.
3. **S3 Compatible**: JuiceFS provides [S3 gateway](../deployment/s3_gateway.md) to implement an S3-compatible access interface.
4. **Cloud-Native**: It is easy to use JuiceFS in Kubernetes via [CSI Driver](../deployment/how_to_use_on_kubernetes.md).
5. **Distributed**: Each file system can be mounted on thousands of servers at the same time with high-performance concurrent reads and writes and shared data.
6. **Strong Consistency**: Any committed changes in files will be visible on all servers immediately.
7. **Outstanding Performance**: The latency can be down to a few milliseconds, and the throughput can be nearly unlimited depending on object storage scale (see [performance test results](../benchmark/benchmark.md)).
8. **Data Security**: JuiceFS supports encryption in transit and encryption at rest (view [Details](../security/encrypt.md)).
9. **File Lock**: JuiceFS supports BSD lock (flock) and POSIX lock (fcntl).
10. **Data Compression**: JuiceFS supports [LZ4](https://lz4.github.io/lz4) and [Zstandard](https://facebook.github.io/zstd) compression algorithms to save storage space.

## Scenarios

JuiceFS is designed for massive data storage and can be used as an alternative to many distributed file systems and network file systems, especially for the following scenarios:

- **Big Data**: JuiceFS is compatible with HDFS and can be seamlessly integrated with mainstream computing engines (Spark, Presto, Hive, etc.), bringing much better performance than directly using object storage.
- **Machine Learning**: JuiceFS is compatible with POSIX, and supports all machine learning and deep learning frameworks; As a shareable file storage, JuiceFS can improve the efficiency of team management and data usage.
- **Kubernetes**: JuiceFS supports Kubernetes CSI, providing decoupled persistent storage for pods so that your application can be stateless, also great for data sharing among containers.
- **Shared Workspace**: JuiceFS file system can be mounted on any host; no restrictions to client concurrent read/write; POSIX compatible with existing data flow and scripting operations.
- **Data Backup**: Backup all kinds of data in scalable storage space without limitation; combined with the shared mount feature, data from multiple hosts can be aggregated into one place and then backed up together.

## Data Privacy

JuiceFS is open source software, and the source code can be found at [GitHub](https://github.com/juicedata/juicefs). When using JuiceFS to store data, the data is split into chunks according to certain rules and stored in self-defined object storage or other storage media, and the corresponding metadata is stored in self-defined database.
