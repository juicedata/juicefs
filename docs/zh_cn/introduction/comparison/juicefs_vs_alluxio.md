---
slug: /comparison/juicefs_vs_alluxio
---

# JuiceFS 对比 Alluxio

[Alluxio](https://www.alluxio.io)（/əˈlʌksio/）是大数据和机器学习生态系统中的数据访问层。最初作为研究项目「Tachyon」，它是在加州大学伯克利分校的 [AMPLab](https://en.wikipedia.org/wiki/AMPLab) 在 2013 年的博士论文创建的。

Alluxio 和 JuiceFS 虽然都能提供文件系统服务，但架构以及使用场景存在很大差异：Alluxio 的主要作用是为各个数据存储系统提供统一接入平台（你的数据仍存储在外部系统），为应用提供高速缓存层。而 JuiceFS 则是一个分布式高性能文件系统，你可以将其作为大数据存储平台，也可以用他来替换当前的存储系统，为你的业务增效降本。

本章详细介绍二者的功能区别，帮助你的团队进行技术选型。你可以通过下表速查两者的关键特性对比，然后在本文中选取感兴趣的话题详细阅读。

| 特性 | Alluxio | JuiceFS |
| --- | --- | --- |
| 存储格式 | Object | Block |
| 缓存粒度 | 64MiB | 64MB 逻辑块 + 4MB 物理存储块 |
| 多级缓存 | ✓ | ✓ |
| Hadoop 兼容 | ✓ | ✓ |
| S3 兼容 | ✓ | ✓ |
| Kubernetes CSI Driver | ✓ | ✓ |
| Hadoop 数据本地性 | ✓ | ✓ |
| 完全兼容 POSIX | ✕ | ✓ |
| 原子元数据操作 | ✕ | ✓ |
| 一致性 | ✕ | ✓ |
| 数据压缩 | ✕ | ✓ |
| 数据加密 | ✕ | ✓ |
| 服务端运维 | 复杂 | 推荐直接使用云服务商托管服务，实现零运维 |
| 开发语言 | Java | Go |
| 开源协议 | Apache License 2.0 | Apache License 2.0 |
| 开源时间 | 2014 | 2021.1 |

## 存储格式与缓存

Alluxio 自身不是一个存储系统，而是一个文件系统的聚合层，来为不同的存储系统（比如 HDFS，NFS）提供统一接入和缓存服务。这也是为什么我们无法将存储和缓存拆开来讨论与对比，因为 Alluxio 自己的存储层，作用实际上就是提供缓存服务（更多关于架构信息请阅读其[官方文档](https://docs.alluxio.io/os/user/stable/en/core-services/Caching.html?q=64MB#alluxio-storage-overview)）。

在 Alluxio 的架构中，背后的存储系统称作「UFS」（Under File Storage），可想而知，这些存储系统都是外部系统，不受 Alluxio 管辖，他们各自的存储格式与 Alluxio 无关。

UFS 层让 Alluxio 能够聚合不同的文件系统，但 Alluxio 的重要作用是为这些存储系统提供缓存服务，因此 Alluxio 也有自己的数据存储，称作 Alluxio storage，会被部署成 Alluxio workers，用来提供缓存服务。

在 Alluxio 存储层，默认使用 64MB 作为缓存块大小，在新版实验功能中也引入了[可调节缓存块大小的设计](https://docs.alluxio.io/os/user/stable/en/core-services/Caching.html?q=64MB#experimental-paging-worker-storage)，来调节缓存粒度，优化性能。

JuiceFS 是一个分布式文件系统，实现了自己的存储格式，文件会被视作一个个最大 64MB 的逻辑数据块（Chunk），再拆成 4MB 的 Block 上传至对象存储，作为最基本的物理存储单位，Block 也是本地缓存的粒度，相比 Alluxio 的 64MB 缓存块，JuiceFS 的粒度更小，更适合随机读取（例如 Parquet 和 ORC）工作负载，缓存管理也更有效率。JuiceFS 的存储设计，在[架构文档](../architecture.md#how-juicefs-store-files)中有更详细的介绍。

## Hadoop 兼容

二者均兼容 HDFS，包括 Hadoop 2.x 和 Hadoop 3.x，以及 Hadoop 生态系统中的各种组件。详见：

* [JuiceFS Hadoop SDK](../../deployment/hadoop_java_sdk.md)
* [Alluxio集成HDFS作为底层存储](https://docs.alluxio.io/os/user/stable/en/ufs/HDFS.html)

## Kubernetes CSI Driver

二者均提供 Kubernetes CSI 驱动，但项目质量有区别，详见：

* [JuiceFS CSI Driver](https://juicefs.com/docs/zh/csi/introduction/) 由 Juicedata 持续维护
* [Alluxio CSI Driver](https://github.com/Alluxio/alluxio-csi) 项目维护力度不大，也没有得到 Alluxio 的官方支持

## 完全兼容 POSIX

JuiceFS[完全兼容 POSIX](../../reference/posix_compatibility.md)，完整通过用于检验 POSIX 兼容性的 [pjdfstest](https://github.com/pjd/pjdfstest)，并以 99% 以上的成功率通过用于检验 Linux 软件可靠性的 [Linux Test Project](https://github.com/linux-test-project/ltp)，无缝对接已有应用。

除了 pjdfstest 的兼容性测试外，JuiceFS 支持 mmap、fallocate 文件打洞、xattr、BSD 锁（flock）和 POSIX 记录锁（fcntl）。

Alluxio 没有通过 POSIX 兼容性测试。[京东](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom)的 pjdfstest 测试表明 Alluxio 不支持符号链接、truncate、fallocate、append、xattr、mkfifo、mknod 和 utimes。

## 原子元数据操作

Alluxio 中的元数据操作有两个步骤：第一步是修改 Alluxio master 的状态，第二步是向 UFS 发送请求。可以看到，元数据操作不是原子的，当操作正在执行或发生任何故障时，其状态是不可预测的。Alluxio 依赖 UFS 来实现元数据操作，比如重命名文件操作会变成复制和删除操作。

感谢 [Redis 事务](https://redis.io/topics/transactions)，**JuiceFS 的大部分元数据操作都是原子的**，例如重命名文件、删除文件、重命名目录。您不必担心一致性和性能。

## 一致性

Alluxio 根据需要从 UFS 加载元数据，并且它在启动时没有关于 UFS 的信息。默认情况下，Alluxio 期望对 UFS 的所有修改都通过 Alluxio 进行。如果直接对 UFS 进行更改，则需要手动或定期在 Alluxio 和 UFS 之间同步元数据。正如[「原子元数据操作」](#原子元数据操作)部分所说，两步元数据操作可能会导致不一致。

JuiceFS 提供元数据和数据的强一致性。**JuiceFS 的元数据服务是唯一的真实来源（single source of truth），不是 UFS 的镜像。** 元数据服务不依赖对象存储来获取元数据。对象存储只是被视为无限制的块存储。JuiceFS 和对象存储之间没有任何不一致之处。

## 数据压缩

JuiceFS 支持使用 [LZ4](https://lz4.github.io/lz4) 或 [Zstandard](https://facebook.github.io/zstd) 来压缩您的所有数据。Alluxio 没有这个功能。

## 数据加密

JuiceFS 支持传输中加密（encryption in transit）以及静态加密（encryption at rest）。Alluxio 社区版没有这个功能，但是[企业版](https://docs.alluxio.io/ee/user/stable/en/operation/Security.html#end-to-end-data-encryption)有。

## 零运维

Alluxio 的架构可以分为 3 个组件：master、worker 和客户端。一个典型的集群由一个主节点（master）、多个备用主节点（standby master）、一个作业主节点（job master）、多个备用作业主节点（standby job master）、多个 worker 和 job worker 组成。您需要自己运维这些节点。

JuiceFS 使用 Redis 或者[其它系统](../../reference/how_to_set_up_metadata_engine.md)作为元数据引擎。您可以轻松使用由公有云提供商托管的服务作为 JuiceFS 的元数据引擎，没有任何运维负担。
