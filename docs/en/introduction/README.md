---
title: Introduction to JuiceFS
sidebar_label: Introduction to JuiceFS
sidebar_position: 1
slug: .
---
# Introduction

**JuiceFS** is a high-performance shared file system designed for cloud-native use and released under the Apache License 2.0. It provides full [POSIX](https://en.wikipedia.org/wiki/POSIX) compatibility, allowing almost all kinds of object storage to be used locally as massive local disks and to be mounted and read on different cross-platform and cross-region hosts at the same time.

JuiceFS implements a distributed file system by adopting the architecture that separates "data" and "metadata" storage. When using JuiceFS to store data, the data itself is persisted in [object storage](../guide/how_to_set_up_object_storage.md#supported-object-storage) (e.g. Amazon S3), and the corresponding metadata can be persisted in various [databases](../guide/how_to_set_up_metadata_engine.md) such as Redis, MySQL, TiKV, SQLite, etc., based on the scenarios and requirements.

JuiceFS provides rich APIs for various forms of data management, analysis, archiving, and backup. It can seamlessly interface with big data, machine learning, artificial intelligence and other application platforms without modifying code, and provide massive, elastic and high-performance storage at low cost. With JuiceFS, you do not need to worry about availability, disaster recovery, monitoring and expansion, and thus operation and maintaince work can be remarkably simplified, which helps companies focus more on business development and R&D efficiency improvement.

## Features

1. **POSIX Compatible** JuiceFS can be used like a local file system as it seamlessly interfaces with existing applications.
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

JuiceFS is designed for massive data storage and can be used as an alternative to many distributed file systems and network file systems, especially for the following scenarios.

- **Big Data Analytics**: compatible with HDFS without requiring extra API; seamlessly integrated with mainstream computing engines (Spark, Presto, Hive, etc.); unlimited storage space; nearly zero operation and maintenance costs; well-developed caching mechanism, and better performance than object storage.
- **Machine Learning**: compatible with POSIX, supporting all machine learning and deep learning frameworks; shareable file  storage, which can improve the efficiency of team management and data use.
- **Persistent volumes in container clusters**: supporting Kubernetes CSI; persistent storage and independent of container lifetime; strong consistency to ensure that date stored is correct; take over data storage requirements to ensure statelessness of the service.
- **Shared Workspace**: JuiceFS file system can be mounted on any host; no restrictions to client concurrent read/write; POSIX compatible with existing data flow and scripting operations.
- **Data Backup**: Back up all kinds of data in scalable storage space without limitation; combined with the shared mount feature, data from multiple hosts can be aggregated into one place and then backed up together.

## Data Privacy

JuiceFS is open source software, and the source code can be found at [GitHub](https://github.com/juicedata/juicefs). When using JuiceFS to store data, the data is split into chunks according to certain rules and stored in self-defined object storage or other storage media, and the corresponding metadata is stored in self-defined database.
