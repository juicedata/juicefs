# JuiceFS 对比 Alluxio

[Alluxio](https://www.alluxio.io)（/əˈlʌksio/）是大数据和机器学习生态系统中的数据访问层。最初作为研究项目「Tachyon」，它是在加州大学伯克利分校的 [AMPLab](https://en.wikipedia.org/wiki/AMPLab) 作为创建者 2013 年的博士论文创建的。Alluxio 于 2014 年开源。

下表显示了 Alluxio 和 JuiceFS 之间的主要功能差异。

| 特性                  | Alluxio            | JuiceFS |
| --------------------- | -------            | ------- |
| 存储格式              | Object             | Block   |
| 缓存粒度              | 64MiB              | 4MiB    |
| 多级缓存              | ✓                  | ✓       |
| Hadoop 兼容           | ✓                  | ✓       |
| S3 兼容               | ✓                  | ✓       |
| Kubernetes CSI Driver | ✓                  | ✓       |
| Hadoop 数据本地性     | ✓                  | ✓       |
| 完全兼容 POSIX        | ✕                  | ✓       |
| 原子元数据操作        | ✕                  | ✓       |
| 一致性                | ✕                  | ✓       |
| 数据压缩              | ✕                  | ✓       |
| 数据加密              | ✕                  | ✓       |
| 零运维                | ✕                  | ✓       |
| 开发语言              | Java               | Go      |
| 开源协议              | Apache License 2.0 | AGPLv3  |
| 开源时间              | 2011               | 2021.1  |

### 存储格式

JuiceFS 中一个文件的[存储格式](how_juicefs_store_files.md)包含三个层级：chunk、slice 和 block。一个文件将被分割成多个块，并被压缩和加密（可选）存储到对象存储中。

Alluxio 将文件作为「对象」存储到 UFS。文件不会像 JuiceFS 那样被拆分成 block。

### 缓存粒度

JuiceFS 的[默认块大小](how_juicefs_store_files.md)为 4MiB，相比 Alluxio 的 64MiB，粒度更小。较小的块大小更适合随机读取（例如 Parquet 和 ORC）工作负载，即缓存管理将更有效率。

### Hadoop 兼容

JuiceFS [完整兼容 HDFS](hadoop_java_sdk.md)。不仅兼容 Hadoop 2.x 和 Hadoop 3.x，还兼容 Hadoop 生态系统中的各种组件。

### Kubernetes CSI Driver

JuiceFS 提供了 [Kubernetes CSI Driver](https://github.com/juicedata/juicefs-csi-driver) 来帮助在 Kubernetes 中便捷使用 JuiceFS。Alluxio 也提供了 [Kubernetes CSI Driver](https://github.com/Alluxio/alluxio-csi)，但是这个项目维护得不够活跃，也没有得到 Alluxio 的官方支持。

### 完全兼容 POSIX

JuiceFS [完全兼容 POSIX](posix_compatibility.md)。来自[京东](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom)的一个 pjdfstest 显示 Alluxio 没有通过 POSIX 兼容性测试，例如 Alluxio 不支持符号链接、truncate、fallocate、append、xattr、mkfifo、mknod 和 utimes。除了 pjdfstest 涵盖的东西外，JuiceFS 还提供了关闭再打开（close-to-open）一致性、原子元数据操作、mmap、fallocate 打洞、xattr、BSD 锁（flock）和 POSIX 记录锁（fcntl）。

### 原子元数据操作

Alluxio 中的元数据操作有两个步骤：第一步是修改 Alluxio master 的状态，第二步是向 UFS 发送请求。可以看到，元数据操作不是原子的，当操作正在执行或发生任何故障时，其状态是不可预测的。Alluxio 依赖 UFS 来实现元数据操作，比如重命名文件操作会变成复制和删除操作。

感谢 [Redis 事务](https://redis.io/topics/transactions)，**JuiceFS 的大部分元数据操作都是原子的**，例如重命名文件、删除文件、重命名目录。您不必担心一致性和性能。

### 一致性

Alluxio 根据需要从 UFS 加载元数据，并且它在启动时没有关于 UFS 的信息。默认情况下，Alluxio 期望对 UFS 的所有修改都通过 Alluxio 进行。如果直接对 UFS 进行更改，则需要手动或定期在 Alluxio 和 UFS 之间同步元数据。正如[「原子元数据操作」](#原子元数据操作)部分所说，两步元数据操作可能会导致不一致。

JuiceFS 提供元数据和数据的强一致性。**JuiceFS 的元数据服务是唯一的真实来源（single source of truth），不是 UFS 的镜像。** 元数据服务不依赖对象存储来获取元数据。对象存储只是被视为无限制的块存储。JuiceFS 和对象存储之间没有任何不一致之处。

### 数据压缩

JuiceFS 支持使用 [LZ4](https://lz4.github.io/lz4) 或 [Zstandard](https://facebook.github.io/zstd) 来压缩您的所有数据。Alluxio 没有这个功能。

### 数据加密

JuiceFS 支持传输中加密（encryption in transit）以及静态加密（encryption at rest）。Alluxio 社区版没有这个功能，但是[企业版](https://docs.alluxio.io/ee/user/stable/en/operation/Security.html#end-to-end-data-encryption)有。

### 零运维

Alluxio 的架构可以分为 3 个组件：master、worker 和客户端。一个典型的集群由一个主节点（master）、多个备用主节点（standby master）、一个作业主节点（job master）、多个备用作业主节点（standby job master）、多个 worker 和 job worker 组成。您需要自己运维这些节点。

JuiceFS 使用 Redis 或者[其它系统](databases_for_metadata.md)作为元数据引擎。您可以轻松使用由公有云提供商托管的服务作为 JuiceFS 的元数据引擎，没有任何运维负担。
