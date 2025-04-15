---
title: Introduction to JuiceFS
sidebar_position: 1
slug: .
pagination_next: introduction/architecture
---

[**JuiceFS**](https://github.com/juicedata/juicefs) is an open-source, high-performance distributed file system designed for the cloud, released under the Apache License 2.0. By providing full [POSIX](https://en.wikipedia.org/wiki/POSIX) compatibility, it allows almost all kinds of object storage to be used as massive local disks and to be mounted and accessed on different hosts across platforms and regions.

JuiceFS separates "data" and "metadata" storage. Files are split into chunks and stored in [object storage](../reference/how_to_set_up_object_storage.md#supported-object-storage) like Amazon S3. The corresponding metadata can be stored in various [databases](../reference/how_to_set_up_metadata_engine.md) such as Redis, MySQL, TiKV, and SQLite, based on the scenarios and requirements.

JuiceFS provides rich APIs for various forms of data management, analysis, archiving, and backup. It seamlessly interfaces with big data, machine learning, artificial intelligence and other application platforms without modifying code, and delivers massive, elastic, and high-performance storage at low cost. With JuiceFS, you do not need to worry about availability, disaster recovery, monitoring, and scalability. This greatly reduces maintenance work and makes it an excellent choice for DevOps.

## Features {#features}

- **POSIX Compatible**: JuiceFS can be used like a local file system, making it easy to integrate with existing applications.
- **HDFS Compatible**: JuiceFS is fully compatible with the [HDFS API](../deployment/hadoop_java_sdk.md), which can enhance metadata performance.
- **S3 Compatible**: JuiceFS provides an [S3 gateway](../guide/gateway.md) to implement an S3-compatible access interface.
- **Cloud-Native**: It is easy to use JuiceFS in Kubernetes via the [CSI Driver](../deployment/how_to_use_on_kubernetes.md).
- **Distributed**: Each file system can be mounted on thousands of servers at the same time with high-performance concurrent reads and writes and shared data.
- **Strong Consistency**: Any changes committed to files are immediately visible on all servers.
- **Outstanding Performance**: JuiceFS achieves millisecond-level latency and nearly unlimited throughput depending on the object storage scale (see [performance test results](../benchmark/benchmark.md)).
- **Data Security**: JuiceFS supports encryption in transit and encryption at rest (view [Details](../security/encryption.md)).
- **File Lock**: JuiceFS supports BSD lock (flock) and POSIX lock (fcntl).
- **Data Compression**: JuiceFS supports the [LZ4](https://lz4.github.io/lz4) and [Zstandard](https://facebook.github.io/zstd) compression algorithms to save storage space.

## Scenarios {#scenarios}

JuiceFS is designed for massive data storage and can be used as an alternative to many distributed file systems and network file systems, especially in the following scenarios:

- **Big Data**: JuiceFS is compatible with HDFS and can be seamlessly integrated with mainstream computing engines such as Spark, Presto, and Hive, bringing much better performance than directly using object storage.
- **Machine Learning**: JuiceFS is compatible with POSIX and supports all machine learning and deep learning frameworks. As a shareable file storage, JuiceFS can improve the efficiency of team management and data usage.
- **Kubernetes**: JuiceFS supports Kubernetes CSI, providing decoupled persistent storage for pods so that your application can be stateless, also great for data sharing among containers.
- **Shared Workspace**: JuiceFS file system can be mounted on any host, allowing concurrent read/write operations without limitations. Its POSIX compatibility ensures smooth data flow and supports scripting operations.
- **Data Backup**: JuiceFS provides scalable storage space for backing up all kinds of data. With its shared mount feature, data from multiple hosts can be aggregated into one place and then backed up together.

## Data privacy {#data-privacy}

JuiceFS is an open-source software available on [GitHub](https://github.com/juicedata/juicefs). When using JuiceFS to store data, the data is split into chunks according to specific rules and stored in custom object storage or other storage media, and the corresponding metadata is stored in a custom database.

## More info {#more-info}

* **Use case**: For more use cases of similar scenarios, please visit [User Stories](https://juicefs.com/en/blog/user-stories).
* **Join the community**: Welcome to join [Slack](https://go.juicefs.com/slack) to discuss with JuiceFS users.
* **AI assistant**: If you encounter any problems, you are welcome to use the "Ask AI" feature (in the bottom right corner) to get assistance from the AI assistant. The knowledge base of the AI ​​assistant comes from documentation and related content on GitHub.
