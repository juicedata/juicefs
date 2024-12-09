---
slug: /comparison/juicefs_vs_alluxio
---

# JuiceFS 对比 Alluxio

用户在存算分离的数据平台和 AI 训练加速场景中经常会比较 Alluxio 和 JuiceFS 两个产品。除了使用场景有相似之处，更多的原因是这两个产品都与对象存储相结合，都提供了访问数据时的缓存加速能力，看上去像是同一个场景中的「替代产品」，但两个产品又有很多的不同。本章详细介绍二者的功能区别，两个系统都是开源项目，但也都各自提供功能更为强大的企业版，本文的对比也会考虑到不同版本的区别，帮助你的团队进行技术选型。

Alluxio 的定位是在现有的存储系统之上提供缓存加速层，在实际项目中存储系统大多是对象存储系统。JuiceFS 的定位是为云环境设计的分布式文件系统，可以通过缓存机制加速数据访问。

从架构设计角度讲，Alluxio 与对象存储是两套系统，Alluxio 是业务应用于对象存储之间的中间件，维护多个节点中的存储空间形成一个缓存系统，存储应用访问过的热数据。

JuiceFS 用对象存储做数据持久层，对象存储可以看做是 JuiceFS 的一个内部组件，打比方说对象存储好像一块容量无限大的硬盘，JuiceFS 对这块「硬盘」进行格式化，JuiceFS 的元数据服务就是分区表，结合在一起形成完整的「文件系统」概念。

Alluxio 和 JuiceFS 虽然都能提供文件系统服务，但架构以及使用场景存在很大差异：Alluxio 的主要作用是为各个数据存储系统提供统一接入平台（你的数据仍存储在外部系统），为应用提供高速缓存层。而 JuiceFS 则是一个分布式高性能文件系统，你可以将其作为大数据存储平台，也可以用他来替换当前的存储系统，为你的业务增效降本。

考虑到二者的定位有很大不同，下方表格只能呈现各自作为文件系统角色时的功能特性，并不是一个「公平的对比」。JuiceFS 并不提供多数据源聚合功能，因此也无法与 Alluxio 进行比较。如果你对两个产品都感兴趣，请继续阅读表格下方的章节。

| 特性 | Alluxio | JuiceFS |
|:---:|:---:|:---:|
| 多级缓存 | 支持 | 支持 |
| Hadoop 兼容 | 支持 | 支持 |
| S3 兼容 | 支持 | 支持 |
| Kubernetes CSI 驱动 | 支持 | 支持 |
| WebDAV 协议 | 不支持 | 支持 |
| Hadoop 数据本地性 | 支持 | 支持 |
| 完全兼容 POSIX | 不支持 | 支持 |
| 一致性 | 不一定 | 强一致性|
| 数据压缩 | 不支持 | 支持 |
| 数据加密 | 不支持 | 支持 |
| 服务端运维 | 复杂 | 推荐直接使用云服务商托管服务，实现零运维 |
| 开发语言 | Java | Go |
| 开源协议 | Apache License 2.0 | Apache License 2.0 |
| 开源时间 | 2014 | 2021.1 |

## 架构与核心特性 {#architecture-and-key-features}

### 存储与缓存 {#storage-and-cache}

Alluxio 自身不是一个存储系统，而是一个强大的聚合层，来为不同的存储系统（比如 HDFS、NFS）提供统一接入和缓存服务。这也是为什么我们无法将存储和缓存拆开来讨论与对比，因为 Alluxio 自己的存储层，作用实际上就是提供缓存服务（更多关于架构信息请阅读其[官方文档](https://docs.alluxio.io/os/user/stable/cn/core-services/Caching.html)）。

在 Alluxio 的架构中，背后的存储系统称作「UFS」（Under File Storage），可想而知，这些存储系统都是外部系统，不受 Alluxio 管辖，他们各自的存储格式与 Alluxio 无关。

UFS 层让 Alluxio 能够聚合不同的文件系统，但 Alluxio 的重要作用是为这些存储系统提供缓存服务，因此 Alluxio 也有自己的数据存储，称作 Alluxio storage，会被部署成 Alluxio workers，用来提供缓存服务。

在 Alluxio 存储层，默认使用 64MB 作为缓存块大小，并且在缓存盘之上优先使用内存，为热数据提供更加高速的缓存服务。新版实验功能中也引入了[可调节缓存块大小的设计](https://docs.alluxio.io/os/user/stable/en/core-services/Caching.html#experimental-paging-worker-storage)，来调节缓存粒度，优化性能。

JuiceFS 是一个分布式文件系统，实现了自己的存储格式，文件会被视作一个个最大 64MB 的逻辑数据块（Chunk），再拆成 4MB 的 Block 上传至对象存储，作为最基本的物理存储单位。Block 也是本地缓存的粒度，相比 Alluxio 的 64MB 缓存块，JuiceFS 的粒度更小，更适合随机读取（例如 Parquet 和 ORC）工作负载，缓存管理也更有效率。JuiceFS 的存储设计，在[架构文档](../architecture.md#how-juicefs-store-files)中有更详细的介绍。

Alluxio 和 JuiceFS 都支持多级缓存，设计上各有特色，但都能够支持用硬盘、SSD、内存来灵活配置大容量或者高性能缓存，详见：

* [Alluxio 缓存](https://docs.alluxio.io/os/user/stable/cn/core-services/Caching.html)
* [JuiceFS 缓存](../../guide/cache.md)
* JuiceFS 企业版在社区版的基础上，支持更为强大的[分布式缓存](/docs/zh/cloud/guide/distributed-cache)

### 一致性 {#consistency}

JuiceFS 是一个强一致性的分布式文件系统，它的原子性依赖底层元数据引擎的事务支持（比如 [Redis 事务](https://redis.io/topics/transactions)），因此大部分元数据操作都具有原子性，例如重命名文件、删除文件、重命名目录。

Alluxio 自身并不是一个存储系统，但你依然可以通过 Alluxio 进行写入，不过原子性肯定就无法支持了，因为 Alluxio 依赖 UFS 来实现元数据操作，比如重命名文件操作会变成复制和删除操作。

继续讨论一致性之前，必须先简单了解 Alluxio 的写入是如何实现的。上一小节已经介绍过，Alluxio 存储层和 UFS 是分离的——你可以写存储层，也可以写 UFS，具体文件写入要如何在两个层之间协调，通过以下几种[写入策略](https://docs.alluxio.io/os/user/stable/cn/overview/Architecture.html#%E6%95%B0%E6%8D%AE%E6%B5%81%E5%86%99%E6%93%8D%E4%BD%9C)来控制：

* `MUST_CACHE`：写入 Alluxio worker 内存，性能最好，但 worker 异常会导致数据丢失。适合用来写入临时数据。
* `THROUGH`：直接写入 UFS，性能取决于底层存储。适合用来写入需要持久化，但最近不需要用到的数据。
* `CACHE_THROUGH`：同时写入 Alluxio worker 内存和底层 UFS
* `ASYNC_THROUGH`：先写入 Alluxio worker 内存，再异步提交给 UFS。

可想而知，任何在 Alluxio 中进行的数据写入，都面临着写入性能和一致性之间的取舍。为了达到最理想的性能，用户需要仔细研究写入场景，并为其分配合适的写入策略。显而易见的是，使用 `MUST_CACHE` 或 `ASYNC_THROUGH` 策略一定没有一致性保证，如果写入操作过程中发生故障，其状态是不可预测的。

以上是两个系统在写入一致性方面的对比，至于读数据，Alluxio 会按需从 UFS 加载元数据，并且它在启动时没有关于 UFS 的信息。默认情况下，Alluxio 期望对 UFS 的所有修改都通过 Alluxio 进行。如果直接对 UFS 进行更改，则需要手动或定期在 Alluxio 和 UFS 之间同步元数据，这也容易成为成为不一致的来源。

JuiceFS 则不存在这方面的问题，这是因为 JuiceFS 以元数据服务作为唯一的真实来源（single source of truth），对象存储在这个架构下，只作为数据存储使用，不管理任何元数据。

### 数据压缩 {#data-compression}

JuiceFS 支持使用 [LZ4](https://lz4.github.io/lz4) 或 [Zstandard](https://facebook.github.io/zstd) 来压缩数据。

Alluxio 本质上并不是一个存储系统，虽然你也可以通过 Alluxio 进行数据写入，但[并不支持压缩](https://alluxio.atlassian.net/browse/ALLUXIO-31)。

### 数据加密 {#data-encryption}

Alluxio 仅在[企业版](https://docs.alluxio.io/ee/user/stable/en/security/Security.html#encryption)支持数据加密。

JuiceFS 支持[传输中加密以及静态加密](../../security/encryption.md)。

## 客户端协议对比 {#client-protocol-comparison}

### POSIX

JuiceFS[完全兼容 POSIX](../../reference/posix_compatibility.md)，完整通过用于检验 POSIX 兼容性的 [pjdfstest](https://github.com/pjd/pjdfstest)，并以 99% 以上的成功率通过用于检验 Linux 软件可靠性的 [Linux Test Project](https://github.com/linux-test-project/ltp)，无缝对接已有应用。

除了 pjdfstest 的兼容性测试外，JuiceFS 支持 mmap、fallocate 文件打洞、xattr、BSD 锁（flock）和 POSIX 记录锁（fcntl）。

Alluxio 没有通过 POSIX 兼容性测试。[京东](https://www.slideshare.net/Alluxio/using-alluxio-posix-fuse-api-in-jdcom)的 pjdfstest 测试表明 Alluxio 不支持符号链接、truncate、fallocate、append、xattr、mkfifo、mknod 和 utimes。

### HDFS

二者均兼容 HDFS，包括 Hadoop 2.x 和 Hadoop 3.x，以及 Hadoop 生态系统中的各种组件。详见：

* [JuiceFS Hadoop SDK](../../deployment/hadoop_java_sdk.md)
* [Alluxio 集成 HDFS 作为底层存储](https://docs.alluxio.io/os/user/stable/cn/ufs/HDFS.html)

### S3

JuiceFS 实现了 [S3 网关](../../guide/gateway.md)，因此如果有需要，可以通过 S3 API 直接访问文件系统，也能使用 s3cmd、AWS CLI、MinIO Client（mc）等工具直接管理文件系统。

Alluxio 也支持大部分 S3 API，详见[文档](https://docs.alluxio.io/os/user/stable/cn/api/S3-API.html)。

### Kubernetes CSI Driver

二者均提供 Kubernetes CSI 驱动：

* [JuiceFS CSI Driver](/docs/zh/csi/introduction) 由 Juicedata 团队持续维护
* [Alluxio CSI Driver](https://github.com/Alluxio/alluxio/tree/master-2.x/integration/docker/csi) 由 Alluxio 团队持续维护，相对来说迭代速度较慢。

### WebDAV

JuiceFS 实现了 [WebDAV 服务](../../deployment/webdav.md)，用户可以通过 WebDAV 协议管理文件系统中的数据。

Alluxio 不支持 WebDAV 协议。

## 云上部署和运维 {#deployment-and-operation}

这一小节只讨论两个产品的社区版，两个产品的企业版都能获取技术支持服务，因此不作讨论。

Alluxio 的架构可以分为 3 个组件：master、worker 和客户端。一个典型的集群由一个主节点（master）、多个备用主节点（standby master）、一个作业主节点（job master）、多个备用作业主节点（standby job master）、多个 worker 和 job worker 组成。需要自己部署及运维这些节点，详见[文档](https://docs.alluxio.io/os/user/stable/cn/overview/Getting-Started.html#%E9%83%A8%E7%BD%B2-alluxio)。

JuiceFS 使用 Redis 或者其它流行的数据库作为[元数据引擎](../../reference/how_to_set_up_metadata_engine.md)，大部分公有云服务商都提供这些数据库的全托管服务，你可以直接将其作为 JuiceFS 元数据引擎，没有任何运维负担。
