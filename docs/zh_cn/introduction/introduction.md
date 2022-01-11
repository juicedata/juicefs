---
title: JuiceFS 是什么？
sidebar_label: JuiceFS 是什么？
sidebar_position: 1
slug: .
---
#

![JuiceFS LOGO](../images/juicefs-logo.png)

**JuiceFS** 是一款面向云原生设计的高性能共享文件系统，在 Apache 2.0 开源协议下发布。提供完备的 [POSIX](https://en.wikipedia.org/wiki/POSIX) 兼容性，可将几乎所有对象存储接入本地作为海量本地磁盘使用，亦可同时在跨平台、跨地区的不同主机上挂载读写。

JuiceFS 采用「数据」与「元数据」分离存储的架构，从而实现文件系统的分布式设计。使用 JuiceFS 存储数据，数据本身会被持久化在[对象存储](../reference/how_to_setup_object_storage.md#支持的存储服务)（例如，Amazon S3），相对应的元数据可以按需持久化在 Redis、MySQL、TiKV、SQLite 等多种[数据库](../reference/how_to_setup_metadata_engine.md)中。

JuiceFS 提供了丰富的 API，适用于各种形式数据的管理、分析、归档、备份，可以在不修改代码的前提下无缝对接大数据、机器学习、人工智能等应用平台，为其提供海量、弹性、低价的高性能存储。运维人员不用再为可用性、灾难恢复、监控、扩容等工作烦恼，专注于业务开发，提升研发效率。同时运维细节的简化，也让运维团队更容易向 DevOps 团队转型。

## 核心特性

1. **POSIX 兼容**：像本地文件系统一样使用，无缝对接已有应用，无业务侵入性；
2. **HDFS 兼容**：完整兼容 [HDFS API](../deployment/hadoop_java_sdk.md)，提供更强的元数据性能；
3. **S3 兼容**：提供 [S3 网关](../deployment/s3_gateway.md) 实现 S3 协议兼容的访问接口；
4. **云原生**：通过 [CSI Driver](../deployment/how_to_use_on_kubernetes.md) 轻松地在 Kubernetes 中使用 JuiceFS；
5. **分布式设计**：同一文件系统可在上千台服务器同时挂载，高性能并发读写，共享数据；
6. **强一致性**：确认的文件修改会在所有服务器上立即可见，保证强一致性；
7. **强悍性能**：毫秒级延迟，近乎无限的吞吐量（取决于对象存储规模），查看[性能测试结果](../benchmark/benchmark.md)；
8. **数据安全**：支持传输中加密（encryption in transit）和静态加密（encryption at rest），[查看详情](../security/encrypt.md)；
9. **文件锁**：支持 BSD 锁（flock）和 POSIX 锁（fcntl）；
10. **数据压缩**：支持 [LZ4](https://lz4.github.io/lz4) 和 [Zstandard](https://facebook.github.io/zstd) 压缩算法，节省存储空间。

## 技术架构

JuiceFS 文件系统由三个部分组成：

1. **JuiceFS 客户端**：协调对象存储和元数据存储引擎，以及 POSIX、Hadoop、Kubernetes CSI Driver、S3 Gateway 等文件系统接口的实现；
2. **数据存储**：存储数据本身，支持本地磁盘、公有云或私有云对象存储、HDFS 等介质；
3. **元数据引擎**：存储数据对应的元数据（metadata）包含文件名、文件大小、权限组、创建修改时间和目录结构等，支持 Redis、MySQL、TiKV 等多种引擎；

![image](../images/juicefs-arch-new.png)

作为文件系统，JuiceFS 会分别处理数据及其对应的元数据，数据会被存储在对象存储中，元数据会被存储在元数据引擎中。

在 **数据存储** 方面，JuiceFS 支持几乎所有的公有云对象存储，同时也支持 OpenStack Swift、Ceph、MinIO 等支持私有化部署的开源对象存储。

在 **元数据存储** 方面，JuiceFS 采用多引擎设计，目前已支持 Redis、TiKV、MySQL/MariaDB、PostgreSQL、SQLite 等作为元数据服务引擎，也将陆续实现更多元数据存储引擎。欢迎 [提交 Issue](https://github.com/juicedata/juicefs/issues) 反馈你的需求。

在 **文件系统接口** 实现方面：

- 通过 **FUSE**，JuiceFS 文件系统能够以 POSIX 兼容的方式挂载到服务器，将海量云端存储直接当做本地存储来使用。
- 通过 **Hadoop Java SDK**，JuiceFS 文件系统能够直接替代 HDFS，为 Hadoop 提供低成本的海量存储。
- 通过 **Kubernetes CSI Driver**，JuiceFS 文件系统能够直接为 Kubernetes 提供海量存储。
- 通过 **S3 Gateway**，使用 S3 作为存储层的应用可直接接入，同时可使用 AWS CLI、s3cmd、MinIO client 等工具访问 JuiceFS 文件系统。

## 应用场景

JuiceFS 为海量数据存储设计，可以作为很多分布式文件系统和网络文件系统的替代，特别是以下场景：

- **大数据分析**：HDFS 兼容，没有任何特殊 API 侵入业务；与主流计算引擎（Spark、Presto、Hive 等）无缝衔接；无限扩展的存储空间；运维成本几乎为 0；完善的缓存机制，高于对象存储性能数倍。
- **机器学习**：POSIX 兼容，可以支持所有机器学习、深度学习框架；共享能力提升团队管理、使用数据效率。
- **容器集群中的持久卷**：Kubernetes CSI 支持；持久存储并与容器生存期独立；强一致性保证数据正确；接管数据存储需求，保证服务的无状态化。
- **共享工作区**：可以在任意主机挂载；没有客户端并发读写限制；POSIX 兼容已有的数据流和脚本操作。
- **数据备份**：在无限平滑扩展的存储空间备份各种数据，结合共享挂载功能，可以将多主机数据汇总至一处，做统一备份。

## 数据隐私

JuiceFS 是开源软件，你可以在 [GitHub](https://github.com/juicedata/juicefs) 找到完整的源代码。在使用 JuiceFS 存储数据时，数据会按照一定的规则被拆分成数据块并保存在你自己定义的对象存储或其它存储介质中，数据所对应的元数据则存储在你自己定义的数据库中。
