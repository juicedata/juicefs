---
title: What is JuiceFS?
sidebar_label: What is JuiceFS
sidebar_position: 1
slug: .
---
#

![JuiceFS LOGO](../images/juicefs-logo.png)

**JuiceFS** is a high-performance shared file system designed for cloud-native use and released under the Apache License 2.0. It provides full [POSIX](https://en.wikipedia.org/wiki/POSIX) compatibility, allowing almost all object stores to be used locally as massive local disks, and to be mounted and read on different hosts across platforms and regions at the same time.

JuiceFS implements the distributed design of file system by separating "data" and "metadata" storage architecture. When using JuiceFS to store data, the data itself is persisted in [object storage](../reference/how_to_setup_object_storage.md#supported-object-storage) (e.g., Amazon S3), and the corresponding metadata can be persisted on-demand in various [databases](../reference/how_to_setup_metadata_engine.md) such as Redis, MySQL, TiKV, SQLite, etc.

JuiceFS provides rich APIs for various forms of data management, analysis, archiving, and backup, and can seamlessly interface with big data, machine learning, artificial intelligence and other application platforms without modifying code, providing them with massive, elastic, and low-cost high-performance storage. It allows you to focus on business development and improve R&D efficiency without worrying about availability, disaster recovery, monitoring, and expansion. Make it easier for operations and maintenance teams to transform to DevOps teams.

## Features

1. **POSIX Compatible**: used like a local file system, seamlessly interfacing with existing applications.
2. **HDFS Compatible**: Full compatibility with the [HDFS API](../deployment/hadoop_java_sdk.md), providing enhanced metadata performance.
3. **S3 Compatible**: Provides [S3 gateway](../deployment/s3_gateway.md) implementing the S3-compatible access interface.
4. **Cloud-Native**: Use JuiceFS in Kubernetes easily via [CSI Driver](../deployment/how_to_use_on_kubernetes.md).
5. **Distributed**: the same file system can be mounted on thousands of servers at the same time, with high performance concurrent reads and writes and shared data.
6. **Strong Consistency**: confirmed file changes are immediately visible on all servers, ensuring strong consistency.
7. **Better Performance**: millisecond latency, nearly unlimited throughput (depending on object storage scale), see [performance test results](../benchmark/benchmark.md).
8. **Data Security**: Supports encryption in transit and encryption at rest, [View Details](../security/encrypt.md).
9. **File lock**: support for BSD lock (flock) and POSIX lock (fcntl).
10. **Data Compression**: Supports [LZ4](https://lz4.github.io/lz4) and [Zstandard](https://facebook.github.io/zstd) compression algorithms to save storage space.

## Architecture

The JuiceFS file system consists of three parts:

1. **JuiceFS Client**: coordinating object storage and metadata engine, and implementation of file system interfaces such as POSIX, Hadoop, Kubernetes CSI Driver, S3 Gateway, etc..
2. **Data Storage**: storage of the data itself, supporting media such as local disk, public or private cloud object storage, HDFS, etc.
3. **Metadata Engine**: storage data corresponding metadata contains file name, file size, permission group, creation and modification time and directory structure, etc., supporting Redis, MySQL, TiKV and other engines.

![image](../images/juicefs-arch-new.png)

As a file system, JuiceFS handles the data and its corresponding metadata separately, with the data being stored in the object store and the metadata being stored in the metadata engine.

In terms of **data storage**, JuiceFS supports almost all public cloud object stores, as well as OpenStack Swift, Ceph, MinIO and other open source object stores that support private deployments.

In terms of **metadata storage**, JuiceFS is designed with multiple engines, and currently supports Redis, TiKV, MySQL/MariaDB, PostgreSQL, SQLite, etc. as metadata service engines, and will implement more multiple data storage engines one after another. Welcome to [Submit Issue](https://github.com/juicedata/juicefs/issues) to feedback your requirements.

In terms of **File System Interface** implementation:

- With **FUSE**, the JuiceFS file system can be mounted to the server in a POSIX-compatible manner to use massive cloud storage directly as local storage.
- With **Hadoop Java SDK**, JuiceFS file system can directly replace HDFS and provide low-cost mass storage for Hadoop.
- With the **Kubernetes CSI Driver**, the JuiceFS file system can directly provide mass storage for Kubernetes.
- With **S3 Gateway**, applications using S3 as the storage layer can directly access the JuiceFS file system and use tools such as AWS CLI, s3cmd, and MinIO client.

## Scenarios

JuiceFS is designed for massive data storage and can be used as an alternative to many distributed file systems and network file systems, especially for the following scenarios.

- **Big Data Analytics**: HDFS-compatible without any special API intrusion into the business; seamless integration with mainstream computing engines (Spark, Presto, Hive, etc.); infinitely scalable storage space; almost 0 operation and maintenance cost; perfect caching mechanism, several times higher than object storage performance.
- **Machine Learning**: POSIX compatible, can support all machine learning, deep learning frameworks; sharing capabilities to improve the efficiency of team management, use of data.
- **Persistent volumes in container clusters**: Kubernetes CSI support; persistent storage and independent from container lifetime; strong consistency to ensure correct data; take over data storage requirements to ensure statelessness of the service.
- **Shared Workspace**: can be mounted on any host; no client concurrent read/write restrictions; POSIX compatible with existing data flow and scripting operations.
- **Data Backup**: Back up all kinds of data in unlimited smoothly scalable storage space; combined with the shared mount feature, you can aggregate multi-host data to one place and do unified backup.

## Data Privacy

JuiceFS is open source software, and you can find the full source code at [GitHub](https://github.com/juicedata/juicefs). When using JuiceFS to store data, the data is split into chunks according to certain rules and stored in your own defined object storage or other storage media, and the metadata corresponding to the data is stored in your own defined database.
